package sources

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/nmea"
	"github.com/KK4ODA/Aether-Time/internal/wsjtx"
)

type memStore struct {
	mu sync.Mutex
	m  map[string]float64
}

func (s *memStore) GetFloat(k string) (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	return v, ok
}

func (s *memStore) SetFloat(k string, v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
}

// fakeHost simulates the engine. The local clock is `skew` behind real time
// (so the true offset is +skew).
type fakeHost struct {
	skew    time.Duration
	mu      sync.Mutex
	samples []Sample
	ref     *time.Duration
	pos     *Position
	store   *memStore
}

func newFakeHost(skew time.Duration) *fakeHost {
	return &fakeHost{skew: skew, store: &memStore{m: map[string]float64{}}}
}

func (h *fakeHost) Now() time.Time { return time.Now().Add(-h.skew) }
func (h *fakeHost) Submit(s Sample) {
	h.mu.Lock()
	h.samples = append(h.samples, s)
	h.mu.Unlock()
}
func (h *fakeHost) Reference(string) (time.Duration, time.Duration, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ref == nil {
		return 0, 0, false
	}
	return *h.ref, 2 * time.Millisecond, true
}
func (h *fakeHost) Fresh(string) bool { return false }
func (h *fakeHost) SetPosition(p Position) {
	h.mu.Lock()
	h.pos = &p
	h.mu.Unlock()
}
func (h *fakeHost) Position() (Position, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pos == nil {
		return Position{}, false
	}
	return *h.pos, true
}
func (h *fakeHost) Store() Store         { return h.store }
func (h *fakeHost) Logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func (h *fakeHost) last() (Sample, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.samples) == 0 {
		return Sample{}, false
	}
	return h.samples[len(h.samples)-1], true
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func TestHTTPSMeasure(t *testing.T) {
	// The web server's clock is correct; ours is 337 ms behind.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
	}))
	defer srv.Close()
	host := newFakeHost(337 * time.Millisecond)
	s := NewHTTPS(config.HTTPSConfig{URLs: []string{srv.URL}, PollIntervalS: 60})
	off, errb, _, probes, err := s.Measure(context.Background(), host, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if d := absDur(off - 337*time.Millisecond); d > errb || d > 25*time.Millisecond {
		t.Fatalf("offset %v ± %v, want 337ms", off, errb)
	}
	if errb > 30*time.Millisecond {
		t.Fatalf("error bound %v too wide after %d probes", errb, probes)
	}
	t.Logf("offset %v ± %v in %d probes", off, errb, probes)
}

func TestWSJTXDecodesAndRelay(t *testing.T) {
	// A relay target standing in for JTAlert/GridTracker.
	relayTarget, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer relayTarget.Close()

	cfg := config.Default().WSJTX
	cfg.Listen = "127.0.0.1:0"
	cfg.Forward = []string{relayTarget.LocalAddr().String()}
	cfg.MinDecodes = 5
	cfg.LearnBias = false
	w := NewWSJTX(cfg)
	host := newFakeHost(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx, host)
	var addr net.Addr
	waitFor(t, 2*time.Second, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.conn != nil {
			addr = w.conn.LocalAddr()
			return true
		}
		return false
	})

	// Our fake WSJT-X instance.
	radio, err := net.DialUDP("udp", nil, addr.(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer radio.Close()
	radio.Write(wsjtx.EncodeStatus("WSJT-X", wsjtx.Status{Mode: "FT8", Transmitting: true}))
	// Clock 0.4 s fast => DTs around +0.4 s (one outlier that must be ignored).
	for i, dt := range []float64{0.38, 0.41, 0.40, 0.43, 0.39, 0.42, 1.9} {
		radio.Write(wsjtx.EncodeDecode("WSJT-X", wsjtx.Decode{New: true, SNR: -10, DeltaTime: dt, Mode: "~",
			Message: fmt.Sprintf("CQ T%d", i)}))
	}
	waitFor(t, 3*time.Second, func() bool { _, ok := host.last(); return ok })
	s, _ := host.last()
	if d := absDur(s.Offset + 405*time.Millisecond); d > 20*time.Millisecond {
		t.Fatalf("offset %v, want about -405ms", s.Offset)
	}
	if !w.Transmitting() {
		t.Fatal("expected transmitting state from Status message")
	}

	// The relay target received the traffic and can reply back to the radio.
	buf := make([]byte, 2048)
	relayTarget.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, from, err := relayTarget.ReadFromUDP(buf)
	if err != nil || n == 0 {
		t.Fatalf("relay target got nothing: %v", err)
	}
	reply := wsjtx.EncodeHeartbeat("WSJT-X", "relay-reply")
	relayTarget.WriteToUDP(reply, from)
	radio.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err = radio.Read(buf)
	if err != nil {
		t.Fatalf("reply not relayed back: %v", err)
	}
	if h, _, _ := wsjtx.Parse(buf[:n]); h.Type != wsjtx.TypeHeartbeat {
		t.Fatalf("unexpected relayed message %+v", h)
	}

	// Stepping the clock shifts stored DTs so the estimate follows.
	w.ClockStepped(-405 * time.Millisecond)
	waitFor(t, 3*time.Second, func() bool {
		s, _ := host.last()
		return absDur(s.Offset) < 20*time.Millisecond
	})
}

func TestWSJTXLearnsBias(t *testing.T) {
	cfg := config.Default().WSJTX
	cfg.Listen = ""
	cfg.AllTxtPath = ""
	cfg.MinDecodes = 3
	w := NewWSJTX(cfg)
	host := newFakeHost(0)
	zero := time.Duration(0)
	host.ref = &zero // NTP says the clock is perfect...
	for i := 0; i < 8; i++ {
		w.mu.Lock()
		w.addDecodeLocked(time.Now(), 0.2, -5, "FT8", "CQ", "test") // ...yet the band shows DT +0.2
		w.mu.Unlock()
	}
	for i := 0; i < 9; i++ {
		w.lastLearn = time.Time{}
		w.dirty = true
		w.evaluate(host)
	}
	if w.biasSrc != "learned" || absDur(fromMs(w.bias)-200*time.Millisecond) > time.Millisecond {
		t.Fatalf("bias %v (%s)", w.bias, w.biasSrc)
	}
	w.dirty = true
	w.evaluate(host)
	s, _ := host.last()
	if absDur(s.Offset) > 5*time.Millisecond {
		t.Fatalf("with learned bias the offset should be ~0, got %v", s.Offset)
	}
	if v, ok := host.store.GetFloat("wsjtx.dt_bias_ms"); !ok || absDur(fromMs(v)-200*time.Millisecond) > time.Millisecond {
		t.Fatalf("bias not persisted: %v %v", v, ok)
	}
}

func TestParseAllTxt(t *testing.T) {
	d, ok := ParseAllTxtLine("231123_185915    14.074 Rx FT8    -12  0.2 1234 CQ K1ABC FN42")
	if !ok || d.SNR != -12 || d.DeltaTime != 0.2 || d.Mode != "FT8" || d.Message != "CQ K1ABC FN42" {
		t.Fatalf("wsjt-x line: %+v %v", d, ok)
	}
	d, ok = ParseAllTxtLine("20231123_185930  -7  -0.3 2200 +  CQ DX W1AW FN31")
	if !ok || d.SNR != -7 || d.DeltaTime != -0.3 || d.Mode != "FT4" {
		t.Fatalf("jtdx line: %+v %v", d, ok)
	}
	if _, ok := ParseAllTxtLine("231123_185930    14.074 Tx FT8      0  0.0 1500 CQ KK4ODA EM73"); ok {
		t.Fatal("Tx lines must be ignored")
	}
	if _, ok := ParseAllTxtLine("garbage line here with words"); ok {
		t.Fatal("garbage accepted")
	}
}

// pipeGPS feeds NMEA bursts with a known latency through the GPS source.
func TestGPSLatencyCalibration(t *testing.T) {
	pr, pw := io.Pipe()
	cfg := config.Default().GPS
	cfg.Transport = "tcp"
	cfg.AutoCalibrate = true
	g := NewGPS(cfg)
	g.open = func(context.Context) (io.ReadCloser, error) { return pr, nil }
	host := newFakeHost(-2 * time.Second) // local clock 2 s fast
	trueOff := -2 * time.Second
	host.ref = &trueOff // network time knows the truth
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx, host)

	const latency = 120 * time.Millisecond
	go func() {
		// Bursts every 300 ms whose timestamps advance 300 ms (fractional seconds are valid NMEA).
		start := time.Now().UTC().Truncate(time.Second).Add(time.Second)
		time.Sleep(time.Until(start))
		for i := 0; i < 40 && ctx.Err() == nil; i++ {
			epoch := start.Add(time.Duration(i) * 300 * time.Millisecond)
			time.Sleep(time.Until(epoch.Add(latency)))
			body := fmt.Sprintf("GPRMC,%s,A,3345.000,N,08423.400,W,0.0,0.0,%s,,,A",
				epoch.Format("150405.00"), epoch.Format("020106"))
			fmt.Fprintf(pw, "$%s*%s\r\n", body, nmea.Checksum(body))
		}
	}()
	waitFor(t, 15*time.Second, func() bool {
		lat, ok := g.cal.latency()
		return ok && absDur(lat-latency) < 15*time.Millisecond
	})
	waitFor(t, 5*time.Second, func() bool {
		s, ok := host.last()
		return ok && absDur(s.Offset-trueOff) < 20*time.Millisecond && s.Error < 50*time.Millisecond
	})
	if p, ok := host.Position(); !ok || p.Grid != "EM73ts" {
		t.Fatalf("position %+v", p)
	}
}
