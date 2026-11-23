// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux && !darwin && !windows

package clock

import "time"

func stepClock(time.Duration) error { return ErrNotPermitted }

func canAdjust() (bool, string) { return false, "setting the clock is not supported on this platform" }
