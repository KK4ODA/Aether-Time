package sources

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/fusion"
	"github.com/KK4ODA/Aether-Time/internal/wsjtx"
)

// WSJTX estimates the clock offset from the DT values of FT8/FT4 decodes.
//
// Every station on the band starts transmitting on UTC period boundaries, so
// the median DT of many decodes tells how far our clock is from the band's
// consensus: DT = bias - offset. The bias (audio-chain latency plus the habits
// of other stations) is learned automatically while NTP or GPS is available,
// so the offline estimate tracks true UTC rather than just "the band".
//
// Decodes come from the WSJT-X UDP protocol (WSJT-X, JTDX, MSHV) and/or by
// tailing ALL.TXT. All UDP traffic can be relayed to other programs (JTAlert,
// GridTracker, loggers) in both directions.
type WSJTX struct {
	cfg config.WSJTXConfig

	mu        sync.Mutex
	state     string
	message   string
	decodes   []dtEntry
	recent    []RecentDecode
	instances map[string]*instance
	conn      *net.UDPConn
	lastAddr  *net.UDPAddr
	relays    []*relay
	dirty     bool
	bias      float64 // ms
	biasN     int
	biasSrc   string
	lastLearn time.Time
	lastEst   *dtEstimate
	alltxt    string
	host      Host
}

type dtEntry struct {
	at   time.Time
	dt   float64 // seconds
	snr  int
	mode string
}

// RecentDecode is shown in the dashboard.
type RecentDecode struct {
	At      string  `json:"at"`
	Mode    string  `json:"mode"`
	SNR     int     `json:"snr"`
	DT      float64 `json:"dt"`
	Message string  `json:"message"`
	Source  string  `json:"source"`
}

type instance struct {
	ID           string    `json:"id"`
	Version      string    `json:"version,omitempty"`
	Mode         string    `json:"mode,omitempty"`
	DialHz       uint64    `json:"dial_hz,omitempty"`
	Transmitting bool      `json:"transmitting"`
	Decoding     bool      `json:"decoding"`
	DECall       string    `json:"de_call,omitempty"`
	DEGrid       string    `json:"de_grid,omitempty"`
	LastSeen     time.Time `json:"last_seen"`
	addr         *net.UDPAddr
	gridSent     string
	gridAt       time.Time
}

type relay struct {
	target *net.UDPAddr
	conn   *net.UDPConn
}

type dtEstimate struct {
	MedianDT float64 `json:"median_dt"`
	SpreadDT float64 `json:"spread_dt"`
	N        int     `json:"n"`
	OffsetMs float64 `json:"offset_ms"`
	ErrorMs  float64 `json:"error_ms"`
}

// NewWSJTX creates the decode-timing source.
func NewWSJTX(cfg config.WSJTXConfig) *WSJTX {
	return &WSJTX{cfg: cfg, state: StateWaiting, message: "waiting for decodes",
		instances: map[string]*instance{}, bias: cfg.DTBiasMs, biasSrc: "configured"}
}

// ID implements Source.
func (w *WSJTX) ID() string { return "wsjtx" }

// Priority implements Source.
func (w *WSJTX) Priority() int { return w.cfg.Priority }

// MaxAge implements Source.
func (w *WSJTX) MaxAge() time.Duration {
	return time.Duration(w.cfg.WindowS * float64(time.Second))
}

// Trigger implements Source.
func (w *WSJTX) Trigger() {}

// ClockStepped implements Source. DT = bias - offset, so when the clock moves
// by d every stored DT moves by +d.
func (w *WSJTX) ClockStepped(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.decodes {
		w.decodes[i].dt += d.Seconds()
	}
	w.dirty = true
}

// Transmitting implements TxReporter.
func (w *WSJTX) Transmitting() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, in := range w.instances {
		if in.Transmitting && time.Since(in.LastSeen) < 30*time.Second {
			return true
		}
	}
	return false
}

// Status implements Source.
func (w *WSJTX) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	var inst []instance
	for _, in := range w.instances {
		inst = append(inst, *in)
	}
	sort.Slice(inst, func(i, j int) bool { return inst[i].ID < inst[j].ID })
	hist := make([]int, 21) // -1.0 .. +1.0 s in 0.1 s bins (ends catch outliers)
	cutoff := time.Now().Add(-w.MaxAge())
	for _, d := range w.decodes {
		if d.at.Before(cutoff) {
			continue
		}
		b := int(math.Round(d.dt*10)) + 10
		if b < 0 {
			b = 0
		}
		if b > 20 {
			b = 20
		}
		hist[b]++
	}
	details := map[string]any{
		"listen":      w.cfg.Listen,
		"forward":     w.cfg.Forward,
		"all_txt":     w.alltxt,
		"instances":   inst,
		"recent":      append([]RecentDecode(nil), w.recent...),
		"histogram":   hist,
		"bias_ms":     w.bias,
		"bias_source": w.biasSrc,
		"window_s":    w.cfg.WindowS,
		"min_decodes": w.cfg.MinDecodes,
	}
	if w.lastEst != nil {
		details["estimate"] = *w.lastEst
	}
	return Status{ID: w.ID(), Kind: "wsjtx", Name: "FT8/FT4 decodes (WSJT-X, JTDX, MSHV)", Enabled: true,
		State: w.state, Message: w.message, Priority: w.cfg.Priority, Details: details}
}

// Run implements Source.
func (w *WSJTX) Run(ctx context.Context, host Host) {
	w.mu.Lock()
	w.host = host
	if w.cfg.LearnBias {
		if v, ok := host.Store().GetFloat("wsjtx.dt_bias_ms"); ok {
			w.bias, w.biasSrc, w.biasN = v, "learned", 10
		}
	}
	w.mu.Unlock()

	if w.cfg.Listen != "" {
		if err := w.listen(ctx, host); err != nil {
			w.setState(StateError, "cannot listen on "+w.cfg.Listen+": "+err.Error()+
				" (another program may own this port: set WSJT-X to a multicast address, or use the relay)")
		}
	}
	if w.cfg.AllTxtPath != "" {
		w.mu.Lock()
		w.alltxt = w.cfg.AllTxtPath
		w.mu.Unlock()
		go w.tail(ctx, host, w.cfg.AllTxtPath)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			w.mu.Lock()
			if w.conn != nil {
				w.conn.Close()
			}
			for _, r := range w.relays {
				r.conn.Close()
			}
			w.mu.Unlock()
			return
		case <-tick.C:
			w.evaluate(host)
			w.autoGrid(host)
		}
	}
}

func (w *WSJTX) setState(state, msg string) {
	w.mu.Lock()
	w.state, w.message = state, msg
	w.mu.Unlock()
}

func (w *WSJTX) listen(ctx context.Context, host Host) error {
	addr, err := net.ResolveUDPAddr("udp", w.cfg.Listen)
	if err != nil {
		return err
	}
	var conn *net.UDPConn
	if addr.IP != nil && addr.IP.IsMulticast() {
		conn, err = net.ListenMulticastUDP("udp", nil, addr)
	} else {
		conn, err = net.ListenUDP("udp", addr)
	}
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.conn = conn
	w.mu.Unlock()
	for _, f := range w.cfg.Forward {
		ta, err := net.ResolveUDPAddr("udp", f)
		if err != nil {
			host.Logger().Warn("wsjtx relay: bad address", "addr", f, "err", err)
			continue
		}
		rc, err := net.DialUDP("udp", nil, ta)
		if err != nil {
			host.Logger().Warn("wsjtx relay: cannot open", "addr", f, "err", err)
			continue
		}
		r := &relay{target: ta, conn: rc}
		w.mu.Lock()
		w.relays = append(w.relays, r)
		w.mu.Unlock()
		go w.relayBack(ctx, r)
	}
	go w.readLoop(ctx, conn, host)
	return nil
}

func (w *WSJTX) readLoop(ctx context.Context, conn *net.UDPConn, host Host) {
	buf := make([]byte, 64*1024)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		pkt := append([]byte(nil), buf[:n]...)
		w.mu.Lock()
		relays := append([]*relay(nil), w.relays...)
		w.mu.Unlock()
		for _, r := range relays {
			_, _ = r.conn.Write(pkt)
		}
		w.handlePacket(pkt, from, host)
	}
}

// relayBack forwards replies from a relayed program (e.g. JTAlert's "reply to
// CQ") back to the WSJT-X instance they address.
func (w *WSJTX) relayBack(ctx context.Context, r *relay) {
	buf := make([]byte, 64*1024)
	for {
		n, err := r.conn.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// ICMP "port unreachable" while the other program is not running.
			time.Sleep(200 * time.Millisecond)
			continue
		}
		pkt := buf[:n]
		w.mu.Lock()
		dest := w.lastAddr
		if h, _, err := wsjtx.Parse(pkt); err == nil || h.ID != "" {
			if in := w.instances[h.ID]; in != nil && in.addr != nil {
				dest = in.addr
			}
		}
		conn := w.conn
		w.mu.Unlock()
		if dest != nil && conn != nil {
			_, _ = conn.WriteToUDP(pkt, dest)
		}
	}
}

func (w *WSJTX) handlePacket(pkt []byte, from *net.UDPAddr, host Host) {
	h, msg, err := wsjtx.Parse(pkt)
	if err != nil {
		return
	}
	now := host.Now()
	w.mu.Lock()
	in := w.instances[h.ID]
	if in == nil {
		in = &instance{ID: h.ID}
		w.instances[h.ID] = in
	}
	in.LastSeen = time.Now()
	in.addr = from
	w.lastAddr = from
	switch m := msg.(type) {
	case *wsjtx.Heartbeat:
		in.Version = m.Version
	case *wsjtx.Status:
		in.Mode, in.DialHz, in.Transmitting, in.Decoding = m.Mode, m.DialFrequency, m.Transmitting, m.Decoding
		in.DECall, in.DEGrid = m.DECall, m.DEGrid
	case *wsjtx.Decode:
		mode := wsjtx.ModeName(m.Mode)
		if (mode == m.Mode || mode == "") && in.Mode != "" {
			mode = in.Mode
		}
		if m.New && !m.OffAir && !m.LowConfidence {
			w.addDecodeLocked(now, m.DeltaTime, int(m.SNR), mode, m.Message, h.ID)
		}
	}
	if h.Type == wsjtx.TypeClose {
		delete(w.instances, h.ID)
	}
	w.mu.Unlock()
}

func (w *WSJTX) addDecodeLocked(at time.Time, dt float64, snr int, mode, text, src string) {
	w.decodes = append(w.decodes, dtEntry{at: time.Now(), dt: dt, snr: snr, mode: mode})
	if len(w.decodes) > 5000 {
		w.decodes = w.decodes[len(w.decodes)-5000:]
	}
	w.recent = append([]RecentDecode{{At: at.UTC().Format("15:04:05"), Mode: mode, SNR: snr, DT: dt,
		Message: text, Source: src}}, w.recent...)
	if len(w.recent) > 25 {
		w.recent = w.recent[:25]
	}
	w.dirty = true
}

func (w *WSJTX) modeWanted(mode string) bool {
	if len(w.cfg.Modes) == 0 {
		return true
	}
	for _, m := range w.cfg.Modes {
		if strings.EqualFold(m, mode) {
			return true
		}
	}
	return false
}

// evaluate recomputes the estimate when new decodes have arrived.
func (w *WSJTX) evaluate(host Host) {
	w.mu.Lock()
	cutoff := time.Now().Add(-w.MaxAge())
	keep := w.decodes[:0]
	for _, d := range w.decodes {
		if !d.at.Before(cutoff) {
			keep = append(keep, d)
		}
	}
	w.decodes = keep
	if !w.dirty {
		if len(w.decodes) == 0 && w.state == StateOK {
			w.state, w.message = StateWaiting, "no recent decodes"
		}
		w.mu.Unlock()
		return
	}
	w.dirty = false
	var dts []float64
	for _, d := range w.decodes {
		if d.snr >= w.cfg.MinSNR && math.Abs(d.dt) <= 2.5 && w.modeWanted(d.mode) {
			dts = append(dts, d.dt)
		}
	}
	if len(dts) < w.cfg.MinDecodes {
		w.state, w.message = StateWaiting, fmt.Sprintf("%d/%d decodes collected", len(dts), w.cfg.MinDecodes)
		w.mu.Unlock()
		return
	}
	med := fusion.Median(append([]float64(nil), dts...))
	spread := 1.4826 * fusion.MAD(dts, med)
	bias := w.bias / 1000
	offset := -(med - bias)
	biasUnc := 0.100
	if w.biasSrc == "learned" {
		biasUnc = 0.015
	}
	stat := 1.25 * spread / math.Sqrt(float64(len(dts)))
	errS := 0.025 + stat + biasUnc
	w.lastEst = &dtEstimate{MedianDT: med, SpreadDT: spread, N: len(dts), OffsetMs: offset * 1000, ErrorMs: errS * 1000}
	w.state = StateOK
	w.message = fmt.Sprintf("median DT %+.2f s from %d decodes", med, len(dts))
	learn := w.cfg.LearnBias && len(dts) >= 2*w.cfg.MinDecodes && time.Since(w.lastLearn) > 15*time.Second
	w.mu.Unlock()

	host.Submit(Sample{
		Source:  w.ID(),
		Offset:  time.Duration(offset * float64(time.Second)),
		Error:   time.Duration(errS * float64(time.Second)),
		Taken:   host.Now(),
		Stratum: 4,
		Note:    fmt.Sprintf("%d decodes", len(dts)),
	})
	if learn {
		w.learnBias(host, med)
	}
}

// learnBias updates the DT bias while a better reference is available.
func (w *WSJTX) learnBias(host Host, medianDT float64) {
	ref, refErr, ok := host.Reference(w.ID())
	if !ok || refErr > 40*time.Millisecond {
		return
	}
	obs := (medianDT + ref.Seconds()) * 1000 // DT = bias - offset  =>  bias = DT + offset
	if math.Abs(obs) > 1500 {
		return
	}
	w.mu.Lock()
	w.lastLearn = time.Now()
	switch w.biasSrc {
	case "learned":
		// Slowly track changes (different band conditions, audio settings).
		w.biasN++
		w.bias += (obs - w.bias) * 0.05
	default:
		if w.biasSrc != "learning" {
			w.biasSrc, w.biasN, w.bias = "learning", 0, 0
		}
		w.biasN++
		w.bias += (obs - w.bias) / float64(w.biasN) // running mean
		if w.biasN >= 8 {
			w.biasSrc = "learned"
		}
	}
	save := w.biasSrc == "learned" && w.biasN%8 == 0
	v := w.bias
	w.mu.Unlock()
	if save {
		host.Store().SetFloat("wsjtx.dt_bias_ms", v)
	}
}

// autoGrid sends the GPS locator to each WSJT-X instance (BktTimeSync's "AutoGrid").
func (w *WSJTX) autoGrid(host Host) {
	if !w.cfg.AutoGrid {
		return
	}
	pos, ok := host.Position()
	if !ok || pos.Grid == "" {
		return
	}
	w.mu.Lock()
	conn := w.conn
	type target struct {
		id   string
		addr *net.UDPAddr
	}
	var targets []target
	for _, in := range w.instances {
		if in.addr == nil || time.Since(in.LastSeen) > time.Minute {
			continue
		}
		if in.gridSent != pos.Grid || time.Since(in.gridAt) > 10*time.Minute {
			in.gridSent, in.gridAt = pos.Grid, time.Now()
			targets = append(targets, target{in.ID, in.addr})
		}
	}
	w.mu.Unlock()
	if conn == nil {
		return
	}
	for _, t := range targets {
		if _, err := conn.WriteToUDP(wsjtx.EncodeLocation(t.id, pos.Grid), t.addr); err == nil {
			host.Logger().Info("AutoGrid: sent locator", "instance", t.id, "grid", pos.Grid)
		}
	}
}

// SendGrid immediately sends a locator to all known instances.
func (w *WSJTX) SendGrid(grid string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conn == nil {
		return 0
	}
	n := 0
	for _, in := range w.instances {
		if in.addr == nil {
			continue
		}
		if _, err := w.conn.WriteToUDP(wsjtx.EncodeLocation(in.ID, grid), in.addr); err == nil {
			in.gridSent, in.gridAt = grid, time.Now()
			n++
		}
	}
	return n
}

// tail follows an ALL.TXT file, handling truncation and rotation.
func (w *WSJTX) tail(ctx context.Context, host Host, path string) {
	var off int64 = -1
	var partial string
	for {
		if !sleepCtx(ctx, time.Second, nil) {
			return
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		if off < 0 || fi.Size() < off {
			if off < 0 {
				off = fi.Size() // only new lines
			} else {
				off = 0 // truncated or rotated
			}
			partial = ""
		}
		if fi.Size() == off {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			f.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, 1<<20))
		f.Close()
		if err != nil {
			continue
		}
		off += int64(len(data))
		text := partial + string(data)
		lines := strings.Split(text, "\n")
		partial = lines[len(lines)-1]
		now := host.Now()
		w.mu.Lock()
		for _, l := range lines[:len(lines)-1] {
			if d, ok := ParseAllTxtLine(l); ok {
				w.addDecodeLocked(now, d.DeltaTime, int(d.SNR), d.Mode, d.Message, "ALL.TXT")
			}
		}
		w.mu.Unlock()
	}
}

var allTxtStamp = regexp.MustCompile(`^\d{6,8}_\d{6}(\.\d+)?$`)

// ParseAllTxtLine parses a received-decode line from WSJT-X or JTDX ALL.TXT.
//
//	WSJT-X: 231123_185915    14.074 Rx FT8    -12  0.2 1234 CQ K1ABC FN42
//	JTDX:   20231123_185915  -12  0.2 1234 ~  CQ K1ABC FN42
func ParseAllTxtLine(line string) (wsjtx.Decode, bool) {
	f := strings.Fields(line)
	if len(f) < 5 || !allTxtStamp.MatchString(f[0]) {
		return wsjtx.Decode{}, false
	}
	var snrS, dtS, mode string
	var rest []string
	switch {
	case len(f) >= 7 && (f[2] == "Rx" || f[2] == "Tx"):
		if f[2] == "Tx" {
			return wsjtx.Decode{}, false
		}
		mode, snrS, dtS = f[3], f[4], f[5]
		if len(f) > 7 {
			rest = f[7:]
		}
	default:
		snrS, dtS = f[1], f[2]
		mode = "FT8"
		if len(f) > 4 {
			if n := wsjtx.ModeName(f[4]); n != f[4] {
				mode = n
				rest = f[5:]
			} else {
				rest = f[4:]
			}
		}
	}
	snr, err1 := strconv.Atoi(snrS)
	dt, err2 := strconv.ParseFloat(dtS, 64)
	if err1 != nil || err2 != nil || snr < -50 || snr > 60 || math.Abs(dt) > 10 {
		return wsjtx.Decode{}, false
	}
	return wsjtx.Decode{New: true, SNR: int32(snr), DeltaTime: dt, Mode: mode, Message: strings.Join(rest, " ")}, true
}

// AllTxtCandidates lists ALL.TXT files that exist for the current user.
func AllTxtCandidates() []string {
	var dirs []string
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		la := os.Getenv("LOCALAPPDATA")
		dirs = []string{filepath.Join(la, "WSJT-X"), filepath.Join(la, "JTDX")}
	case "darwin":
		dirs = []string{filepath.Join(home, "Library", "Application Support", "WSJT-X"),
			filepath.Join(home, "Library", "Application Support", "JTDX")}
	default:
		dirs = []string{filepath.Join(home, ".local", "share", "WSJT-X"), filepath.Join(home, ".local", "share", "JTDX")}
	}
	var out []string
	for _, d := range dirs {
		p := filepath.Join(d, "ALL.TXT")
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}
