package sources

import (
	"math"
	"sync"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/fusion"
)

// latencyCal learns the delay between the top of the second and the moment a
// GPS time message reaches us, by comparing against the other sources while
// they are available (typically NTP). Once learned, the GPS gives accurate
// time on its own: offline, in the field.
type latencyCal struct {
	mu         sync.Mutex
	key        string
	configured time.Duration
	auto       bool
	window     []float64 // recent latency observations, ms
	learned    float64
	have       bool
	sinceSave  int
}

const (
	calWindow     = 120
	calMinSamples = 20
	calMaxRefErr  = 40 * time.Millisecond
)

func newLatencyCal(key string, configured time.Duration, auto bool) *latencyCal {
	return &latencyCal{key: key, configured: configured, auto: auto}
}

// load restores a previously learned latency.
func (c *latencyCal) load(store Store) {
	if !c.auto || store == nil {
		return
	}
	if v, ok := store.GetFloat(c.key); ok {
		c.mu.Lock()
		c.learned, c.have = v, true
		c.mu.Unlock()
	}
}

// latency returns the latency to apply and whether it is calibrated (learned or
// explicitly configured).
func (c *latencyCal) latency() (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.auto && c.have {
		return fromMs(c.learned), true
	}
	return c.configured, c.configured != 0
}

// spread returns the jitter of recent observations (ms, 1.48·MAD).
func (c *latencyCal) spread() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.window) < 5 {
		return 0
	}
	w := append([]float64(nil), c.window...)
	m := fusion.Median(w)
	return 1.4826 * fusion.MAD(c.window, m)
}

// observe records one raw measurement (message time minus arrival time).
func (c *latencyCal) observe(raw time.Duration, host Host, exclude string) {
	if !c.auto {
		return
	}
	ref, refErr, ok := host.Reference(exclude)
	if !ok || refErr > calMaxRefErr {
		return
	}
	lat := ms(ref - raw)
	if lat < -100 || lat > 1500 || math.IsNaN(lat) {
		return
	}
	c.mu.Lock()
	c.window = append(c.window, lat)
	if len(c.window) > calWindow {
		c.window = c.window[len(c.window)-calWindow:]
	}
	if len(c.window) >= calMinSamples {
		w := append([]float64(nil), c.window...)
		c.learned = fusion.Median(w)
		c.have = true
		c.sinceSave++
	}
	save := c.have && c.sinceSave >= 30
	if save {
		c.sinceSave = 0
	}
	v := c.learned
	c.mu.Unlock()
	if save {
		host.Store().SetFloat(c.key, v)
	}
}

func (c *latencyCal) details() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := map[string]any{
		"configured_latency_ms": ms(c.configured),
		"auto_calibrate":        c.auto,
		"calibration_samples":   len(c.window),
	}
	if c.have {
		d["learned_latency_ms"] = c.learned
	}
	return d
}

// rawWindow keeps recent raw offsets and returns a robust (median) estimate.
type rawWindow struct {
	vals []time.Duration
	max  int
}

func (w *rawWindow) add(v time.Duration) {
	w.vals = append(w.vals, v)
	if len(w.vals) > w.max {
		w.vals = w.vals[len(w.vals)-w.max:]
	}
}

func (w *rawWindow) shift(d time.Duration) {
	for i := range w.vals {
		w.vals[i] -= d
	}
}

func (w *rawWindow) median() (time.Duration, time.Duration) {
	if len(w.vals) == 0 {
		return 0, 0
	}
	xs := make([]float64, len(w.vals))
	for i, v := range w.vals {
		xs[i] = float64(v)
	}
	m := fusion.Median(append([]float64(nil), xs...))
	mad := fusion.MAD(xs, m)
	return time.Duration(m), time.Duration(1.4826 * mad)
}
