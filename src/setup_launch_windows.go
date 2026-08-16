//go:build windows

package main

import (
	"fmt"
	"os/exec"
)

// launchDetached starts the bot in its own console window so the setup
// window can be closed without taking the bot down with it.
func launchDetached(exe, dir string) error {
	return exec.Command("cmd", "/c", "start", "a_gnome_trader", "/D", dir, exe).Run()
}

// createDesktopShortcut drops a .lnk on the user's desktop pointing at exe.
func createDesktopShortcut(exe, dir string) error {
	ps := fmt.Sprintf(
		`$ws = New-Object -ComObject WScript.Shell; `+
			`$s = $ws.CreateShortcut((Join-Path ([Environment]::GetFolderPath('Desktop')) 'a_gnome_trader.lnk')); `+
			`$s.TargetPath = '%s'; $s.WorkingDirectory = '%s'; $s.Save()`, exe, dir)
	return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).Run()
}
