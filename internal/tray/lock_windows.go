// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package tray

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockFile(f *os.File) bool {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	return err == nil
}
