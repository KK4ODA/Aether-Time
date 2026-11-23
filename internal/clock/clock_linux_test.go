// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package clock

import (
	"testing"
	"time"
)

// The kernel requires 0 <= usec < 1e6, with the sign carried by sec.
func TestSetOffsetTimexEncoding(t *testing.T) {
	for _, d := range []time.Duration{
		1500 * time.Millisecond, -1500 * time.Millisecond, 250 * time.Microsecond,
		-250 * time.Microsecond, -3 * time.Hour, 0,
	} {
		tx := setOffsetTimex(d)
		if tx.Modes != adjSetOffset {
			t.Fatalf("modes %x", tx.Modes)
		}
		usec := int64(tx.Time.Usec)
		if usec < 0 || usec >= 1e6 {
			t.Fatalf("%v: usec %d out of range", d, usec)
		}
		got := time.Duration(int64(tx.Time.Sec))*time.Second + time.Duration(usec)*time.Microsecond
		if diff := got - d; diff < -time.Microsecond || diff > time.Microsecond {
			t.Fatalf("%v encoded as %v", d, got)
		}
	}
}

func TestFakeClock(t *testing.T) {
	f := NewFake(2 * time.Second)
	if err := f.Step(-2 * time.Second); err != nil || f.Offset() != 0 {
		t.Fatalf("offset %v err %v", f.Offset(), err)
	}
	f.Denied = true
	if err := f.Step(time.Second); err != ErrNotPermitted {
		t.Fatalf("want ErrNotPermitted, got %v", err)
	}
}
