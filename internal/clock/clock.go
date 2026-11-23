// Package clock reads and adjusts the operating system clock.
package clock

import (
	"errors"
	"sync"
	"time"
)

// Clock is the system clock as seen by the sync engine. A fake implementation
// is used in tests.
type Clock interface {
	// Now returns the current system time (with a monotonic reading).
	Now() time.Time
	// Step adds d to the system clock in one jump.
	Step(d time.Duration) error
	// CanAdjust reports whether this process may change the clock, and if not, why.
	CanAdjust() (bool, string)
}

// ErrNotPermitted is returned when the process lacks the right to set the clock.
var ErrNotPermitted = errors.New("not permitted to set the system clock")

// System is the real operating system clock.
type System struct{}

// Now implements Clock.
func (System) Now() time.Time { return time.Now() }

// Step implements Clock.
func (System) Step(d time.Duration) error {
	if d == 0 {
		return nil
	}
	return stepClock(d)
}

// CanAdjust implements Clock.
func (System) CanAdjust() (bool, string) { return canAdjust() }

// Fake is a Clock whose offset from real time is controlled by tests.
type Fake struct {
	mu       sync.Mutex
	offset   time.Duration
	steps    []time.Duration
	Denied   bool
	DenyWhy  string
	StepHook func(d time.Duration)
}

// NewFake returns a fake clock that is off by offset (positive = ahead of real time).
func NewFake(offset time.Duration) *Fake { return &Fake{offset: offset} }

// Now implements Clock.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return time.Now().Add(f.offset)
}

// Step implements Clock.
func (f *Fake) Step(d time.Duration) error {
	f.mu.Lock()
	if f.Denied {
		f.mu.Unlock()
		return ErrNotPermitted
	}
	f.offset += d
	f.steps = append(f.steps, d)
	hook := f.StepHook
	f.mu.Unlock()
	if hook != nil {
		hook(d)
	}
	return nil
}

// CanAdjust implements Clock.
func (f *Fake) CanAdjust() (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Denied {
		return false, f.DenyWhy
	}
	return true, ""
}

// Offset returns how far the fake clock is ahead of real time.
func (f *Fake) Offset() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.offset
}

// Steps returns the steps applied so far.
func (f *Fake) Steps() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.steps...)
}
