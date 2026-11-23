package engine

import (
	"math"
	"time"
)

// driftTracker estimates the frequency error of the free-running clock by a
// weighted linear fit of its phase (total corrections + current offset) over
// time. The slope is used to keep correcting during holdover, when no time
// source is available.
type driftTracker struct {
	start  time.Time
	points []phasePoint
	last   time.Time
}

type phasePoint struct {
	t, phase, w float64 // seconds since start, seconds, weight
}

const (
	driftMinSpan   = 30 * time.Minute
	driftMaxSpan   = 24 * time.Hour
	driftPointGap  = 60 * time.Second
	driftMinPoints = 10
	driftMaxPPM    = 1000
)

func newDriftTracker() *driftTracker { return &driftTracker{start: time.Now()} }

// add records the phase at real time now. Points that break the trend by more
// than half a second (someone else stepped the clock) restart the fit.
func (d *driftTracker) add(now time.Time, phase, errSec float64) {
	if !d.last.IsZero() && now.Sub(d.last) < driftPointGap {
		return
	}
	t := now.Sub(d.start).Seconds()
	if slope, icpt, _, ok := d.fit(); ok {
		if math.Abs(icpt+slope*t-phase) > 0.5 {
			d.points = nil
		}
	}
	if errSec < 1e-4 {
		errSec = 1e-4
	}
	d.points = append(d.points, phasePoint{t: t, phase: phase, w: 1 / (errSec * errSec)})
	d.last = now
	cut := t - driftMaxSpan.Seconds()
	i := 0
	for i < len(d.points) && d.points[i].t < cut {
		i++
	}
	d.points = d.points[i:]
}

// fit returns slope (s/s), intercept, slope standard error, and whether the
// fit is usable.
func (d *driftTracker) fit() (slope, icpt, se float64, ok bool) {
	n := len(d.points)
	if n < driftMinPoints {
		return 0, 0, 0, false
	}
	if d.points[n-1].t-d.points[0].t < driftMinSpan.Seconds() {
		return 0, 0, 0, false
	}
	var sw, swt, swp float64
	for _, p := range d.points {
		sw += p.w
		swt += p.w * p.t
		swp += p.w * p.phase
	}
	mt, mp := swt/sw, swp/sw
	var stt, stp float64
	for _, p := range d.points {
		stt += p.w * (p.t - mt) * (p.t - mt)
		stp += p.w * (p.t - mt) * (p.phase - mp)
	}
	if stt == 0 {
		return 0, 0, 0, false
	}
	slope = stp / stt
	icpt = mp - slope*mt
	var rss float64
	for _, p := range d.points {
		r := p.phase - (icpt + slope*p.t)
		rss += p.w * r * r
	}
	se = math.Sqrt(rss / float64(n-2) / stt)
	if math.Abs(slope)*1e6 > driftMaxPPM {
		return 0, 0, 0, false
	}
	return slope, icpt, se, true
}

// PPM returns the fitted drift in parts per million and its standard error.
func (d *driftTracker) PPM() (ppm, sePPM float64, ok bool) {
	s, _, se, ok := d.fit()
	return s * 1e6, se * 1e6, ok
}
