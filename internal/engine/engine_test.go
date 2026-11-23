package engine

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/clock"
	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/sources"
)

type fakeSource struct {
	id    string
	prio  int
	mu    sync.Mutex
	steps []time.Duration
	tx    bool
}

func (f *fakeSource) ID() string                              { return f.id }
func (f *fakeSource) Priority() int                           { return f.prio }
func (f *fakeSource) MaxAge() time.Duration                   { return time.Minute }
func (f *fakeSource) Run(ctx context.Context, h sources.Host) { <-ctx.Done() }
func (f *fakeSource) Status() sources.Status                  { return sources.Status{ID: f.id, State: sources.StateOK} }
func (f *fakeSource) Trigger()                                {}
func (f *fakeSource) ClockStepped(d time.Duration) {
	f.mu.Lock()
	f.steps = append(f.steps, d)
	f.mu.Unlock()
}
func (f *fakeSource) Transmitting() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tx
}

const msD = time.Millisecond

func newTestEngine(t *testing.T, mutate func(*config.Config), srcs ...*fakeSource) (*Engine, *clock.Fake) {
	t.Helper()
	cfg := config.Default()
	cfg.NTPServer.Enabled = false
	if mutate != nil {
		mutate(&cfg)
	}
	clk := clock.NewFake(0)
	var ss []sources.Source
	for _, s := range srcs {
		ss = append(ss, s)
	}
	dir := t.TempDir()
	e := New(cfg, Deps{
		Clock:       clk,
		State:       LoadState(filepath.Join(dir, "state.json")),
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		SyncLogPath: filepath.Join(dir, "sync-log.csv"),
		Sources:     ss,
	})
	return e, clk
}

func submit(e *Engine, id string, off, errb time.Duration) {
	e.Submit(sources.Sample{Source: id, Offset: off, Error: errb, Taken: e.Now()})
}

func TestStepCorrection(t *testing.T) {
	ntpSrc := &fakeSource{id: "ntp", prio: 2}
	e, clk := newTestEngine(t, nil, ntpSrc)
	submit(e, "ntp", -2*time.Second, 10*msD)
	e.tick()
	if got := clk.Steps(); len(got) != 1 || got[0] != -2*time.Second {
		t.Fatalf("steps %v", got)
	}
	if len(ntpSrc.steps) != 1 || ntpSrc.steps[0] != -2*time.Second {
		t.Fatalf("source not notified: %v", ntpSrc.steps)
	}
	if r := e.latest["ntp"]; r.Offset != 0 {
		t.Fatalf("stored sample not shifted: %v", r.Offset)
	}
	e.tick()
	if snap := e.Snapshot(); snap.State != StateSynced {
		t.Fatalf("state %s (%s)", snap.State, snap.Note)
	}
	b, err := os.ReadFile(e.csv)
	if err != nil || !strings.Contains(string(b), "step,-2000.000") {
		t.Fatalf("sync log: %q %v", b, err)
	}
}

func TestBelowThresholdIgnored(t *testing.T) {
	e, clk := newTestEngine(t, nil, &fakeSource{id: "ntp"})
	submit(e, "ntp", 10*msD, 2*msD)
	e.tick()
	if len(clk.Steps()) != 0 {
		t.Fatal("should not correct below threshold")
	}
}

func TestSmoothCorrection(t *testing.T) {
	e, clk := newTestEngine(t, nil, &fakeSource{id: "ntp"})
	submit(e, "ntp", 100*msD, 2*msD)
	for i := 0; i < 6; i++ {
		e.tick()
	}
	steps := clk.Steps()
	var total time.Duration
	for _, s := range steps {
		if s > 25*msD {
			t.Fatalf("smooth step %v exceeds the limit", s)
		}
		total += s
	}
	if total < 90*msD || total > 100*msD {
		t.Fatalf("smoothing applied %v in %d steps", total, len(steps))
	}
}

func TestMonitorMode(t *testing.T) {
	e, clk := newTestEngine(t, func(c *config.Config) { c.Mode = config.ModeMonitor }, &fakeSource{id: "ntp"})
	submit(e, "ntp", 3*time.Second, 2*msD)
	e.tick()
	if len(clk.Steps()) != 0 {
		t.Fatal("monitor mode must not touch the clock")
	}
	if s := e.Snapshot(); s.OffsetMs == nil || *s.OffsetMs != 3000 {
		t.Fatalf("offset not reported: %+v", s.OffsetMs)
	}
}

func TestMaxCorrection(t *testing.T) {
	e, clk := newTestEngine(t, func(c *config.Config) {
		c.Adjust.MaxCorrectionS = 3600
		c.Adjust.MinIntervalS = 0
	}, &fakeSource{id: "ntp"})
	// The first sync is allowed to be large (dead RTC battery).
	submit(e, "ntp", 2*time.Hour, 2*msD)
	e.tick()
	// Later huge jumps are refused.
	submit(e, "ntp", -3*time.Hour, 2*msD)
	e.tick()
	if got := clk.Steps(); len(got) != 1 || got[0] != 2*time.Hour {
		t.Fatalf("steps %v", got)
	}
	if !strings.Contains(e.Snapshot().Note, "refused") {
		t.Fatalf("note %q", e.Snapshot().Note)
	}
}

func TestDeferDuringTransmit(t *testing.T) {
	ft8 := &fakeSource{id: "wsjtx", tx: true}
	e, clk := newTestEngine(t, nil, ft8)
	submit(e, "wsjtx", 700*msD, 30*msD)
	e.tick()
	if len(clk.Steps()) != 0 {
		t.Fatal("corrected while transmitting")
	}
	ft8.mu.Lock()
	ft8.tx = false
	ft8.mu.Unlock()
	e.tick()
	if got := clk.Steps(); len(got) != 1 || got[0] != 700*msD {
		t.Fatalf("steps %v", got)
	}
}

func TestFalsetickerExcluded(t *testing.T) {
	e, clk := newTestEngine(t, nil, &fakeSource{id: "ntp"}, &fakeSource{id: "gps"}, &fakeSource{id: "wsjtx"})
	submit(e, "ntp", 1000*msD, 10*msD)
	submit(e, "gps", 1003*msD, 5*msD)
	submit(e, "wsjtx", -1500*msD, 50*msD)
	e.tick()
	snap := e.Snapshot()
	if len(snap.Rejected) != 1 || snap.Rejected[0] != "wsjtx" {
		t.Fatalf("rejected %v", snap.Rejected)
	}
	if got := clk.Steps(); len(got) != 1 || got[0] < 1000*msD || got[0] > 1003*msD {
		t.Fatalf("steps %v", got)
	}
}

func TestSyncNowIgnoresThreshold(t *testing.T) {
	e, clk := newTestEngine(t, nil, &fakeSource{id: "ntp"})
	e.SyncNow()
	time.Sleep(2 * time.Millisecond)
	submit(e, "ntp", 5*msD, 1*msD)
	e.tick()
	if got := clk.Steps(); len(got) != 1 || got[0] != 5*msD {
		t.Fatalf("steps %v", got)
	}
}

func TestNoPermission(t *testing.T) {
	e, clk := newTestEngine(t, nil, &fakeSource{id: "ntp"})
	clk.Denied, clk.DenyWhy = true, "not admin"
	submit(e, "ntp", time.Second, msD)
	e.tick()
	if len(clk.Steps()) != 0 || !strings.Contains(e.Snapshot().Note, "monitor only") {
		t.Fatalf("note %q", e.Snapshot().Note)
	}
}

func TestHoldoverUsesDrift(t *testing.T) {
	e, clk := newTestEngine(t, nil, &fakeSource{id: "ntp"})
	e.st.setDrift(50) // clock loses 50 µs per second
	e.lastRef = &refPoint{at: time.Now().Add(-10 * time.Minute), phase: 0, err: 2 * msD}
	e.firstDone = true
	e.tick()
	snap := e.Snapshot()
	steps := clk.Steps()
	if len(steps) == 0 {
		t.Fatalf("no holdover correction; note %q", snap.Note)
	}
	var total time.Duration
	for _, s := range steps {
		total += s
	}
	if total < 20*msD || total > 31*msD {
		t.Fatalf("holdover corrected %v, want ≈30ms", total)
	}
}

func TestDriftTracker(t *testing.T) {
	d := newDriftTracker()
	base := d.start
	for i := 0; i <= 60; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		phase := 12e-6 * at.Sub(base).Seconds() // +12 ppm
		d.add(at, phase+float64(i%3-1)*1e-4, 1e-3)
	}
	ppm, se, ok := d.PPM()
	if !ok || ppm < 11.5 || ppm > 12.5 {
		t.Fatalf("ppm %v ± %v ok=%v", ppm, se, ok)
	}
	// A foreign clock step resets the fit instead of corrupting it.
	d.add(base.Add(62*time.Minute), 5, 1e-3)
	if _, _, ok := d.PPM(); ok {
		t.Fatal("expected reset after a jump")
	}
}

func TestStatePersistence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s := LoadState(p)
	s.SetFloat("gps.latency_ms", 87.5)
	s.setDrift(-3.25)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2 := LoadState(p)
	if v, ok := s2.GetFloat("gps.latency_ms"); !ok || v != 87.5 {
		t.Fatalf("latency %v %v", v, ok)
	}
	if v, ok := s2.drift(); !ok || v != -3.25 {
		t.Fatalf("drift %v %v", v, ok)
	}
}
