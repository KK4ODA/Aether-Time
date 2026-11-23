// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows || (darwin && cgo)

package tray

// platformSupported is always true on Windows and macOS desktops.
func platformSupported() bool { return true }
