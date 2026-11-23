// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package svc

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// RunElevated starts this executable with administrator rights (UAC prompt).
func RunElevated(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"") {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		quoted[i] = a
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	return windows.ShellExecute(0, verb, file, params, dir, windows.SW_HIDE)
}

// CanElevate reports whether RunElevated is supported.
func CanElevate() bool { return true }
