package engine

import (
	"sync"
	"time"
)

// Point is one entry of the offset history chart.
type Point struct {
	T        int64   `json:"t"` // unix milliseconds (system clock)
	OffsetMs float64 `json:"offset_ms"`
	ErrorMs  float64 `json:"error_ms"`
	State    string  `json:"state"`
}

// Correction records one change made to the clock.
type Correction struct {
	At       time.Time `json:"at"`
	AmountMs float64   `json:"amount_ms"`
	Method   string    `json:"method"` // step, smooth, manual
	Sources  []string  `json:"sources"`
	ErrorMs  float64   `json:"error_ms"`
	Result   string    `json:"result"` // ok or error text
}

// History keeps 24 hours of offset points and the latest corrections. It is
// shared across engine restarts (configuration changes).
type History struct {
	mu          sync.Mutex
	points      []Point
	corrections []Correction
}

const (
	historyMaxPoints      = 24 * 3600 / 5
	historyMaxCorrections = 500
)

// NewHistory creates an empty history.
func NewHistory() *History { return &History{} }

func (h *History) addPoint(p Point) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.points = append(h.points, p)
	if len(h.points) > historyMaxPoints {
		h.points = append([]Point(nil), h.points[len(h.points)-historyMaxPoints:]...)
	}
}

func (h *History) addCorrection(c Correction) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.corrections = append(h.corrections, c)
	if len(h.corrections) > historyMaxCorrections {
		h.corrections = append([]Correction(nil), h.corrections[len(h.corrections)-historyMaxCorrections:]...)
	}
}

// Since returns the points newer than t (unix ms) and all corrections after it.
func (h *History) Since(t int64) ([]Point, []Correction) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var pts []Point
	for i := len(h.points) - 1; i >= 0; i-- {
		if h.points[i].T < t {
			pts = append([]Point(nil), h.points[i+1:]...)
			break
		}
		if i == 0 {
			pts = append([]Point(nil), h.points...)
		}
	}
	var cs []Correction
	for _, c := range h.corrections {
		if c.At.UnixMilli() >= t {
			cs = append(cs, c)
		}
	}
	return pts, cs
}

// LastCorrection returns the most recent correction, if any.
func (h *History) LastCorrection() (Correction, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.corrections) == 0 {
		return Correction{}, false
	}
	return h.corrections[len(h.corrections)-1], true
}
