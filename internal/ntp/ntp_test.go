package ntp

import (
	"context"
	"testing"
	"time"
)

func TestTimestampRoundTrip(t *testing.T) {
	for _, tc := range []time.Time{
		time.Date(2026, 11, 23, 18, 59, 15, 123456789, time.UTC),
		time.Date(2036, 2, 7, 6, 28, 17, 0, time.UTC), // just past era 0 rollover
		time.Date(2001, 1, 1, 0, 0, 0, 500000000, time.UTC),
	} {
		got := ToTimestamp(tc).Time(tc)
		if d := got.Sub(tc); d < -time.Microsecond || d > time.Microsecond {
			t.Errorf("round trip %v -> %v (diff %v)", tc, got, d)
		}
	}
}

func TestPacketMarshal(t *testing.T) {
	p := Packet{Leap: 0, Version: 4, Mode: ModeServer, Stratum: 2, Poll: 6, Precision: -20,
		RootDelay: ToShort(12 * time.Millisecond), RootDispersion: ToShort(3 * time.Millisecond),
		RefID: ASCIIRefID("GPS"), Transmit: 0x0102030405060708}
	q, err := Unmarshal(p.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if *q != p {
		t.Fatalf("mismatch:\n got %+v\nwant %+v", *q, p)
	}
	if s := RefIDString(1, q.RefID); s != "GPS" {
		t.Errorf("refid %q", s)
	}
	if s := RefIDString(3, 0xC0A80101); s != "192.168.1.1" {
		t.Errorf("refid %q", s)
	}
}

// The client and server must agree: a server whose clock is 250 ms ahead of
// the client should be measured as a +250 ms offset.
func TestClientAgainstServer(t *testing.T) {
	serverOffset := 250 * time.Millisecond
	srv := &Server{
		Addr: "127.0.0.1:0",
		Now:  func() time.Time { return time.Now().Add(serverOffset) },
		State: func() ServerState {
			return ServerState{Synced: true, Stratum: 2, RefID: ASCIIRefID("GPS"), RefTime: time.Now()}
		},
	}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	resp, err := Query(ctx, srv.LocalAddr().String(), 2*time.Second, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if d := resp.Offset - serverOffset; d < -5*time.Millisecond || d > 5*time.Millisecond {
		t.Fatalf("offset %v, want ~%v", resp.Offset, serverOffset)
	}
	if resp.Stratum != 2 {
		t.Fatalf("unexpected stratum %d", resp.Stratum)
	}
	if served, _ := srv.Stats(); served != 1 {
		t.Fatalf("served %d", served)
	}
}

func TestUnsyncedServerRejected(t *testing.T) {
	srv := &Server{
		Addr:  "127.0.0.1:0",
		Now:   time.Now,
		State: func() ServerState { return ServerState{Synced: false} },
	}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if _, err := Query(ctx, srv.LocalAddr().String(), 2*time.Second, time.Now); err == nil {
		t.Fatal("expected unsynchronized server to be rejected")
	}
}

func TestRateLimit(t *testing.T) {
	s := &Server{clients: map[string]*clientRate{}}
	addr := &fakeAddr{"10.0.0.5:123"}
	allowed := 0
	for i := 0; i < 50; i++ {
		if s.allow(addr) {
			allowed++
		}
	}
	if allowed < int(rateBurst) || allowed > int(rateBurst)+1 {
		t.Fatalf("allowed %d burst requests", allowed)
	}
}

type fakeAddr struct{ s string }

func (f *fakeAddr) Network() string { return "udp" }
func (f *fakeAddr) String() string  { return f.s }
