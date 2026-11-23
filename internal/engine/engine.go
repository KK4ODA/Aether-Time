// Package engine fuses the time sources, decides when and how to correct the
// system clock, learns the clock's drift for holdover, and serves the
// disciplined time to the LAN.
package engine

import (
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/clock"
	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/fusion"
	"github.com/KK4ODA/Aether-Time/internal/ntp"
	"github.com/KK4ODA/Aether-Time/internal/sources"
)

// Sync states shown in the UI.
const (
	StateSynced   = "synced"   // a reference is available and the clock is within threshold
	StateAdjusted = "tracking" // a reference is available, a correction is pending or in progress
	StateHoldover = "holdover" // no reference; following the learned drift
	StateNoRef    = "no-reference"
	StateStarting = "starting"
)

// Deps are long-lived objects shared across engine restarts.
type Deps struct {
	Clock   clock.Clock
	State   *State
	History *History
	Logger  *slog.Logger
	// SyncLogPath is the CSV file corrections are appended to ("" disables it).
	SyncLogPath string
	// Sources overrides the configured sources (tests).
	Sources []sources.Source
}

// Engine is the synchronization engine. Create with New, then Run.
type Engine struct {
	cfg  config.Config
	clk  clock.Clock
	st   *State
	hist *History
	log  *slog.Logger
	srcs []sources.Source
	byID map[string]sources.Source
	csv  string

	mu         sync.Mutex
	latest     map[string]sampleRec
	pos        *sources.Position
	applied    time.Duration // sum of all corrections (for the drift phase)
	firstDone  bool
	lastCorrAt time.Time // real time of the last correction
	smoothing  bool
	force      *forceReq
	current    *estimate
	lastRef    *refPoint
	drift      *driftTracker
	state      string
	note       string
	alerts     []Alert
	lastPoint  time.Time
	monitor    bool // runtime override: monitor only
	ntpServer  *ntp.Server
	ntpErr     string
	started    time.Time
}

type sampleRec struct {
	sources.Sample
	recv time.Time // real receive time, for ageing
}

type forceReq struct {
	at  time.Time
	ttl time.Time
}

type estimate struct {
	offset, err  time.Duration
	used, reject []string
	holdover     bool
	stratum      int
}

type refPoint struct {
	at    time.Time // real time
	phase time.Duration
	err   time.Duration
}

// Alert is a notable event shown in the UI.
type Alert struct {
	At    time.Time `json:"at"`
	Level string    `json:"level"` // info, warn, error
	Text  string    `json:"text"`
}

// New creates an engine for cfg.
func New(cfg config.Config, deps Deps) *Engine {
	e := &Engine{
		cfg:     cfg,
		clk:     deps.Clock,
		st:      deps.State,
		hist:    deps.History,
		log:     deps.Logger,
		csv:     deps.SyncLogPath,
		latest:  map[string]sampleRec{},
		byID:    map[string]sources.Source{},
		drift:   newDriftTracker(),
		state:   StateStarting,
		started: time.Now(),
	}
	if e.clk == nil {
		e.clk = clock.System{}
	}
	if e.st == nil {
		e.st = LoadState("")
	}
	if e.hist == nil {
		e.hist = NewHistory()
	}
	if e.log == nil {
		e.log = slog.Default()
	}
	if deps.Sources != nil {
		e.srcs = deps.Sources
	} else {
		e.srcs = BuildSources(cfg)
	}
	for _, s := range e.srcs {
		e.byID[s.ID()] = s
	}
	if !cfg.Log.SyncLog {
		e.csv = ""
	}
	return e
}

// BuildSources creates the sources enabled in cfg.
func BuildSources(cfg config.Config) []sources.Source {
	var out []sources.Source
	if cfg.GPS.Enabled {
		out = append(out, sources.NewGPS(cfg.GPS))
	}
	if cfg.GPSD.Enabled {
		out = append(out, sources.NewGPSD(cfg.GPSD))
	}
	if cfg.NTP.Enabled {
		out = append(out, sources.NewNTP(cfg.NTP))
	}
	if cfg.WSJTX.Enabled {
		out = append(out, sources.NewWSJTX(cfg.WSJTX))
	}
	if cfg.HTTPS.Enabled {
		out = append(out, sources.NewHTTPS(cfg.HTTPS))
	}
	return out
}

// Run starts the sources and the control loop and blocks until ctx is done.
func (e *Engine) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, s := range e.srcs {
		wg.Add(1)
		go func(s sources.Source) {
			defer wg.Done()
			s.Run(ctx, e)
		}(s)
	}
	if e.cfg.NTPServer.Enabled {
		e.startNTPServer(ctx)
	}
	if ok, why := e.clk.CanAdjust(); !ok && e.cfg.Mode == config.ModeAuto {
		e.alert("warn", "Monitor only: this process may not change the clock — "+why)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	save := time.NewTicker(5 * time.Minute)
	defer save.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			_ = e.st.Save()
			return
		case <-tick.C:
			e.tick()
		case <-save.C:
			if err := e.st.Save(); err != nil {
				e.log.Warn("saving state", "err", err)
			}
		}
	}
}

func (e *Engine) startNTPServer(ctx context.Context) {
	srv := &ntp.Server{Addr: e.cfg.NTPServer.Listen, Now: e.clk.Now, State: e.ntpState}
	if err := srv.Listen(); err != nil {
		e.mu.Lock()
		e.ntpErr = err.Error()
		e.mu.Unlock()
		e.alert("error", "NTP server could not start on "+e.cfg.NTPServer.Listen+": "+err.Error())
		return
	}
	e.mu.Lock()
	e.ntpServer = srv
	e.mu.Unlock()
	e.log.Info("NTP server listening", "addr", srv.LocalAddr())
	go func() {
		if err := srv.Serve(ctx); err != nil {
			e.alert("error", "NTP server stopped: "+err.Error())
		}
	}()
}

// ntpState tells the NTP server what to advertise.
func (e *Engine) ntpState() ntp.ServerState {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur := e.current
	if cur == nil || cur.err > 500*time.Millisecond {
		return ntp.ServerState{Synced: false}
	}
	strat := cur.stratum + 1
	ref := "LOCL"
	switch {
	case cur.holdover:
		ref, strat = "HOLD", strat+1
	case contains(cur.used, "gps") || contains(cur.used, "gpsd"):
		ref = "GPS"
	case contains(cur.used, "ntp"):
		ref = "NTP"
	case contains(cur.used, "https"):
		ref = "HTTP"
	case contains(cur.used, "wsjtx"):
		ref = "FT8"
	}
	if strat < 1 {
		strat = 1
	}
	if strat > ntp.MaxStratum {
		strat = ntp.MaxStratum
	}
	refAt := e.clk.Now()
	if !e.lastCorrAt.IsZero() {
		refAt = refAt.Add(-time.Since(e.lastCorrAt))
	}
	return ntp.ServerState{
		Synced:         true,
		Stratum:        uint8(strat),
		RefID:          ntp.ASCIIRefID(ref),
		RefTime:        refAt,
		RootDispersion: cur.err + absDur(cur.offset),
	}
}

// ---- sources.Host implementation ----

// Now implements sources.Host.
func (e *Engine) Now() time.Time { return e.clk.Now() }

// Submit implements sources.Host.
func (e *Engine) Submit(s sources.Sample) {
	if s.Error <= 0 {
		s.Error = fusion.MinError
	}
	e.mu.Lock()
	e.latest[s.Source] = sampleRec{Sample: s, recv: time.Now()}
	e.mu.Unlock()
	if e.cfg.Log.Debug {
		e.log.Debug("sample", "source", s.Source, "offset_ms", ms(s.Offset), "error_ms", ms(s.Error), "note", s.Note)
	}
}

// Reference implements sources.Host.
func (e *Engine) Reference(exclude string) (time.Duration, time.Duration, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	est, ok := e.fuseLocked(exclude)
	if !ok {
		return 0, 0, false
	}
	return est.offset, est.err, true
}

// Fresh implements sources.Host.
func (e *Engine) Fresh(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.latest[id]
	if !ok {
		return false
	}
	src := e.byID[id]
	return src != nil && time.Since(r.recv) < src.MaxAge()
}

// SetPosition implements sources.Host.
func (e *Engine) SetPosition(p sources.Position) {
	e.mu.Lock()
	first := e.pos == nil || e.pos.Grid != p.Grid
	e.pos = &p
	e.mu.Unlock()
	if first {
		e.log.Info("GPS position", "grid", p.Grid, "lat", p.Lat, "lon", p.Lon)
	}
}

// Position implements sources.Host.
func (e *Engine) Position() (sources.Position, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pos == nil {
		return sources.Position{}, false
	}
	return *e.pos, true
}

// Store implements sources.Host.
func (e *Engine) Store() sources.Store { return e.st }

// Logger implements sources.Host.
func (e *Engine) Logger() *slog.Logger { return e.log }

// ---- control ----

const agingRate = 20e-6 // assumed frequency error (s/s) when ageing samples without a drift estimate

// fuseLocked combines the fresh samples, optionally excluding one source.
func (e *Engine) fuseLocked(exclude string) (estimate, bool) {
	ppm, sePPM, driftOK := e.drift.PPM()
	var ests []fusion.Estimate
	strata := map[string]int{}
	ids := make([]string, 0, len(e.latest))
	for id := range e.latest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := e.latest[id]
		if id == exclude {
			continue
		}
		src := e.byID[id]
		if src == nil {
			continue
		}
		age := time.Since(r.recv)
		if age > src.MaxAge() {
			continue
		}
		off, errb := r.Offset, r.Error
		if driftOK {
			off += time.Duration(ppm * 1e-6 * float64(age))
			errb += time.Duration((sePPM + 1) * 1e-6 * float64(age))
		} else {
			errb += time.Duration(agingRate * float64(age))
		}
		ests = append(ests, fusion.Estimate{ID: id, Offset: off, Error: errb})
		strata[id] = r.Stratum
	}
	if len(ests) == 0 {
		return estimate{}, false
	}
	if e.cfg.Selection == config.SelectPriority {
		best := ests[0]
		for _, x := range ests[1:] {
			if e.byID[x.ID].Priority() < e.byID[best.ID].Priority() {
				best = x
			}
		}
		ests = []fusion.Estimate{best}
	}
	res, ok := fusion.Combine(ests)
	if !ok {
		return estimate{}, false
	}
	minStrat := 16
	for _, id := range res.Used {
		if strata[id] < minStrat {
			minStrat = strata[id]
		}
	}
	return estimate{offset: res.Offset, err: res.Error, used: res.Used, reject: res.Reject, stratum: minStrat}, true
}

// tick runs once a second: fuse, track drift, decide on a correction.
func (e *Engine) tick() {
	now := time.Now()
	e.mu.Lock()
	est, ok := e.fuseLocked("")
	if ok {
		e.lastRef = &refPoint{at: now, phase: e.applied + est.offset, err: est.err}
		e.drift.add(now, (e.applied + est.offset).Seconds(), est.err.Seconds())
		if ppm, _, good := e.drift.PPM(); good && now.Sub(e.started) > time.Minute {
			e.st.setDrift(ppm)
		}
	} else if e.cfg.Adjust.Holdover && e.lastRef != nil {
		ppm, have := e.driftPPMLocked()
		elapsed := now.Sub(e.lastRef.at)
		if have && elapsed < 7*24*time.Hour {
			pred := e.lastRef.phase + time.Duration(ppm*1e-6*float64(elapsed))
			est = estimate{
				offset:   pred - e.applied,
				err:      e.lastRef.err + time.Duration(5e-6*float64(elapsed)),
				holdover: true,
				stratum:  e.stratumLocked(),
			}
			ok = true
		}
	}
	if ok {
		e.current = &est
	} else {
		e.current = nil
	}
	e.updateStateLocked(est, ok)
	if ok && (e.lastPoint.IsZero() || now.Sub(e.lastPoint) >= 5*time.Second) {
		e.lastPoint = now
		e.hist.addPoint(Point{T: e.clk.Now().UnixMilli(), OffsetMs: ms(est.offset), ErrorMs: ms(est.err), State: e.state})
	}
	action, amount, why := e.decideLocked(est, ok, now)
	e.mu.Unlock()

	if action != "" {
		e.apply(amount, action, est, why)
	}
}

func (e *Engine) stratumLocked() int {
	if e.current != nil {
		return e.current.stratum
	}
	return 8
}

func (e *Engine) driftPPMLocked() (float64, bool) {
	if ppm, _, ok := e.drift.PPM(); ok {
		return ppm, true
	}
	return e.st.drift()
}

func (e *Engine) updateStateLocked(est estimate, ok bool) {
	switch {
	case !ok:
		e.state = StateNoRef
	case est.holdover:
		e.state = StateHoldover
	case absDur(est.offset) <= e.threshold() || e.smoothing:
		if e.smoothing {
			e.state = StateAdjusted
		} else {
			e.state = StateSynced
		}
	default:
		e.state = StateAdjusted
	}
}

func (e *Engine) threshold() time.Duration { return fromMs(e.cfg.Adjust.ThresholdMs) }

// decideLocked returns the correction to make now, if any.
func (e *Engine) decideLocked(est estimate, ok bool, now time.Time) (action string, amount time.Duration, why string) {
	a := e.cfg.Adjust
	forced := false
	if e.force != nil {
		if now.After(e.force.ttl) {
			e.force = nil
			e.note = "sync now: no source answered"
		} else if ok && e.anySampleAfterLocked(e.force.at) {
			forced = true
		}
	}
	if !ok {
		e.smoothing = false
		return "", 0, ""
	}
	if e.cfg.Mode == config.ModeMonitor || e.monitor {
		e.note = "monitor only"
		e.force = nil
		return "", 0, ""
	}
	if can, _ := e.clk.CanAdjust(); !can {
		e.note = "monitor only (no permission to set the clock)"
		e.force = nil
		return "", 0, ""
	}
	off := est.offset
	abs := absDur(off)
	maxStep := fromMs(a.SmoothMaxStepMs)

	if e.smoothing {
		if abs < e.threshold()/2 || abs < time.Millisecond {
			e.smoothing = false
			e.note = "smooth correction complete"
			return "", 0, ""
		}
		if abs >= fromMs(a.StepThresholdMs) {
			e.smoothing = false // something changed a lot: fall through to a step
		} else {
			return "smooth", clampDur(off, maxStep), "smoothing"
		}
	}

	if forced {
		e.force = nil
		if abs < time.Millisecond {
			e.note = "sync now: clock already correct"
			return "", 0, ""
		}
		return "step", off, "sync now"
	}
	if abs < e.threshold() {
		e.note = ""
		return "", 0, ""
	}
	if abs < est.err/2 {
		e.note = fmt.Sprintf("offset %.0f ms is within the uncertainty (±%.0f ms)", ms(off), ms(est.err))
		return "", 0, ""
	}
	first := !e.firstDone
	if !first && now.Sub(e.lastCorrAt) < time.Duration(a.MinIntervalS*float64(time.Second)) {
		return "", 0, ""
	}
	if a.MaxCorrectionS > 0 && abs > time.Duration(a.MaxCorrectionS*float64(time.Second)) && !(first && a.AllowLargeFirstStep) {
		e.note = fmt.Sprintf("refused: correction %.1f s exceeds the %.0f s limit", off.Seconds(), a.MaxCorrectionS)
		return "", 0, ""
	}
	if a.DeferDuringTX && e.transmitting() {
		e.note = "waiting for the transmission to end"
		return "", 0, ""
	}
	if first && !a.SyncOnStart && est.holdover {
		return "", 0, ""
	}
	if abs >= fromMs(a.StepThresholdMs) || !a.Smooth || maxStep <= 0 {
		return "step", off, "offset above threshold"
	}
	e.smoothing = true
	return "smooth", clampDur(off, maxStep), "offset above threshold"
}

func (e *Engine) anySampleAfterLocked(t time.Time) bool {
	for _, r := range e.latest {
		if r.recv.After(t) {
			return true
		}
	}
	return false
}

func (e *Engine) transmitting() bool {
	for _, s := range e.srcs {
		if tx, ok := s.(sources.TxReporter); ok && tx.Transmitting() {
			return true
		}
	}
	return false
}

// apply steps the clock and keeps every stored measurement consistent.
func (e *Engine) apply(d time.Duration, method string, est estimate, why string) {
	err := e.clk.Step(d)
	now := time.Now()
	c := Correction{At: e.clk.Now().UTC(), AmountMs: ms(d), Method: method, Sources: est.used, ErrorMs: ms(est.err), Result: "ok"}
	if est.holdover {
		c.Sources = []string{"holdover"}
	}
	if err != nil {
		c.Result = err.Error()
		e.mu.Lock()
		e.smoothing = false
		e.lastCorrAt = now // back off before retrying
		e.firstDone = true
		e.mu.Unlock()
		e.alert("error", fmt.Sprintf("could not adjust the clock by %+.1f ms: %v", ms(d), err))
		e.hist.addCorrection(c)
		e.writeCSV(c)
		return
	}
	e.mu.Lock()
	e.applied += d
	for id, r := range e.latest {
		r.Offset -= d
		e.latest[id] = r
	}
	if e.current != nil {
		e.current.offset -= d
	}
	e.firstDone = true
	e.lastCorrAt = now
	if method != "smooth" {
		e.note = fmt.Sprintf("stepped %+.1f ms (%s)", ms(d), why)
	} else {
		e.note = "smoothing"
	}
	e.mu.Unlock()
	for _, s := range e.srcs {
		s.ClockStepped(d)
	}
	e.st.recordSync(c.At, c.AmountMs)
	e.hist.addCorrection(c)
	e.writeCSV(c)
	if method != "smooth" || absDur(d) >= 5*time.Millisecond {
		e.log.Info("clock corrected", "amount_ms", round1(ms(d)), "method", method, "sources", strings.Join(c.Sources, ","), "reason", why)
	}
}

func (e *Engine) writeCSV(c Correction) {
	if e.csv == "" {
		return
	}
	newFile := false
	if _, err := os.Stat(e.csv); err != nil {
		newFile = true
	}
	f, err := os.OpenFile(e.csv, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if newFile {
		_ = w.Write([]string{"utc", "method", "amount_ms", "error_ms", "sources", "result"})
	}
	_ = w.Write([]string{c.At.Format(time.RFC3339Nano), c.Method, strconv.FormatFloat(c.AmountMs, 'f', 3, 64),
		strconv.FormatFloat(c.ErrorMs, 'f', 3, 64), strings.Join(c.Sources, " "), c.Result})
	w.Flush()
}

func (e *Engine) alert(level, text string) {
	e.mu.Lock()
	e.alerts = append(e.alerts, Alert{At: time.Now().UTC(), Level: level, Text: text})
	if len(e.alerts) > 50 {
		e.alerts = e.alerts[len(e.alerts)-50:]
	}
	e.mu.Unlock()
	switch level {
	case "error":
		e.log.Error(text)
	case "warn":
		e.log.Warn(text)
	default:
		e.log.Info(text)
	}
}

// SyncNow polls every source and applies the next fresh estimate immediately,
// ignoring the threshold and minimum interval.
func (e *Engine) SyncNow() {
	e.mu.Lock()
	now := time.Now()
	e.force = &forceReq{at: now, ttl: now.Add(45 * time.Second)}
	e.note = "sync now: measuring"
	e.mu.Unlock()
	for _, s := range e.srcs {
		s.Trigger()
	}
	e.log.Info("sync now requested")
}

// ManualAdjust steps the clock by d (BktTimeSync's "manual setting").
func (e *Engine) ManualAdjust(d time.Duration) error {
	if can, why := e.clk.CanAdjust(); !can {
		return fmt.Errorf("%w: %s", clock.ErrNotPermitted, why)
	}
	e.apply(d, "manual", estimate{used: []string{"manual"}}, "manual adjustment")
	return nil
}

// SetMonitor enables or disables the runtime monitor-only override.
func (e *Engine) SetMonitor(on bool) {
	e.mu.Lock()
	e.monitor = on
	e.mu.Unlock()
}

// Source returns the source with the given ID.
func (e *Engine) Source(id string) sources.Source { return e.byID[id] }

// ---- helpers ----

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func fromMs(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func clampDur(d, lim time.Duration) time.Duration {
	if d > lim {
		return lim
	}
	if d < -lim {
		return -lim
	}
	return d
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
