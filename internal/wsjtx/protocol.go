// Package wsjtx encodes and decodes the WSJT-X UDP message protocol
// (NetworkMessage.hpp), which JTDX and MSHV also speak. Values are Qt
// QDataStream big-endian encodings.
package wsjtx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"time"
)

// Magic is the first word of every datagram.
const Magic uint32 = 0xadbccbda

// Message type numbers.
const (
	TypeHeartbeat  = 0
	TypeStatus     = 1
	TypeDecode     = 2
	TypeClear      = 3
	TypeReply      = 4
	TypeQSOLogged  = 5
	TypeClose      = 6
	TypeReplay     = 7
	TypeHaltTx     = 8
	TypeFreeText   = 9
	TypeWSPRDecode = 10
	TypeLocation   = 11
)

// Header is common to every message.
type Header struct {
	Schema uint32
	Type   uint32
	ID     string
}

// Heartbeat is sent periodically by WSJT-X.
type Heartbeat struct {
	MaxSchema uint32
	Version   string
	Revision  string
}

// Status reports the operating state. Fields beyond Decoding are optional
// and depend on the sender's version.
type Status struct {
	DialFrequency uint64
	Mode          string
	DXCall        string
	Report        string
	TxMode        string
	TxEnabled     bool
	Transmitting  bool
	Decoding      bool
	RxDF          uint32
	TxDF          uint32
	DECall        string
	DEGrid        string
	DXGrid        string
}

// Decode is one decoded message from the band.
type Decode struct {
	New           bool
	Time          time.Duration // time of the period, ms since midnight on the sender's clock
	SNR           int32
	DeltaTime     float64 // seconds
	DeltaFreq     uint32
	Mode          string // single-character mode code, e.g. "~" for FT8
	Message       string
	LowConfidence bool
	OffAir        bool
}

// ErrNotWSJTX is returned for datagrams without the magic number.
var ErrNotWSJTX = errors.New("wsjtx: bad magic")

// errShort marks a truncated field.
var errShort = errors.New("wsjtx: truncated message")

type reader struct {
	b   []byte
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if len(r.b) < n {
		r.err = errShort
		return nil
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v
}

func (r *reader) u8() uint8 {
	if v := r.take(1); v != nil {
		return v[0]
	}
	return 0
}
func (r *reader) boolean() bool { return r.u8() != 0 }
func (r *reader) u32() uint32 {
	if v := r.take(4); v != nil {
		return binary.BigEndian.Uint32(v)
	}
	return 0
}
func (r *reader) i32() int32 { return int32(r.u32()) }
func (r *reader) u64() uint64 {
	if v := r.take(8); v != nil {
		return binary.BigEndian.Uint64(v)
	}
	return 0
}
func (r *reader) f64() float64 { return math.Float64frombits(r.u64()) }
func (r *reader) str() string {
	n := r.u32()
	if r.err != nil || n == 0xffffffff {
		return ""
	}
	if n > 1<<16 {
		r.err = errShort
		return ""
	}
	return string(r.take(int(n)))
}
func (r *reader) more() bool { return r.err == nil && len(r.b) > 0 }

// ParseHeader decodes the header and returns a reader positioned at the payload.
func parseHeader(b []byte) (Header, *reader, error) {
	r := &reader{b: b}
	if r.u32() != Magic {
		return Header{}, nil, ErrNotWSJTX
	}
	h := Header{Schema: r.u32(), Type: r.u32(), ID: r.str()}
	if r.err != nil {
		return h, nil, r.err
	}
	return h, r, nil
}

// Parse decodes a datagram. The returned message is one of *Heartbeat,
// *Status, *Decode, or nil for types this package does not interpret.
func Parse(b []byte) (Header, any, error) {
	h, r, err := parseHeader(b)
	if err != nil {
		return h, nil, err
	}
	switch h.Type {
	case TypeHeartbeat:
		m := &Heartbeat{MaxSchema: r.u32(), Version: r.str(), Revision: r.str()}
		if r.err != nil && m.MaxSchema == 0 {
			return h, nil, r.err
		}
		return h, m, nil
	case TypeStatus:
		m := &Status{}
		m.DialFrequency = r.u64()
		m.Mode = r.str()
		m.DXCall = r.str()
		m.Report = r.str()
		m.TxMode = r.str()
		m.TxEnabled = r.boolean()
		m.Transmitting = r.boolean()
		if r.err != nil {
			return h, nil, r.err
		}
		if r.more() {
			m.Decoding = r.boolean()
		}
		if r.more() {
			m.RxDF = r.u32()
			m.TxDF = r.u32()
			m.DECall = r.str()
			m.DEGrid = r.str()
			m.DXGrid = r.str()
		}
		return h, m, nil
	case TypeDecode:
		m := &Decode{}
		m.New = r.boolean()
		m.Time = time.Duration(r.u32()) * time.Millisecond
		m.SNR = r.i32()
		m.DeltaTime = r.f64()
		m.DeltaFreq = r.u32()
		m.Mode = r.str()
		m.Message = r.str()
		if r.err != nil {
			return h, nil, r.err
		}
		if r.more() {
			m.LowConfidence = r.boolean()
		}
		if r.more() {
			m.OffAir = r.boolean()
		}
		if math.IsNaN(m.DeltaTime) || math.IsInf(m.DeltaTime, 0) {
			return h, nil, errors.New("wsjtx: invalid delta time")
		}
		return h, m, nil
	}
	return h, nil, nil
}

type writer struct{ bytes.Buffer }

func (w *writer) u32(v uint32) { _ = binary.Write(&w.Buffer, binary.BigEndian, v) }
func (w *writer) u64(v uint64) { _ = binary.Write(&w.Buffer, binary.BigEndian, v) }
func (w *writer) f64(v float64) {
	w.u64(math.Float64bits(v))
}
func (w *writer) boolean(v bool) {
	if v {
		w.WriteByte(1)
	} else {
		w.WriteByte(0)
	}
}
func (w *writer) str(s string) {
	w.u32(uint32(len(s)))
	w.WriteString(s)
}

func header(typ uint32, id string) *writer {
	w := &writer{}
	w.u32(Magic)
	w.u32(2)
	w.u32(typ)
	w.str(id)
	return w
}

// EncodeLocation builds a Location message that asks WSJT-X (with "Accept UDP
// requests" enabled) to use grid as its temporary locator for AutoGrid.
func EncodeLocation(id, grid string) []byte {
	w := header(TypeLocation, id)
	w.str(grid)
	return w.Bytes()
}

// EncodeHeartbeat builds a heartbeat (used by tests and to announce ourselves).
func EncodeHeartbeat(id, version string) []byte {
	w := header(TypeHeartbeat, id)
	w.u32(3)
	w.str(version)
	w.str("")
	return w.Bytes()
}

// EncodeStatus builds a Status message (used by tests and simulators).
func EncodeStatus(id string, s Status) []byte {
	w := header(TypeStatus, id)
	w.u64(s.DialFrequency)
	w.str(s.Mode)
	w.str(s.DXCall)
	w.str(s.Report)
	w.str(s.TxMode)
	w.boolean(s.TxEnabled)
	w.boolean(s.Transmitting)
	w.boolean(s.Decoding)
	w.u32(s.RxDF)
	w.u32(s.TxDF)
	w.str(s.DECall)
	w.str(s.DEGrid)
	w.str(s.DXGrid)
	return w.Bytes()
}

// EncodeDecode builds a Decode message (used by tests and simulators).
func EncodeDecode(id string, d Decode) []byte {
	w := header(TypeDecode, id)
	w.boolean(d.New)
	w.u32(uint32(d.Time / time.Millisecond))
	w.u32(uint32(d.SNR))
	w.f64(d.DeltaTime)
	w.u32(d.DeltaFreq)
	w.str(d.Mode)
	w.str(d.Message)
	w.boolean(d.LowConfidence)
	w.boolean(d.OffAir)
	return w.Bytes()
}

// ModeName maps WSJT-X's single-character decode mode codes to names.
func ModeName(code string) string {
	switch code {
	case "~":
		return "FT8"
	case "+":
		return "FT4"
	case "#":
		return "JT65"
	case "@":
		return "JT9"
	case "$":
		return "JT4"
	case "&":
		return "MSK144"
	case "`":
		return "FST4"
	case ":":
		return "Q65"
	}
	return code
}
