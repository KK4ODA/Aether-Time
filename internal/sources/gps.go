package sources

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"

	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/maidenhead"
	"github.com/KK4ODA/Aether-Time/internal/nmea"
)

// GPS reads NMEA 0183 from a receiver on a serial port (USB, Bluetooth SPP,
// RS-232), from a TCP server (phone apps like GPS2IP or Share GPS, ser2net) or
// from UDP datagrams.
//
// Timing uses the arrival of the first sentence of each once-per-second burst.
// The fixed delay between the top of the second and that arrival is learned
// automatically against network time, after which the GPS is an accurate
// offline reference.
type GPS struct {
	cfg  config.GPSConfig
	wake trigger
	cal  *latencyCal

	// open is replaced in tests.
	open func(ctx context.Context) (io.ReadCloser, error)

	mu          sync.Mutex
	state       string
	message     string
	connected   bool
	fix         bool
	sats        int
	pos         *Position
	lastLine    string
	sentences   int
	validBursts int
	window      rawWindow
	lastUTC     time.Time
	lastUTCLoc  time.Time
	lastValid   time.Time
}

// NewGPS creates the NMEA GPS source.
func NewGPS(cfg config.GPSConfig) *GPS {
	g := &GPS{cfg: cfg, wake: newTrigger(), state: StateWaiting, message: "starting", window: rawWindow{max: 16},
		cal: newLatencyCal("gps.latency_ms", fromMs(cfg.LatencyMs), cfg.AutoCalibrate)}
	g.open = g.openTransport
	return g
}

// ID implements Source.
func (g *GPS) ID() string { return "gps" }

// Priority implements Source.
func (g *GPS) Priority() int { return g.cfg.Priority }

// MaxAge implements Source.
func (g *GPS) MaxAge() time.Duration { return 15 * time.Second }

// Trigger implements Source.
func (g *GPS) Trigger() {}

// ClockStepped implements Source.
func (g *GPS) ClockStepped(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.window.shift(d)
}

// Status implements Source.
func (g *GPS) Status() Status {
	g.mu.Lock()
	details := map[string]any{
		"transport": g.cfg.Transport,
		"connected": g.connected,
		"fix":       g.fix,
		"sats":      g.sats,
		"sentences": g.sentences,
		"last":      g.lastLine,
	}
	if g.cfg.Transport == "serial" {
		details["port"] = g.cfg.Port
		details["baud"] = g.cfg.Baud
	} else {
		details["address"] = g.cfg.Address
	}
	if g.pos != nil {
		details["lat"], details["lon"], details["alt"], details["grid"] = g.pos.Lat, g.pos.Lon, g.pos.Alt, g.pos.Grid
	}
	_, jitter := g.window.median()
	details["jitter_ms"] = ms(jitter)
	st := Status{ID: g.ID(), Kind: "gps", Name: "GPS receiver (NMEA)", Enabled: true,
		State: g.state, Message: g.message, Priority: g.cfg.Priority}
	g.mu.Unlock()
	for k, v := range g.cal.details() {
		details[k] = v
	}
	st.Details = details
	return st
}

// Run implements Source.
func (g *GPS) Run(ctx context.Context, host Host) {
	g.cal.load(host.Store())
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		rc, err := g.open(ctx)
		if err != nil {
			g.setState(StateError, "cannot open: "+err.Error(), false)
			if !sleepCtx(ctx, backoff, nil) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 2 * time.Second
		g.setState(StateWaiting, "connected, waiting for a fix", true)
		err = g.read(ctx, rc, host)
		rc.Close()
		if ctx.Err() != nil {
			return
		}
		g.setState(StateError, "connection lost: "+errString(err), false)
		if !sleepCtx(ctx, backoff, nil) {
			return
		}
	}
}

func errString(err error) string {
	if err == nil {
		return "closed"
	}
	return err.Error()
}

func (g *GPS) setState(state, msg string, connected bool) {
	g.mu.Lock()
	g.state, g.message, g.connected = state, msg, connected
	if !connected {
		g.fix = false
		g.validBursts = 0
	}
	g.mu.Unlock()
}

const burstGap = 250 * time.Millisecond

// read consumes the stream, assembling lines and tagging each with the arrival
// time of the burst it belongs to.
func (g *GPS) read(ctx context.Context, rc io.ReadCloser, host Host) error {
	go func() {
		<-ctx.Done()
		rc.Close()
	}()
	buf := make([]byte, 512)
	var line []byte
	var lineBurst time.Time
	var lastByte, burstStart time.Time
	burstUsed := false
	idle := time.Now()
	for {
		n, err := rc.Read(buf)
		now := host.Now()
		if n == 0 && err == nil {
			// Serial reads can time out without data.
			if time.Since(idle) > 10*time.Second {
				g.setState(StateError, "no data from receiver (check port, baud rate)", true)
				idle = time.Now()
			}
			continue
		}
		if err != nil {
			return err
		}
		idle = time.Now()
		// Estimate when the first byte of this chunk arrived on the wire.
		first := now
		if g.cfg.Transport == "serial" && g.cfg.Baud > 0 {
			first = now.Add(-time.Duration(float64(n) * 10 / float64(g.cfg.Baud) * float64(time.Second)))
		}
		if lastByte.IsZero() || first.Sub(lastByte) > burstGap {
			burstStart = first
			burstUsed = false
		}
		lastByte = now
		for _, c := range buf[:n] {
			if len(line) == 0 {
				lineBurst = burstStart
			}
			if c == '\n' || c == '\r' {
				if len(line) > 0 {
					if g.handleLine(string(line), lineBurst, !burstUsed, host) {
						burstUsed = true
					}
					line = line[:0]
				}
				continue
			}
			if len(line) < 256 {
				line = append(line, c)
			}
		}
	}
}

// handleLine processes one sentence. It returns true if the sentence was used
// as the timing reference for its burst.
func (g *GPS) handleLine(line string, burst time.Time, firstInBurst bool, host Host) bool {
	r, err := nmea.Parse(line)
	g.mu.Lock()
	g.lastLine = line
	if err == nil || errors.Is(err, nmea.ErrUnsupported) {
		g.sentences++
	}
	g.mu.Unlock()
	if err != nil {
		return false
	}
	g.mu.Lock()
	if r.Sats > 0 && (r.Type == "GGA" || g.sats == 0) {
		g.sats = r.Sats
	}
	if r.HasValidity {
		g.fix = r.Valid
		if r.Valid {
			g.lastValid = time.Now()
		}
	}
	valid := r.Valid || (!r.HasValidity && time.Since(g.lastValid) < 3*time.Second)
	g.mu.Unlock()

	if r.HasPos && valid {
		g.updatePosition(r, host)
	}
	if !r.HasTime || !firstInBurst {
		return false
	}
	if !valid {
		g.setMsg(StateWaiting, "receiver has no valid fix yet")
		return true
	}
	ref := host.Now()
	g.mu.Lock()
	if !r.HasDate && !g.lastUTC.IsZero() {
		// Continue from the last full date+time we received.
		ref = g.lastUTC.Add(time.Since(g.lastUTCLoc))
	}
	g.mu.Unlock()
	utc, ok := nmea.Resolve(r, ref, g.cfg.TimeOnly)
	if !ok {
		return false
	}
	raw := utc.Sub(burst)
	g.cal.observe(raw, host, g.ID())

	g.mu.Lock()
	if r.HasDate && !g.cfg.TimeOnly {
		g.lastUTC, g.lastUTCLoc = utc, time.Now()
	}
	g.window.add(raw)
	g.validBursts++
	settled := g.validBursts >= 5
	med, jitter := g.window.median()
	g.mu.Unlock()
	if !settled {
		g.setMsg(StateWaiting, "fix acquired, settling")
		return true
	}

	lat, calibrated := g.cal.latency()
	errb := 150 * time.Millisecond // unknown latency: typical NMEA delay is 0–300 ms
	note := "uncalibrated latency"
	if calibrated {
		errb = 3*time.Millisecond + 2*jitter + fromMs(g.cal.spread())
		note = fmt.Sprintf("latency %.0f ms", ms(lat))
	}
	host.Submit(Sample{
		Source:  g.ID(),
		Offset:  med + lat + fromMs(g.cfg.CorrectionMs),
		Error:   errb,
		Taken:   burst,
		Stratum: 0,
		Note:    note,
	})
	g.setMsg(StateOK, fmt.Sprintf("fix, %d sats, %s", g.sats, note))
	return true
}

func (g *GPS) setMsg(state, msg string) {
	g.mu.Lock()
	g.state, g.message = state, msg
	g.mu.Unlock()
}

func (g *GPS) updatePosition(r nmea.Report, host Host) {
	g.mu.Lock()
	if g.pos != nil && time.Since(g.pos.At) < 10*time.Second {
		g.mu.Unlock()
		return
	}
	alt := 0.0
	if g.pos != nil {
		alt = g.pos.Alt
	}
	if r.HasAlt {
		alt = r.Alt
	}
	grid, _ := maidenhead.Locator(r.Lat, r.Lon, 6)
	p := Position{Lat: r.Lat, Lon: r.Lon, Alt: alt, Grid: grid, Source: g.ID(), At: time.Now()}
	g.pos = &p
	g.mu.Unlock()
	host.SetPosition(p)
}

func (g *GPS) openTransport(ctx context.Context) (io.ReadCloser, error) {
	switch g.cfg.Transport {
	case "tcp":
		d := net.Dialer{Timeout: 5 * time.Second}
		return d.DialContext(ctx, "tcp", g.cfg.Address)
	case "udp":
		pc, err := net.ListenPacket("udp", g.cfg.Address)
		if err != nil {
			return nil, err
		}
		return packetReader{pc}, nil
	default:
		mode := &serial.Mode{
			BaudRate:          g.cfg.Baud,
			DataBits:          g.cfg.DataBits,
			InitialStatusBits: &serial.ModemOutputBits{RTS: g.cfg.RTS, DTR: g.cfg.DTR},
		}
		switch strings.ToLower(g.cfg.Parity) {
		case "odd":
			mode.Parity = serial.OddParity
		case "even":
			mode.Parity = serial.EvenParity
		case "mark":
			mode.Parity = serial.MarkParity
		case "space":
			mode.Parity = serial.SpaceParity
		default:
			mode.Parity = serial.NoParity
		}
		switch g.cfg.StopBits {
		case "1.5":
			mode.StopBits = serial.OnePointFiveStopBits
		case "2":
			mode.StopBits = serial.TwoStopBits
		default:
			mode.StopBits = serial.OneStopBit
		}
		p, err := serial.Open(g.cfg.Port, mode)
		if err != nil {
			return nil, err
		}
		_ = p.SetReadTimeout(2 * time.Second)
		return p, nil
	}
}

type packetReader struct{ pc net.PacketConn }

func (p packetReader) Read(b []byte) (int, error) {
	n, _, err := p.pc.ReadFrom(b)
	return n, err
}

func (p packetReader) Close() error { return p.pc.Close() }

// SerialPorts lists the serial ports present on this machine.
func SerialPorts() []string {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil
	}
	return ports
}
