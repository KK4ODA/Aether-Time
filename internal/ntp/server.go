// SPDX-License-Identifier: GPL-3.0-or-later

package ntp

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ServerState describes the time being served. It is queried for every reply.
type ServerState struct {
	Synced         bool
	Stratum        uint8 // our stratum (reference stratum + 1)
	RefID          uint32
	RefTime        time.Time
	RootDelay      time.Duration
	RootDispersion time.Duration
}

// Server answers NTP client requests using the local (disciplined) clock.
type Server struct {
	Addr  string
	Now   func() time.Time
	State func() ServerState

	served  atomic.Uint64
	dropped atomic.Uint64

	mu      sync.Mutex
	conn    net.PacketConn
	clients map[string]*clientRate
}

type clientRate struct {
	tokens float64
	last   time.Time
}

const (
	rateBurst  = 8.0 // requests a client may send back-to-back
	ratePerSec = 2.0 // sustained requests per second per client
)

// Stats returns the number of requests answered and dropped.
func (s *Server) Stats() (served, dropped uint64) { return s.served.Load(), s.dropped.Load() }

// LocalAddr returns the bound address once the server is running.
func (s *Server) LocalAddr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	return s.conn.LocalAddr()
}

// Listen binds the socket. Serve must be called afterwards.
func (s *Server) Listen() error {
	conn, err := net.ListenPacket("udp", s.Addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.conn = conn
	s.clients = make(map[string]*clientRate)
	s.mu.Unlock()
	return nil
}

// Serve answers requests until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return errors.New("ntp server: Listen not called")
	}
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	buf := make([]byte, 1024)
	lastGC := time.Now()
	for {
		n, addr, err := conn.ReadFrom(buf)
		recv := s.Now()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		if time.Since(lastGC) > time.Minute {
			s.gc()
			lastGC = time.Now()
		}
		if !s.allow(addr) {
			s.dropped.Add(1)
			continue
		}
		req, err := Unmarshal(buf[:n])
		if err != nil || req.Mode != ModeClient || req.Version < 1 || req.Version > 4 {
			s.dropped.Add(1)
			continue
		}
		reply := s.reply(req, recv)
		if _, err := conn.WriteTo(reply, addr); err == nil {
			s.served.Add(1)
		}
	}
}

func (s *Server) reply(req *Packet, recv time.Time) []byte {
	st := s.State()
	p := Packet{
		Version:        req.Version,
		Mode:           ModeServer,
		Poll:           req.Poll,
		Precision:      -20,
		Origin:         req.Transmit,
		Receive:        ToTimestamp(recv),
		RefID:          st.RefID,
		RefTime:        ToTimestamp(st.RefTime),
		RootDelay:      ToShort(st.RootDelay),
		RootDispersion: ToShort(st.RootDispersion),
	}
	if st.Synced && st.Stratum >= 1 && st.Stratum <= MaxStratum {
		p.Leap = LeapNone
		p.Stratum = st.Stratum
	} else {
		// Tell clients not to trust us rather than serving bad time.
		p.Leap = LeapAlarm
		p.Stratum = Unsynced
	}
	p.Transmit = ToTimestamp(s.Now())
	return p.Marshal()
}

func (s *Server) allow(addr net.Addr) bool {
	host := addr.String()
	if ua, ok := addr.(*net.UDPAddr); ok {
		host = ua.IP.String()
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.clients[host]
	if c == nil {
		if len(s.clients) > 10000 {
			return false
		}
		c = &clientRate{tokens: rateBurst, last: now}
		s.clients[host] = c
	}
	c.tokens += now.Sub(c.last).Seconds() * ratePerSec
	if c.tokens > rateBurst {
		c.tokens = rateBurst
	}
	c.last = now
	if c.tokens < 1 {
		return false
	}
	c.tokens--
	return true
}

func (s *Server) gc() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, c := range s.clients {
		if time.Since(c.last) > 10*time.Minute {
			delete(s.clients, k)
		}
	}
}
