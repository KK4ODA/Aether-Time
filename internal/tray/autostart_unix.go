// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package tray

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

func autostartFile() (string, error) {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist"), nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", "aether-time.desktop"), nil
}

// AutostartEnabled reports whether the tray starts at login.
func AutostartEnabled() bool {
	p, err := autostartFile()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// EnableAutostart starts the tray at login for the current user.
func EnableAutostart() error {
	exe, err := executable()
	if err != nil {
		return err
	}
	p, err := autostartFile()
	if err != nil {
		return err
	}
	content := desktopEntry(exe)
	if runtime.GOOS == "darwin" {
		content = launchAgent(exe)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// DisableAutostart removes the login item.
func DisableAutostart() error {
	p, err := autostartFile()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
