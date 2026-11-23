// SPDX-License-Identifier: GPL-3.0-or-later

package wsjtx

import (
	"testing"
	"time"
)

func TestDecodeRoundTrip(t *testing.T) {
	in := Decode{New: true, Time: 18*time.Hour + 59*time.Minute + 15*time.Second, SNR: -12, DeltaTime: 0.3,
		DeltaFreq: 1234, Mode: "~", Message: "CQ KK4ODA EM73"}
	h, msg, err := Parse(EncodeDecode("WSJT-X", in))
	if err != nil {
		t.Fatal(err)
	}
	if h.ID != "WSJT-X" || h.Type != TypeDecode {
		t.Fatalf("header %+v", h)
	}
	d, ok := msg.(*Decode)
	if !ok || *d != in {
		t.Fatalf("got %+v want %+v", msg, in)
	}
	if ModeName(d.Mode) != "FT8" {
		t.Fatal("mode name")
	}
}

func TestStatusRoundTrip(t *testing.T) {
	in := Status{DialFrequency: 14074000, Mode: "FT8", TxEnabled: true, Transmitting: true, Decoding: false,
		RxDF: 1500, TxDF: 1500, DECall: "KK4ODA", DEGrid: "EM73"}
	_, msg, err := Parse(EncodeStatus("JTDX", in))
	if err != nil {
		t.Fatal(err)
	}
	if s := msg.(*Status); *s != in {
		t.Fatalf("got %+v want %+v", s, in)
	}
}

// Older senders stop after the Transmitting flag; that must still parse.
func TestStatusTruncatedOptionalFields(t *testing.T) {
	b := EncodeStatus("X", Status{Mode: "FT4", Transmitting: true})
	cut := len(b) - (1 + 4 + 4 + 4 + 4 + 4) // drop Decoding .. DXGrid (empty strings)
	_, msg, err := Parse(b[:cut])
	if err != nil {
		t.Fatal(err)
	}
	if s := msg.(*Status); !s.Transmitting || s.Mode != "FT4" {
		t.Fatalf("%+v", s)
	}
}

func TestRejectsGarbage(t *testing.T) {
	if _, _, err := Parse([]byte("hello world, not wsjtx")); err == nil {
		t.Fatal("expected error")
	}
	b := EncodeDecode("X", Decode{Mode: "~", Message: "CQ"})
	if _, _, err := Parse(b[:20]); err == nil {
		t.Fatal("expected truncation error")
	}
}

func TestLocation(t *testing.T) {
	h, _, err := Parse(EncodeLocation("WSJT-X", "EM73tu"))
	if err != nil || h.Type != TypeLocation {
		t.Fatalf("%+v %v", h, err)
	}
}
