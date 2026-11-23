// SPDX-License-Identifier: GPL-3.0-or-later

// Package conflicts detects other time-synchronization software that would
// fight Aether Time over the system clock.
package conflicts

// Finding describes one conflicting program.
type Finding struct {
	Name   string `json:"name"`
	Advice string `json:"advice"`
}

// Detect returns the conflicting programs found on this machine.
func Detect() []Finding { return detect() }
