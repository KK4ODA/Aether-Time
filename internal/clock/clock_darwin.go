// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package clock

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func stepClock(d time.Duration) error {
	tv := unix.NsecToTimeval(time.Now().Add(d).UnixNano())
	if err := unix.Settimeofday(&tv); err != nil {
		if errors.Is(err, unix.EPERM) {
			return ErrNotPermitted
		}
		return err
	}
	return nil
}

func canAdjust() (bool, string) {
	if os.Geteuid() == 0 {
		return true, ""
	}
	return false, "run with sudo or install the service (sudo aether-time service install)"
}
