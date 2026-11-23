// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package tray

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(f *os.File) bool {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) == nil
}
