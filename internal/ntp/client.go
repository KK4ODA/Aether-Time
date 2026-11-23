// SPDX-License-Identifier: GPL-3.0-or-later

package ntp

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

// Response is the result of one client/server exchange.
type Response struct {
	Server         string
	Address        string
	Offset         time.Duration // server time minus local time
	Delay          time.Duration // round-trip network delay
	Stratum        uint8
	RefID          string
	Leap           uint8
	RootDelay      time.Duration
	RootDispersion time.Duration
	Precision      time.Duration
	ServerTime     time.Time
	LocalTime      time.Time // local time when the reply arrived
}

// ErrorBound estimates the maximum error of the offset (the synchronization
// distance in RFC 5905 terms, simplified).
func (r *Response) ErrorBound() time.Duration {
	return r.Delay/2 + r.RootDelay/2 + r.RootDispersion + r.Precision
}

// KissOfDeathError is returned when the server asks us to go away or slow down.
type KissOfDeathError struct{ Code string }

func (e *KissOfDeathError) Error() string { return "ntp: kiss-of-death " + e.Code }

// Query performs one SNTP exchange with addr (host or host:port). now supplies
// the local clock; pass time.Now for the real system clock.
func Query(ctx context.Context, addr string, timeout time.Duration, now func() time.Time) (*Response, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "123")
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	// A random transmit timestamp (as chrony does) means a reply can only be
	// matched by someone who saw our request; it also avoids leaking our clock.
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	xmt := Timestamp(binary.BigEndian.Uint64(nonce[:]))
	req := Packet{Version: 4, Mode: ModeClient, Transmit: xmt}

	start := time.Now() // monotonic reference, independent of the clock being measured
	t1 := now()
	if _, err := conn.Write(req.Marshal()); err != nil {
		return nil, err
	}
	buf := make([]byte, 512)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		// Use the monotonic clock for the local interval so a clock step in the
		// middle of a query cannot corrupt the result.
		t4 := t1.Add(time.Since(start))
		p, err := Unmarshal(buf[:n])
		if err != nil {
			continue
		}
		if p.Origin != xmt {
			continue // stale or spoofed
		}
		return evaluate(addr, conn.RemoteAddr().String(), p, t1, t4)
	}
}

func evaluate(server, address string, p *Packet, t1, t4 time.Time) (*Response, error) {
	if p.Mode != ModeServer {
		return nil, fmt.Errorf("ntp: unexpected mode %d", p.Mode)
	}
	if p.Stratum == 0 {
		return nil, &KissOfDeathError{Code: RefIDString(0, p.RefID)}
	}
	if p.Stratum > MaxStratum {
		return nil, errors.New("ntp: server is unsynchronized (stratum 16)")
	}
	if p.Leap == LeapAlarm {
		return nil, errors.New("ntp: server clock not synchronized (leap alarm)")
	}
	if p.Transmit == 0 || p.Receive == 0 {
		return nil, errors.New("ntp: invalid server timestamps")
	}
	t2 := p.Receive.Time(t1)
	t3 := p.Transmit.Time(t1)
	offset := (t2.Sub(t1) + t3.Sub(t4)) / 2
	delay := t4.Sub(t1) - t3.Sub(t2)
	if delay < 0 {
		delay = 0
	}
	prec := time.Duration(0)
	if p.Precision < 0 && p.Precision > -40 {
		prec = time.Second >> uint(-p.Precision)
	}
	return &Response{
		Server:         server,
		Address:        address,
		Offset:         offset,
		Delay:          delay,
		Stratum:        p.Stratum,
		RefID:          RefIDString(p.Stratum, p.RefID),
		Leap:           p.Leap,
		RootDelay:      p.RootDelay.Duration(),
		RootDispersion: p.RootDispersion.Duration(),
		Precision:      prec,
		ServerTime:     t3,
		LocalTime:      t4,
	}, nil
}
