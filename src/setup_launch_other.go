//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// launchDetached starts the bot in its own session so closing the terminal
// that ran setup does not take the bot down with it. Output goes to bot.log
// as always.
func launchDetached(exe, dir string) error {
	cmd := exec.Command(exe)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// createDesktopShortcut links the bot onto the user's desktop.
func createDesktopShortcut(exe, dir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	link := filepath.Join(home, "Desktop", "a_gnome_trader")
	_ = os.Remove(link)
	return os.Symlink(exe, link)
}
