package sources

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/maidenhead"
)

// GPSD reads time from a gpsd daemon. With a PPS-capable receiver gpsd reports
// the exact offset of the system clock at each pulse (sub-millisecond); without
// PPS it reports TOFF (serial message arrival), which is calibrated like a
// plain NMEA receiver.
type GPSD struct {
	cfg  config.GPSDConfig
	cal  *latencyCal
	dial func(ctx context.Context) (net.Conn, error)

	mu        sync.Mutex
	state     string
	message   string
	connected bool
	mode      int
	sats      int
	pos       *Position
	lastPPS   time.Time
	lastTOFF  time.Time
	ppsWin    rawWindow
	toffWin   rawWindow
	devices   []string
	version   string
}

// NewGPSD creates the gpsd source.
func NewGPSD(cfg config.GPSDConfig) *GPSD {
	s := &GPSD{cfg: cfg, state: StateWaiting, message: "starting",
		ppsWin: rawWindow{max: 8}, toffWin: rawWindow{max: 16}, cal: newLatencyCal("gpsd.latency_ms", 0, true)}
	s.dial = func(ctx context.Context) (net.Conn, error) {
		d := net.Dialer{Timeout: 5 * time.Second}
		return d.DialContext(ctx, "tcp", cfg.Address)
	}
	return s
}

// ID implements Source.
func (s *GPSD) ID() string { return "gpsd" }

// Priority implements Source.
func (s *GPSD) Priority() int { return s.cfg.Priority }

// MaxAge implements Source.
func (s *GPSD) MaxAge() time.Duration { return 15 * time.Second }

// Trigger implements Source.
func (s *GPSD) Trigger() {}

// ClockStepped implements Source.
func (s *GPSD) ClockStepped(d time.Duration) {
	s.mu.Lock()
	s.ppsWin.shift(d)
	s.toffWin.shift(d)
	s.mu.Unlock()
}

// Status implements Source.
func (s *GPSD) Status() Status {
	s.mu.Lock()
	d := map[string]any{
		"address":   s.cfg.Address,
		"connected": s.connected,
		"mode":      s.mode,
		"sats":      s.sats,
		"devices":   s.devices,
		"version":   s.version,
		"pps":       time.Since(s.lastPPS) < 5*time.Second,
	}
	if s.pos != nil {
		d["lat"], d["lon"], d["alt"], d["grid"] = s.pos.Lat, s.pos.Lon, s.pos.Alt, s.pos.Grid
	}
	st := Status{ID: s.ID(), Kind: "gpsd", Name: "gpsd (GPS daemon)", Enabled: true,
		State: s.state, Message: s.message, Priority: s.cfg.Priority}
	s.mu.Unlock()
	for k, v := range s.cal.details() {
		d[k] = v
	}
	st.Details = d
	return st
}

// Run implements Source.
func (s *GPSD) Run(ctx context.Context, host Host) {
	s.cal.load(host.Store())
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		conn, err := s.dial(ctx)
		if err != nil {
			s.set(StateError, "cannot connect to gpsd: "+shortErr(err), false)
		} else {
			backoff = 2 * time.Second
			s.set(StateWaiting, "connected, waiting for a fix", true)
			err = s.session(ctx, conn, host)
			conn.Close()
			if ctx.Err() != nil {
				return
			}
			s.set(StateError, "gpsd connection lost: "+errString(err), false)
		}
		if !sleepCtx(ctx, backoff, nil) {
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (s *GPSD) set(state, msg string, connected bool) {
	s.mu.Lock()
	s.state, s.message, s.connected = state, msg, connected
	s.mu.Unlock()
}

type gpsdMsg struct {
	Class    string       `json:"class"`
	Mode     int          `json:"mode"`
	Time     string       `json:"time"`
	Lat      *float64     `json:"lat"`
	Lon      *float64     `json:"lon"`
	Alt      *float64     `json:"alt"`
	AltMSL   *float64     `json:"altMSL"`
	RealSec  int64        `json:"real_sec"`
	RealNsec int64        `json:"real_nsec"`
	ClkSec   int64        `json:"clock_sec"`
	ClkNsec  int64        `json:"clock_nsec"`
	USat     int          `json:"uSat"`
	Sats     []gpsdSat    `json:"satellites"`
	Release  string       `json:"release"`
	Devices  []gpsdDevice `json:"devices"`
}

type gpsdSat struct {
	Used bool `json:"used"`
}

type gpsdDevice struct {
	Path string `json:"path"`
}

func (s *GPSD) session(ctx context.Context, conn net.Conn, host Host) error {
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	if _, err := fmt.Fprint(conn, `?WATCH={"enable":true,"json":true,"pps":true};`+"\n"); err != nil {
		return err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		arrival := host.Now()
		var m gpsdMsg
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		s.handle(m, arrival, host)
	}
	return sc.Err()
}

func (s *GPSD) handle(m gpsdMsg, arrival time.Time, host Host) {
	switch m.Class {
	case "VERSION":
		s.mu.Lock()
		s.version = m.Release
		s.mu.Unlock()
	case "DEVICES":
		var paths []string
		for _, d := range m.Devices {
			paths = append(paths, d.Path)
		}
		s.mu.Lock()
		s.devices = paths
		s.mu.Unlock()
	case "SKY":
		n := m.USat
		if n == 0 {
			for _, sat := range m.Sats {
				if sat.Used {
					n++
				}
			}
		}
		s.mu.Lock()
		s.sats = n
		s.mu.Unlock()
	case "PPS":
		// real = GPS time of the pulse; clock = system clock at the pulse.
		off := time.Unix(m.RealSec, m.RealNsec).Sub(time.Unix(m.ClkSec, m.ClkNsec))
		s.mu.Lock()
		s.lastPPS = time.Now()
		s.ppsWin.add(off)
		med, jitter := s.ppsWin.median()
		s.state, s.message = StateOK, "PPS lock"
		s.mu.Unlock()
		host.Submit(Sample{Source: s.ID(), Offset: med + fromMs(s.cfg.CorrectionMs),
			Error: 500*time.Microsecond + 2*jitter, Taken: arrival, Stratum: 0, Note: "PPS"})
	case "TOFF":
		raw := time.Unix(m.RealSec, m.RealNsec).Sub(time.Unix(m.ClkSec, m.ClkNsec))
		s.mu.Lock()
		s.lastTOFF = time.Now()
		s.mu.Unlock()
		s.serialTiming(raw, arrival, host, "TOFF")
	case "TPV":
		s.mu.Lock()
		s.mode = m.Mode
		toffRecent := time.Since(s.lastTOFF) < 10*time.Second
		s.mu.Unlock()
		if m.Mode >= 2 && m.Lat != nil && m.Lon != nil {
			alt := 0.0
			if m.AltMSL != nil {
				alt = *m.AltMSL
			} else if m.Alt != nil {
				alt = *m.Alt
			}
			grid, _ := maidenhead.Locator(*m.Lat, *m.Lon, 6)
			p := Position{Lat: *m.Lat, Lon: *m.Lon, Alt: alt, Grid: grid, Source: s.ID(), At: time.Now()}
			s.mu.Lock()
			update := s.pos == nil || time.Since(s.pos.At) > 10*time.Second
			if update {
				s.pos = &p
			}
			s.mu.Unlock()
			if update {
				host.SetPosition(p)
			}
		}
		if m.Mode < 2 {
			s.mu.Lock()
			if time.Since(s.lastPPS) > 5*time.Second {
				s.state, s.message = StateWaiting, "no fix"
			}
			s.mu.Unlock()
			return
		}
		if toffRecent || m.Time == "" {
			return
		}
		t, err := time.Parse(time.RFC3339Nano, m.Time)
		if err != nil {
			return
		}
		s.serialTiming(t.Sub(arrival), arrival, host, "TPV")
	}
}

// serialTiming handles message-arrival based timing (no PPS).
func (s *GPSD) serialTiming(raw time.Duration, arrival time.Time, host Host, kind string) {
	s.mu.Lock()
	if time.Since(s.lastPPS) < 5*time.Second {
		s.mu.Unlock()
		return // PPS is far better; ignore message timing
	}
	s.toffWin.add(raw)
	med, jitter := s.toffWin.median()
	n := len(s.toffWin.vals)
	s.mu.Unlock()
	s.cal.observe(raw, host, s.ID())
	if n < 5 {
		return
	}
	lat, calibrated := s.cal.latency()
	errb := 150 * time.Millisecond
	note := kind + ", uncalibrated latency"
	if calibrated {
		errb = 3*time.Millisecond + 2*jitter + fromMs(s.cal.spread())
		note = fmt.Sprintf("%s, latency %.0f ms", kind, ms(lat))
	}
	host.Submit(Sample{Source: s.ID(), Offset: med + lat + fromMs(s.cfg.CorrectionMs),
		Error: errb, Taken: arrival, Stratum: 0, Note: note})
	s.mu.Lock()
	s.state, s.message = StateOK, note
	s.mu.Unlock()
}
