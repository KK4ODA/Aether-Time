package engine

import (
	"time"

	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/sources"
)

// Snapshot is the complete engine state for the dashboard and API.
type Snapshot struct {
	SystemTime   time.Time          `json:"system_time"`
	State        string             `json:"state"`
	Note         string             `json:"note"`
	Mode         string             `json:"mode"`
	Monitor      bool               `json:"monitor"`
	CanAdjust    bool               `json:"can_adjust"`
	AdjustWhy    string             `json:"adjust_why,omitempty"`
	OffsetMs     *float64           `json:"offset_ms,omitempty"`
	ErrorMs      *float64           `json:"error_ms,omitempty"`
	Used         []string           `json:"used"`
	Rejected     []string           `json:"rejected"`
	Holdover     bool               `json:"holdover"`
	Smoothing    bool               `json:"smoothing"`
	Transmitting bool               `json:"transmitting"`
	DriftPPM     *float64           `json:"drift_ppm,omitempty"`
	DriftSource  string             `json:"drift_source,omitempty"`
	LastCorr     *Correction        `json:"last_correction,omitempty"`
	LastSync     *time.Time         `json:"last_sync,omitempty"`
	Position     *sources.Position  `json:"position,omitempty"`
	Sources      []SourceView       `json:"sources"`
	Alerts       []Alert            `json:"alerts"`
	NTPServer    NTPServerView      `json:"ntp_server"`
	Learned      map[string]float64 `json:"learned"`
	UptimeS      float64            `json:"uptime_s"`
}

// SourceView is a source's status plus the engine's view of its latest sample.
type SourceView struct {
	sources.Status
	OffsetMs *float64 `json:"offset_ms,omitempty"`
	ErrorMs  *float64 `json:"error_ms,omitempty"`
	AgeS     *float64 `json:"age_s,omitempty"`
	Fresh    bool     `json:"fresh"`
	Used     bool     `json:"used"`
	Rejected bool     `json:"rejected"`
	Note     string   `json:"note,omitempty"`
}

// NTPServerView describes the built-in NTP server.
type NTPServerView struct {
	Enabled bool   `json:"enabled"`
	Listen  string `json:"listen"`
	Served  uint64 `json:"served"`
	Dropped uint64 `json:"dropped"`
	Error   string `json:"error,omitempty"`
}

// Snapshot returns the current state.
func (e *Engine) Snapshot() Snapshot {
	statuses := make([]sources.Status, len(e.srcs))
	for i, s := range e.srcs {
		statuses[i] = s.Status()
	}
	tx := e.transmitting()
	can, why := e.clk.CanAdjust()

	e.mu.Lock()
	defer e.mu.Unlock()
	s := Snapshot{
		SystemTime:   e.clk.Now().UTC(),
		State:        e.state,
		Note:         e.note,
		Mode:         e.cfg.Mode,
		Monitor:      e.monitor || e.cfg.Mode == config.ModeMonitor,
		CanAdjust:    can,
		AdjustWhy:    why,
		Smoothing:    e.smoothing,
		Transmitting: tx,
		Alerts:       append([]Alert(nil), e.alerts...),
		Learned:      e.st.Learned(),
		UptimeS:      time.Since(e.started).Seconds(),
		Used:         []string{},
		Rejected:     []string{},
	}
	if e.current != nil {
		o, er := ms(e.current.offset), ms(e.current.err)
		s.OffsetMs, s.ErrorMs = &o, &er
		s.Used = append(s.Used, e.current.used...)
		s.Rejected = append(s.Rejected, e.current.reject...)
		s.Holdover = e.current.holdover
	}
	if ppm, _, ok := e.drift.PPM(); ok {
		s.DriftPPM, s.DriftSource = &ppm, "measured"
	} else if ppm, ok := e.st.drift(); ok {
		s.DriftPPM, s.DriftSource = &ppm, "saved"
	}
	if c, ok := e.hist.LastCorrection(); ok {
		s.LastCorr = &c
	}
	if ls := e.st.LastSync(); !ls.IsZero() {
		s.LastSync = &ls
	}
	if e.pos != nil {
		p := *e.pos
		s.Position = &p
	}
	s.NTPServer = NTPServerView{Enabled: e.cfg.NTPServer.Enabled, Listen: e.cfg.NTPServer.Listen, Error: e.ntpErr}
	if e.ntpServer != nil {
		s.NTPServer.Served, s.NTPServer.Dropped = e.ntpServer.Stats()
	}
	for i, src := range e.srcs {
		v := SourceView{Status: statuses[i]}
		if r, ok := e.latest[src.ID()]; ok {
			o, er, age := ms(r.Offset), ms(r.Error), time.Since(r.recv).Seconds()
			v.OffsetMs, v.ErrorMs, v.AgeS = &o, &er, &age
			v.Fresh = time.Since(r.recv) < src.MaxAge()
			v.Note = r.Note
		}
		if e.current != nil && !e.current.holdover {
			v.Used = contains(e.current.used, src.ID())
			v.Rejected = contains(e.current.reject, src.ID())
		}
		s.Sources = append(s.Sources, v)
	}
	return s
}

// History returns the offset history since t (unix ms).
func (e *Engine) History(since int64) ([]Point, []Correction) { return e.hist.Since(since) }
