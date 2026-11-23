package sources

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/fusion"
)

// HTTPS derives time from the Date header of web servers. The header only has
// one-second resolution, so each poll sends a handful of requests timed to land
// on second boundaries: every reply bounds the offset to an interval, and the
// intersection of the intervals shrinks to roughly the round-trip time.
//
// This keeps working where UDP port 123 is blocked (hotels, hotspots, guest
// Wi-Fi, some corporate networks), and goes through HTTPS proxies.
type HTTPS struct {
	cfg    config.HTTPSConfig
	wake   trigger
	client *http.Client

	// probe is replaced in tests.
	probe func(ctx context.Context, host Host, url string) (sent, recv, date time.Time, err error)

	mu       sync.Mutex
	state    string
	message  string
	results  []HTTPSResult
	lastPoll time.Time
}

// HTTPSResult is shown in the dashboard.
type HTTPSResult struct {
	URL      string   `json:"url"`
	OffsetMs *float64 `json:"offset_ms,omitempty"`
	ErrorMs  *float64 `json:"error_ms,omitempty"`
	RTTMs    *float64 `json:"rtt_ms,omitempty"`
	Probes   int      `json:"probes,omitempty"`
	Used     bool     `json:"used"`
	Err      string   `json:"error,omitempty"`
}

const httpsMaxProbes = 9

// NewHTTPS creates the HTTPS Date source.
func NewHTTPS(cfg config.HTTPSConfig) *HTTPS {
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     30 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	s := &HTTPS{
		cfg:     cfg,
		wake:    newTrigger(),
		client:  &http.Client{Transport: tr, Timeout: 10 * time.Second},
		state:   StateIdle,
		message: "waiting",
	}
	s.probe = s.httpProbe
	return s
}

// ID implements Source.
func (s *HTTPS) ID() string { return "https" }

// Priority implements Source.
func (s *HTTPS) Priority() int { return s.cfg.Priority }

// MaxAge implements Source.
func (s *HTTPS) MaxAge() time.Duration {
	return time.Duration(2.5 * s.cfg.PollIntervalS * float64(time.Second))
}

// Trigger implements Source.
func (s *HTTPS) Trigger() { s.wake.fire() }

// ClockStepped implements Source.
func (s *HTTPS) ClockStepped(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.results {
		if s.results[i].OffsetMs != nil {
			v := *s.results[i].OffsetMs - ms(d)
			s.results[i].OffsetMs = &v
		}
	}
}

// Status implements Source.
func (s *HTTPS) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{ID: s.ID(), Kind: "https", Name: "Web time (HTTPS fallback)", Enabled: true,
		State: s.state, Message: s.message, Priority: s.cfg.Priority,
		Details: map[string]any{"urls": append([]HTTPSResult(nil), s.results...), "fallback_only": s.cfg.FallbackOnly}}
}

// Run implements Source.
func (s *HTTPS) Run(ctx context.Context, host Host) {
	interval := time.Duration(s.cfg.PollIntervalS * float64(time.Second))
	if s.cfg.FallbackOnly {
		// Give NTP a head start before deciding it is unavailable.
		s.setState(StateIdle, "standing by while NTP works")
		if !sleepCtx(ctx, 20*time.Second, s.wake) {
			return
		}
	}
	for {
		s.mu.Lock()
		due := s.lastPoll.IsZero() || time.Since(s.lastPoll) >= interval
		s.mu.Unlock()
		needed := !s.cfg.FallbackOnly || !host.Fresh("ntp")
		if needed && due {
			s.poll(ctx, host)
		} else if !needed {
			s.setState(StateIdle, "standing by: NTP is working")
		}
		wait := interval
		if s.cfg.FallbackOnly {
			wait = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
			if !s.cfg.FallbackOnly || !host.Fresh("ntp") {
				s.mu.Lock()
				s.lastPoll = time.Time{}
				s.mu.Unlock()
			}
		case <-time.After(wait):
		}
	}
}

func (s *HTTPS) setState(state, msg string) {
	s.mu.Lock()
	s.state, s.message = state, msg
	s.mu.Unlock()
}

func (s *HTTPS) poll(ctx context.Context, host Host) {
	s.mu.Lock()
	s.lastPoll = time.Now()
	s.state, s.message = StateWaiting, "measuring"
	s.mu.Unlock()

	results := make([]HTTPSResult, len(s.cfg.URLs))
	var ests []fusion.Estimate
	var wg sync.WaitGroup
	var emu sync.Mutex
	for i, u := range s.cfg.URLs {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			off, errb, rtt, n, err := s.Measure(ctx, host, u)
			r := HTTPSResult{URL: u, Probes: n}
			if err != nil {
				r.Err = shortErr(err)
			} else {
				o, e, t := ms(off), ms(errb), ms(rtt)
				r.OffsetMs, r.ErrorMs, r.RTTMs = &o, &e, &t
				emu.Lock()
				ests = append(ests, fusion.Estimate{ID: fmt.Sprint(i), Offset: off, Error: errb})
				emu.Unlock()
			}
			results[i] = r
		}(i, u)
	}
	wg.Wait()
	fused, ok := fusion.Combine(ests)
	for _, id := range fused.Used {
		var i int
		fmt.Sscan(id, &i)
		results[i].Used = true
	}
	s.mu.Lock()
	s.results = results
	if !ok {
		s.state, s.message = StateError, "no web server reachable"
		s.mu.Unlock()
		return
	}
	s.state, s.message = StateOK, fmt.Sprintf("%d/%d servers agree", len(fused.Used), len(ests))
	s.mu.Unlock()
	host.Submit(Sample{
		Source:  s.ID(),
		Offset:  fused.Offset + fromMs(s.cfg.CorrectionMs),
		Error:   fused.Error,
		Taken:   host.Now(),
		Stratum: 3,
		Note:    "HTTPS Date",
	})
}

// Measure runs the adaptive probing against one URL and returns the offset,
// its error bound, the last round-trip time and the number of probes used.
func (s *HTTPS) Measure(ctx context.Context, host Host, url string) (offset, errBound, rtt time.Duration, probes int, err error) {
	// Warm-up request: opens the TCP/TLS connection so later probes have a small, stable RTT.
	if _, _, _, err := s.probe(ctx, host, url); err != nil {
		return 0, 0, 0, 0, err
	}
	const inf = time.Duration(1<<62 - 1)
	lo, hi := -inf, inf
	for probes < httpsMaxProbes {
		if probes > 0 {
			// Aim the next request so the server stamps it right at a second
			// boundary according to our current best guess.
			mid := lo/2 + hi/2
			trueNow := host.Now().Add(rtt/2 + mid)
			target := trueNow.Truncate(time.Second).Add(time.Second)
			if target.Sub(trueNow) < 50*time.Millisecond {
				target = target.Add(time.Second)
			}
			if !sleepCtx(ctx, target.Sub(trueNow), nil) {
				return 0, 0, 0, probes, ctx.Err()
			}
		}
		sent, recv, date, perr := s.probe(ctx, host, url)
		if perr != nil {
			return 0, 0, 0, probes, perr
		}
		probes++
		rtt = recv.Sub(sent)
		// The server's clock read `date` (truncated to the second) at some moment
		// between sent and recv: offset ∈ [date - recv, date + 1s - sent].
		l := date.Sub(recv)
		h := date.Add(time.Second).Sub(sent)
		if l > lo {
			lo = l
		}
		if h < hi {
			hi = h
		}
		if lo > hi {
			return 0, 0, 0, probes, errors.New("inconsistent Date headers (caching proxy?)")
		}
		if hi-lo <= rtt+4*time.Millisecond && probes >= 3 {
			break
		}
	}
	// 2 ms allowance for server-side timestamping jitter.
	return lo/2 + hi/2, (hi-lo)/2 + 2*time.Millisecond, rtt, probes, nil
}

func (s *HTTPS) httpProbe(ctx context.Context, host Host, url string) (sent, recv, date time.Time, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "AetherTime (time sync)")
	start := time.Now() // real monotonic reference for the interval
	sent = host.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		return
	}
	recv = sent.Add(time.Since(start)) // monotonic interval, immune to clock steps
	resp.Body.Close()
	h := resp.Header.Get("Date")
	if h == "" {
		err = errors.New("server sent no Date header")
		return
	}
	date, err = http.ParseTime(h)
	return
}
