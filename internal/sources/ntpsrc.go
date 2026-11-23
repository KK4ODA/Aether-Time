// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/fusion"
	"github.com/KK4ODA/Aether-Time/internal/ntp"
)

// NTP polls a list of NTP servers, keeps the best (lowest-delay) reply from
// each burst, and combines the servers while rejecting falsetickers.
type NTP struct {
	cfg  config.NTPConfig
	wake trigger

	mu        sync.Mutex
	servers   []NTPServerStatus
	state     string
	message   string
	lastPoll  time.Time
	lastTrig  time.Time
	nextPoll  time.Time
	lastFused *Sample
}

// NTPServerStatus is shown in the dashboard.
type NTPServerStatus struct {
	Name     string   `json:"name"`
	Address  string   `json:"address"`
	OffsetMs *float64 `json:"offset_ms,omitempty"`
	DelayMs  *float64 `json:"delay_ms,omitempty"`
	ErrorMs  *float64 `json:"error_ms,omitempty"`
	Stratum  int      `json:"stratum,omitempty"`
	RefID    string   `json:"refid,omitempty"`
	Used     bool     `json:"used"`
	Err      string   `json:"error,omitempty"`
	At       string   `json:"at,omitempty"`
}

// NewNTP creates the NTP source.
func NewNTP(cfg config.NTPConfig) *NTP {
	s := &NTP{cfg: cfg, wake: newTrigger(), state: StateWaiting, message: "starting"}
	for _, name := range cfg.Servers {
		s.servers = append(s.servers, NTPServerStatus{Name: name})
	}
	return s
}

// ID implements Source.
func (s *NTP) ID() string { return "ntp" }

// Priority implements Source.
func (s *NTP) Priority() int { return s.cfg.Priority }

// MaxAge implements Source.
func (s *NTP) MaxAge() time.Duration {
	return time.Duration(2.5 * s.cfg.PollIntervalS * float64(time.Second))
}

// Trigger implements Source.
func (s *NTP) Trigger() { s.wake.fire() }

// ClockStepped implements Source.
func (s *NTP) ClockStepped(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.servers {
		if s.servers[i].OffsetMs != nil {
			v := *s.servers[i].OffsetMs - ms(d)
			s.servers[i].OffsetMs = &v
		}
	}
}

// Status implements Source.
func (s *NTP) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv := append([]NTPServerStatus(nil), s.servers...)
	details := map[string]any{"servers": srv}
	if !s.nextPoll.IsZero() {
		details["next_poll"] = s.nextPoll.UTC().Format(time.RFC3339)
	}
	return Status{ID: s.ID(), Kind: "ntp", Name: "Internet time (NTP)", Enabled: true,
		State: s.state, Message: s.message, Priority: s.cfg.Priority, Details: details}
}

// Run implements Source.
func (s *NTP) Run(ctx context.Context, host Host) {
	interval := time.Duration(s.cfg.PollIntervalS * float64(time.Second))
	retry := 30 * time.Second
	for {
		ok := s.poll(ctx, host)
		wait := interval
		if !ok {
			// Retry sooner while offline, backing off to the normal interval.
			wait = retry
			retry *= 2
			if retry > interval {
				retry = interval
			}
		} else {
			retry = 30 * time.Second
		}
		s.mu.Lock()
		s.nextPoll = time.Now().Add(wait)
		s.mu.Unlock()
		if !sleepCtx(ctx, wait, s.wake) {
			return
		}
		// Rate-limit manual triggers to protect public servers.
		s.mu.Lock()
		since := time.Since(s.lastPoll)
		s.mu.Unlock()
		if since < 15*time.Second {
			if !sleepCtx(ctx, 15*time.Second-since, nil) {
				return
			}
		}
	}
}

func (s *NTP) poll(ctx context.Context, host Host) bool {
	s.mu.Lock()
	s.lastPoll = time.Now()
	s.mu.Unlock()
	timeout := time.Duration(s.cfg.TimeoutMs * float64(time.Millisecond))
	type result struct {
		idx  int
		best *ntp.Response
		err  error
	}
	results := make(chan result, len(s.cfg.Servers))
	for i, name := range s.cfg.Servers {
		go func(i int, name string) {
			best, err := s.burst(ctx, name, timeout, host.Now)
			results <- result{i, best, err}
		}(i, name)
	}
	var ests []fusion.Estimate
	resp := make([]*ntp.Response, len(s.cfg.Servers))
	errs := make([]error, len(s.cfg.Servers))
	for range s.cfg.Servers {
		r := <-results
		resp[r.idx], errs[r.idx] = r.best, r.err
		if r.best != nil {
			ests = append(ests, fusion.Estimate{ID: fmt.Sprint(r.idx), Offset: r.best.Offset, Error: r.best.ErrorBound()})
		}
	}
	fused, ok := fusion.Combine(ests)
	used := map[string]bool{}
	for _, id := range fused.Used {
		used[id] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	minStratum := 16
	var newest time.Time
	for i := range s.servers {
		st := NTPServerStatus{Name: s.cfg.Servers[i]}
		if r := resp[i]; r != nil {
			off, del, eb := ms(r.Offset), ms(r.Delay), ms(r.ErrorBound())
			st.Address, st.OffsetMs, st.DelayMs, st.ErrorMs = r.Address, &off, &del, &eb
			st.Stratum, st.RefID = int(r.Stratum), r.RefID
			st.Used = used[fmt.Sprint(i)]
			st.At = r.LocalTime.UTC().Format(time.RFC3339)
			if st.Used && int(r.Stratum) < minStratum {
				minStratum = int(r.Stratum)
			}
			if r.LocalTime.After(newest) {
				newest = r.LocalTime
			}
		} else if errs[i] != nil {
			st.Err = shortErr(errs[i])
		}
		s.servers[i] = st
	}
	if !ok {
		s.state = StateError
		s.message = "no NTP server reachable"
		if len(errs) > 0 && errs[0] != nil {
			s.message += ": " + shortErr(errs[0])
		}
		return false
	}
	smp := Sample{
		Source:  s.ID(),
		Offset:  fused.Offset + fromMs(s.cfg.CorrectionMs),
		Error:   fused.Error,
		Taken:   newest,
		Stratum: minStratum,
		Note:    fmt.Sprintf("%d/%d servers agree", len(fused.Used), len(ests)),
	}
	s.lastFused = &smp
	s.state = StateOK
	s.message = smp.Note
	host.Submit(smp)
	return true
}

// burst sends cfg.Samples queries to one server and keeps the lowest-delay reply,
// which is the one least affected by network queuing.
func (s *NTP) burst(ctx context.Context, name string, timeout time.Duration, now func() time.Time) (*ntp.Response, error) {
	var best *ntp.Response
	var lastErr error
	n := s.cfg.Samples
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		if i > 0 && !sleepCtx(ctx, time.Second, nil) {
			break
		}
		r, err := ntp.Query(ctx, name, timeout, now)
		if err != nil {
			lastErr = err
			var kod *ntp.KissOfDeathError
			if errors.As(err, &kod) {
				break // the server asked us to back off
			}
			continue
		}
		if best == nil || r.Delay < best.Delay {
			best = r
		}
	}
	if best == nil {
		return nil, lastErr
	}
	return best, nil
}

func shortErr(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "DNS lookup failed (offline?)"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection refused (is UDP port 123 blocked on this network?)"
	}
	return err.Error()
}
