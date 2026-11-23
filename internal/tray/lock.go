// SPDX-License-Identifier: GPL-3.0-or-later

package tray

import (
	"os"
	"path/filepath"
)

// Lock makes sure only one tray icon runs per user. It returns a release
// function, or ok=false if another tray already holds the lock.
func Lock() (release func(), ok bool) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return func() {}, true
	}
	dir = filepath.Join(dir, "AetherTime")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return func() {}, true
	}
	f, err := os.OpenFile(filepath.Join(dir, "tray.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}, true
	}
	if !lockFile(f) {
		f.Close()
		return nil, false
	}
	return func() { f.Close() }, true
}
