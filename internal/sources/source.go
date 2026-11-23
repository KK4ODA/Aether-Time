// Package sources contains the time references Aether Time can learn the
// correct time from: NTP, HTTPS, GPS receivers (NMEA, gpsd) and the timing of
// FT8/FT4 decodes reported by WSJT-X, JTDX and MSHV.
package sources

import (
	"context"
	"log/slog"
	"time"
)

// Sample is one measurement of the local clock against a reference.
type Sample struct {
	Source string
	// Offset is reference time minus local clock time: add it to the clock to correct it.
	Offset time.Duration
	// Error is the estimated maximum error of Offset.
	Error time.Duration
	// Taken is the local clock reading (with monotonic component) when Offset was valid.
	Taken time.Time
	// Stratum of the reference: 0 for a primary reference such as GPS.
	Stratum int
	Note    string
}

// Position is a geographic fix from a GPS source.
type Position struct {
	Lat    float64   `json:"lat"`
	Lon    float64   `json:"lon"`
	Alt    float64   `json:"alt"`
	Grid   string    `json:"grid"`
	Source string    `json:"source"`
	At     time.Time `json:"at"`
}

// Store persists small learned values (GPS latency, DT bias) across restarts.
type Store interface {
	GetFloat(key string) (float64, bool)
	SetFloat(key string, v float64)
}

// Host is the engine as seen by a source.
type Host interface {
	Now() time.Time
	Submit(Sample)
	// Reference returns the fused offset of every source except exclude,
	// for calibrating one source against the others.
	Reference(exclude string) (offset, maxErr time.Duration, ok bool)
	// Fresh reports whether the named source currently has a usable sample.
	Fresh(id string) bool
	SetPosition(Position)
	Position() (Position, bool)
	Store() Store
	Logger() *slog.Logger
}

// Status is a source's self-description for the dashboard.
type Status struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	State    string `json:"state"` // ok, waiting, error, idle
	Message  string `json:"message"`
	Priority int    `json:"priority"`
	Details  any    `json:"details,omitempty"`
}

// Source states.
const (
	StateOK      = "ok"
	StateWaiting = "waiting"
	StateError   = "error"
	StateIdle    = "idle"
)

// Source is a time reference.
type Source interface {
	ID() string
	Priority() int
	// MaxAge is how long a sample stays usable.
	MaxAge() time.Duration
	// Run measures until ctx is cancelled, delivering samples to host.Submit.
	Run(ctx context.Context, host Host)
	Status() Status
	// ClockStepped tells the source the system clock was changed by d, so any
	// measurements it keeps can be shifted to stay consistent.
	ClockStepped(d time.Duration)
	// Trigger requests a measurement as soon as possible ("Sync now").
	Trigger()
}

// TxReporter is implemented by sources that know when the radio is transmitting.
type TxReporter interface {
	Transmitting() bool
}

// sleepCtx waits for d or until ctx is done / wake fires. It returns false if ctx is done.
func sleepCtx(ctx context.Context, d time.Duration, wake <-chan struct{}) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	case <-wake:
		return true
	}
}

// trigger is a non-blocking wake-up channel helper.
type trigger chan struct{}

func newTrigger() trigger { return make(trigger, 1) }

func (t trigger) fire() {
	select {
	case t <- struct{}{}:
	default:
	}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func fromMs(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
