// SPDX-License-Identifier: GPL-3.0-or-later

// Package fusion combines several clock-offset estimates into one, rejecting
// "falsetickers" (sources that disagree with the majority) using an interval
// intersection in the spirit of Marzullo's algorithm, which NTP also uses.
package fusion

import (
	"math"
	"sort"
	"time"
)

// Estimate is one source's opinion of the clock offset.
type Estimate struct {
	ID     string
	Offset time.Duration // reference minus local clock
	Error  time.Duration // half-width of the confidence interval
}

// Result is the combined estimate.
type Result struct {
	Offset time.Duration
	Error  time.Duration
	Used   []string // IDs of the estimates that agreed and were combined
	Reject []string // IDs rejected as falsetickers
}

// MinError floors every estimate's error so one source can never claim perfection.
const MinError = 100 * time.Microsecond

// Combine returns the fused estimate. ok is false when there is no input.
func Combine(ests []Estimate) (Result, bool) {
	if len(ests) == 0 {
		return Result{}, false
	}
	for i := range ests {
		if ests[i].Error < MinError {
			ests[i].Error = MinError
		}
	}
	if len(ests) == 1 {
		return Result{Offset: ests[0].Offset, Error: ests[0].Error, Used: []string{ests[0].ID}}, true
	}

	// Sweep the interval endpoints to find the region covered by the most intervals.
	type edge struct {
		at   time.Duration
		kind int // +1 start, -1 end
	}
	edges := make([]edge, 0, 2*len(ests))
	for _, e := range ests {
		edges = append(edges, edge{e.Offset - e.Error, +1}, edge{e.Offset + e.Error, -1})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].at != edges[j].at {
			return edges[i].at < edges[j].at
		}
		return edges[i].kind > edges[j].kind // starts before ends: touching intervals overlap
	})
	best, cur := 0, 0
	var lo, hi time.Duration
	for i, e := range edges {
		cur += e.kind
		if cur > best {
			best = cur
			lo = e.at
			// The region extends to the next edge.
			if i+1 < len(edges) {
				hi = edges[i+1].at
			} else {
				hi = e.at
			}
		}
	}

	// Truechimers are the estimates whose interval touches the best region.
	var res Result
	var sumW, sumWO float64
	for _, e := range ests {
		if e.Offset+e.Error >= lo && e.Offset-e.Error <= hi {
			w := 1 / math.Pow(float64(e.Error), 2)
			sumW += w
			sumWO += w * float64(e.Offset)
			res.Used = append(res.Used, e.ID)
		} else {
			res.Reject = append(res.Reject, e.ID)
		}
	}
	res.Offset = time.Duration(sumWO / sumW)
	res.Error = time.Duration(1 / math.Sqrt(sumW))
	// With only a minority agreeing there is no trustworthy majority; widen the error.
	if best*2 <= len(ests) && len(ests) >= 3 {
		res.Error *= 3
	}
	return res, true
}

// Median returns the median of xs (which is reordered).
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	n := len(xs)
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}

// MAD returns the median absolute deviation of xs around m.
func MAD(xs []float64, m float64) float64 {
	d := make([]float64, len(xs))
	for i, x := range xs {
		d[i] = math.Abs(x - m)
	}
	return Median(d)
}
