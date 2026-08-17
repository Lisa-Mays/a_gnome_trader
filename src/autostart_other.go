//go:build !darwin

package main

import "errors"

// Autostart is only offered on macOS for now. Windows users get a desktop
// shortcut; Linux users typically write their own systemd unit.

func autostartSupported() bool { return false }

var errNoAutostart = errors.New("automatic start is not supported on this platform")

func installAutostartLogin(exe, dir string) error { return errNoAutostart }
func installAutostartBoot(exe, dir string) error  { return errNoAutostart }
