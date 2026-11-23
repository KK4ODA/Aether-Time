// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package tray

// DetachOwnConsole is only needed on Windows.
func DetachOwnConsole() {}
