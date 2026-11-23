// Package ntp implements the subset of NTPv4 (RFC 5905) needed for an SNTP
// client and a small, well-behaved LAN time server.
package ntp

import (
	"encoding/binary"
	"errors"
	"time"
)

// PacketSize is the size of an NTP header without extensions.
const PacketSize = 48

// Protocol modes.
const (
	ModeClient = 3
	ModeServer = 4
)

// Leap indicator values.
const (
	LeapNone    = 0
	LeapAlarm   = 3 // clock not synchronized
	MaxStratum  = 15
	Unsynced    = 16
	ntpEpochOff = 2208988800 // seconds between 1900-01-01 and 1970-01-01
)

// Timestamp is a raw 64-bit NTP timestamp (seconds since 1900 + 32-bit fraction).
type Timestamp uint64

// ToTimestamp converts a time to NTP format. Era rollover (2036) is handled by
// truncation to 32 bits, as the protocol specifies.
func ToTimestamp(t time.Time) Timestamp {
	if t.IsZero() {
		return 0
	}
	ns := t.UnixNano()
	sec := uint64(ns/1e9 + ntpEpochOff)
	frac := uint64(ns%1e9) << 32 / 1e9
	if ns%1e9 < 0 { // times before 1970 are not expected; keep arithmetic sane
		frac = 0
	}
	return Timestamp(sec<<32 | frac)
}

// Time converts an NTP timestamp to time.Time, choosing the 136-year era that
// places the result closest to the pivot time.
func (ts Timestamp) Time(pivot time.Time) time.Time {
	if ts == 0 {
		return time.Time{}
	}
	sec := int64(ts >> 32)
	frac := int64(uint32(ts))
	nsec := (frac*1e9 + (1 << 31)) >> 32
	const era = int64(1) << 32
	pivotNTP := pivot.Unix() + ntpEpochOff
	// Pick the era closest to the pivot.
	base := (pivotNTP / era) * era
	cands := []int64{base - era + sec, base + sec, base + era + sec}
	best := cands[0]
	for _, c := range cands[1:] {
		if abs64(c-pivotNTP) < abs64(best-pivotNTP) {
			best = c
		}
	}
	return time.Unix(best-ntpEpochOff, nsec).UTC()
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// Short converts an NTP 16.16 fixed-point duration.
type Short uint32

// Duration returns the value as a time.Duration.
func (s Short) Duration() time.Duration {
	return time.Duration((int64(s) * int64(time.Second)) >> 16)
}

// ToShort converts a duration to 16.16 fixed point, saturating on overflow.
func ToShort(d time.Duration) Short {
	if d < 0 {
		d = 0
	}
	v := (int64(d) << 16) / int64(time.Second)
	if v > 0xFFFFFFFF {
		v = 0xFFFFFFFF
	}
	return Short(v)
}

// Packet is an NTP header.
type Packet struct {
	Leap           uint8
	Version        uint8
	Mode           uint8
	Stratum        uint8
	Poll           int8
	Precision      int8
	RootDelay      Short
	RootDispersion Short
	RefID          uint32
	RefTime        Timestamp
	Origin         Timestamp
	Receive        Timestamp
	Transmit       Timestamp
}

// Marshal encodes the packet.
func (p *Packet) Marshal() []byte {
	b := make([]byte, PacketSize)
	b[0] = p.Leap<<6 | (p.Version&7)<<3 | p.Mode&7
	b[1] = p.Stratum
	b[2] = byte(p.Poll)
	b[3] = byte(p.Precision)
	binary.BigEndian.PutUint32(b[4:], uint32(p.RootDelay))
	binary.BigEndian.PutUint32(b[8:], uint32(p.RootDispersion))
	binary.BigEndian.PutUint32(b[12:], p.RefID)
	binary.BigEndian.PutUint64(b[16:], uint64(p.RefTime))
	binary.BigEndian.PutUint64(b[24:], uint64(p.Origin))
	binary.BigEndian.PutUint64(b[32:], uint64(p.Receive))
	binary.BigEndian.PutUint64(b[40:], uint64(p.Transmit))
	return b
}

// ErrShortPacket is returned for datagrams smaller than an NTP header.
var ErrShortPacket = errors.New("ntp: packet too short")

// Unmarshal decodes a packet. Extension fields and MACs are ignored.
func Unmarshal(b []byte) (*Packet, error) {
	if len(b) < PacketSize {
		return nil, ErrShortPacket
	}
	return &Packet{
		Leap:           b[0] >> 6,
		Version:        (b[0] >> 3) & 7,
		Mode:           b[0] & 7,
		Stratum:        b[1],
		Poll:           int8(b[2]),
		Precision:      int8(b[3]),
		RootDelay:      Short(binary.BigEndian.Uint32(b[4:])),
		RootDispersion: Short(binary.BigEndian.Uint32(b[8:])),
		RefID:          binary.BigEndian.Uint32(b[12:]),
		RefTime:        Timestamp(binary.BigEndian.Uint64(b[16:])),
		Origin:         Timestamp(binary.BigEndian.Uint64(b[24:])),
		Receive:        Timestamp(binary.BigEndian.Uint64(b[32:])),
		Transmit:       Timestamp(binary.BigEndian.Uint64(b[40:])),
	}, nil
}

// RefIDString renders a reference ID: ASCII for stratum 0/1, dotted IPv4 otherwise.
func RefIDString(stratum uint8, id uint32) string {
	b := []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
	if stratum <= 1 {
		out := make([]byte, 0, 4)
		for _, c := range b {
			if c == 0 {
				break
			}
			if c < 32 || c > 126 {
				c = '?'
			}
			out = append(out, c)
		}
		return string(out)
	}
	return itoa(b[0]) + "." + itoa(b[1]) + "." + itoa(b[2]) + "." + itoa(b[3])
}

func itoa(v byte) string {
	if v == 0 {
		return "0"
	}
	var buf [3]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = '0' + v%10
		v /= 10
	}
	return string(buf[i:])
}

// ASCIIRefID packs up to four ASCII characters into a reference ID.
func ASCIIRefID(s string) uint32 {
	var b [4]byte
	copy(b[:], s)
	return binary.BigEndian.Uint32(b[:])
}
