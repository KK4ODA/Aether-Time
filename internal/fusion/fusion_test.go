package fusion

import (
	"testing"
	"time"
)

const ms = time.Millisecond

func TestSingle(t *testing.T) {
	r, ok := Combine([]Estimate{{ID: "a", Offset: 40 * ms, Error: 10 * ms}})
	if !ok || r.Offset != 40*ms || r.Error != 10*ms {
		t.Fatalf("%+v", r)
	}
}

func TestFalsetickerRejected(t *testing.T) {
	r, ok := Combine([]Estimate{
		{ID: "ntp", Offset: 100 * ms, Error: 5 * ms},
		{ID: "gps", Offset: 102 * ms, Error: 10 * ms},
		{ID: "bad", Offset: 900 * ms, Error: 20 * ms},
	})
	if !ok {
		t.Fatal("no result")
	}
	if len(r.Reject) != 1 || r.Reject[0] != "bad" {
		t.Fatalf("reject %v", r.Reject)
	}
	if r.Offset < 100*ms || r.Offset > 102*ms {
		t.Fatalf("offset %v", r.Offset)
	}
	if r.Error >= 5*ms {
		t.Fatalf("combined error %v should be below the best single error", r.Error)
	}
}

func TestWeighting(t *testing.T) {
	// A precise source and a vague one that agree: the result hugs the precise one.
	r, _ := Combine([]Estimate{
		{ID: "gps", Offset: 10 * ms, Error: 2 * ms},
		{ID: "ft8", Offset: 60 * ms, Error: 80 * ms},
	})
	if r.Offset < 10*ms || r.Offset > 11*ms {
		t.Fatalf("offset %v", r.Offset)
	}
	if len(r.Used) != 2 {
		t.Fatalf("used %v", r.Used)
	}
}

func TestMedianMAD(t *testing.T) {
	xs := []float64{0.1, 0.3, 0.2, 5, 0.2}
	m := Median(append([]float64(nil), xs...))
	if m != 0.2 {
		t.Fatalf("median %v", m)
	}
	if mad := MAD(xs, m); mad < 0.099 || mad > 0.101 {
		t.Fatalf("mad %v", mad)
	}
}
