// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !(windows || (linux && !android) || (darwin && cgo))

package tray

// Supported is false: this build has no tray support (for example a macOS
// build made without cgo).
func Supported() bool { return false }

// Run does nothing in builds without tray support.
func Run(Options) {}

// Quit does nothing in builds without tray support.
func Quit() {}
