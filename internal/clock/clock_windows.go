// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package clock

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32       = windows.NewLazySystemDLL("kernel32.dll")
	procSetSystemTime = modkernel32.NewProc("SetSystemTime")
)

func stepClock(d time.Duration) error {
	// SetSystemTime has millisecond resolution; round to the nearest ms.
	t := time.Now().Add(d).Add(500 * time.Microsecond).UTC()
	st := windows.Systemtime{
		Year:         uint16(t.Year()),
		Month:        uint16(t.Month()),
		DayOfWeek:    uint16(t.Weekday()),
		Day:          uint16(t.Day()),
		Hour:         uint16(t.Hour()),
		Minute:       uint16(t.Minute()),
		Second:       uint16(t.Second()),
		Milliseconds: uint16(t.Nanosecond() / int(time.Millisecond)),
	}
	r, _, err := procSetSystemTime.Call(uintptr(unsafe.Pointer(&st)))
	if r == 0 {
		if err == windows.ERROR_PRIVILEGE_NOT_HELD || err == windows.ERROR_ACCESS_DENIED {
			return ErrNotPermitted
		}
		return err
	}
	return nil
}

func canAdjust() (bool, string) {
	if windows.GetCurrentProcessToken().IsElevated() {
		return true, ""
	}
	return false, "run as Administrator or install the Windows service (Settings → Install service)"
}
