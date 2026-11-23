// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux && !android

package tray

import (
	"os"

	"github.com/godbus/dbus/v5"
)

// platformSupported needs a desktop session bus. Without one (headless Pi,
// SSH, sudo) the tray is skipped and the program runs as before.
func platformSupported() bool {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" && os.Getenv("XDG_RUNTIME_DIR") == "" {
		return false
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	return conn.Connected()
}
