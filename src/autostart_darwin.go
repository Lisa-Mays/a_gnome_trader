//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// macOS autostart. Two flavors:
//
//	login: a per-user LaunchAgent. Starts the bot when this user logs in and
//	       restarts it after a crash. No password needed.
//	boot:  a system LaunchDaemon. Starts the bot when the Mac powers on,
//	       before anyone logs in, and restarts it after a crash. Installing
//	       it asks for an administrator password through the standard macOS
//	       dialog.
//
// Both use launchd's KeepAlive with SuccessfulExit=false, so a clean stop
// (launchctl bootout, or a normal shutdown) stays stopped, while a crash is
// restarted after a short pause.

const autostartLabel = "com.agnometrader.bot"

func autostartSupported() bool { return true }

func autostartPlist(exe, dir string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>WorkingDirectory</key>
	<string>%s</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, autostartLabel, exe, dir, filepath.Join(dir, "launchd.log"), filepath.Join(dir, "launchd.log"))
}

// installAutostartLogin writes a LaunchAgent for the current user and loads
// it, which also starts the bot right away.
func installAutostartLogin(exe, dir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	agents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agents, 0755); err != nil {
		return err
	}
	plistPath := filepath.Join(agents, autostartLabel+".plist")
	if err := os.WriteFile(plistPath, []byte(autostartPlist(exe, dir)), 0644); err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+autostartLabel).Run()
	if err := exec.Command("launchctl", "bootstrap", domain, plistPath).Run(); err != nil {
		// Probably still loaded from a previous run; restart it in place.
		if err2 := exec.Command("launchctl", "kickstart", "-k", domain+"/"+autostartLabel).Run(); err2 != nil {
			return fmt.Errorf("launchctl could not load %s: %v", plistPath, err)
		}
	}
	return nil
}

// installAutostartBoot writes a system LaunchDaemon and loads it, which also
// starts the bot right away. macOS shows its standard administrator password
// dialog for the privileged part.
func installAutostartBoot(exe, dir string) error {
	tmp := filepath.Join(dir, "."+autostartLabel+".plist")
	if err := os.WriteFile(tmp, []byte(autostartPlist(exe, dir)), 0644); err != nil {
		return err
	}
	defer os.Remove(tmp)
	dst := "/Library/LaunchDaemons/" + autostartLabel + ".plist"
	sh := strings.Join([]string{
		fmt.Sprintf("cp %s %s", shq(tmp), shq(dst)),
		fmt.Sprintf("chown root:wheel %s", shq(dst)),
		fmt.Sprintf("chmod 644 %s", shq(dst)),
		fmt.Sprintf("launchctl bootout system/%s 2>/dev/null || true", autostartLabel),
		"sleep 1",
		fmt.Sprintf("launchctl bootstrap system %s || launchctl kickstart -k system/%s", shq(dst), autostartLabel),
	}, " && ")
	out, err := exec.Command("osascript", "-e",
		fmt.Sprintf("do shell script %q with administrator privileges", sh)).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "User canceled") {
			return fmt.Errorf("the administrator password dialog was cancelled")
		}
		if msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// shq single-quotes a string for /bin/sh.
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
