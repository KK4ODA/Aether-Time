// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux && !darwin && !windows

package conflicts

func detect() []Finding { return nil }
