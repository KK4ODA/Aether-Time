// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package svc

import "errors"

// RunElevated is only available on Windows; elsewhere use sudo.
func RunElevated(args ...string) error {
	return errors.New("run in a terminal: sudo aether-time service install")
}

// CanElevate reports whether RunElevated is supported.
func CanElevate() bool { return false }
