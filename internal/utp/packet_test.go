package utp

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// TestMarshalKnownBytes pins the exact wire layout against hand-computed
// bytes — the same discipline internal/mse's known-answer hash tests and
// internal/peer's holepunch wire test already use, so a field-order or
// bit-packing mistake can't hide behind a tautological round trip alone.
func TestMarshalKnownBytes(t *testing.T) {
	p := &Packet{
		Type:          STData,
		ConnID:        0x1234,
		Timestamp:     0x01020304,
		TimestampDiff: 0x05060708,
		WndSize:       0x0009fbf0,
		SeqNr:         0x000a,
		AckNr:         0x0009,
		Payload:       []byte{0xde, 0xad},
	}
	got := hex.EncodeToString(p.Marshal())
	// byte0: type=0(ST_DATA)<<4 | version=1 = 0x01
	// byte1: extension = 0x00 (no SACK)
	// bytes2-3: conn_id = 0x1234
	// bytes4-7: timestamp = 0x01020304
	// bytes8-11: timestamp_diff = 0x05060708
	// bytes12-15: wnd_size = 0x0009fbf0
	// bytes16-17: seq_nr = 0x000a
	// bytes18-19: ack_nr = 0x0009
	// payload: dead
	want := "0100" + "1234" + "01020304" + "05060708" + "0009fbf0" + "000a" + "0009" + "dead"
	if got != want {
		t.Fatalf("Marshal() = %s\nwant        %s", got, want)
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	p := &Packet{
		Type:          STSyn,
		ConnID:        4242,
		Timestamp:     111111,
		TimestampDiff: 222222,
		WndSize:       65536,
		SeqNr:         1,
		AckNr:         0,
		Payload:       []byte("hello utp"),
	}
	got, err := Unmarshal(p.Marshal())
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Type != p.Type || got.ConnID != p.ConnID || got.Timestamp != p.Timestamp ||
		got.TimestampDiff != p.TimestampDiff || got.WndSize != p.WndSize ||
		got.SeqNr != p.SeqNr || got.AckNr != p.AckNr || !bytes.Equal(got.Payload, p.Payload) {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, p)
	}
}

func TestMarshalUnmarshalRoundTripWithSACK(t *testing.T) {
	p := &Packet{
		Type:   STState,
		ConnID: 7,
		SeqNr:  10,
		AckNr:  20,
		SACK:   []byte{0x01, 0x02, 0x03, 0x04},
	}
	got, err := Unmarshal(p.Marshal())
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !bytes.Equal(got.SACK, p.SACK) {
		t.Fatalf("SACK = %x, want %x", got.SACK, p.SACK)
	}
}

// TestUnmarshalSkipsAnUnknownExtension proves BEP 29's forward-
// compatibility chain works: an extension type this package doesn't
// interpret must be skipped over (using its own length), not treated as
// an error, and a real SACK extension chained after it must still be
// found and parsed correctly.
func TestUnmarshalSkipsAnUnknownExtension(t *testing.T) {
	var buf bytes.Buffer
	// Fixed header: type=ST_STATE, version=1, extension=99 (unknown, first
	// in the chain).
	buf.WriteByte(byte(STState)<<4 | protocolVersion)
	buf.WriteByte(99)
	buf.Write([]byte{0, 0})       // conn_id
	buf.Write([]byte{0, 0, 0, 0}) // timestamp
	buf.Write([]byte{0, 0, 0, 0}) // timestamp_diff
	buf.Write([]byte{0, 0, 0, 0}) // wnd_size
	buf.Write([]byte{0, 5})       // seq_nr
	buf.Write([]byte{0, 3})       // ack_nr
	// Unknown extension 99: next=extSelectiveAck(1), len=3, 3 bytes of
	// data this package must skip without interpreting.
	buf.WriteByte(extSelectiveAck)
	buf.WriteByte(3)
	buf.Write([]byte{0xAA, 0xBB, 0xCC})
	// Chained SACK extension: next=0 (terminator), len=4, real bitmask.
	buf.WriteByte(0)
	buf.WriteByte(4)
	buf.Write([]byte{0x11, 0x22, 0x33, 0x44})

	got, err := Unmarshal(buf.Bytes())
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !bytes.Equal(got.SACK, []byte{0x11, 0x22, 0x33, 0x44}) {
		t.Fatalf("SACK = %x, want the chained real SACK bytes, not the skipped unknown extension's", got.SACK)
	}
}

func TestUnmarshalRejectsShortHeader(t *testing.T) {
	if _, err := Unmarshal(make([]byte, 19)); err != ErrShortHeader {
		t.Fatalf("err = %v, want ErrShortHeader", err)
	}
}

func TestUnmarshalRejectsWrongVersion(t *testing.T) {
	buf := make([]byte, headerLen)
	buf[0] = byte(STData)<<4 | 2 // version 2, not the 1 this package speaks
	if _, err := Unmarshal(buf); err != ErrUnsupportedVersion {
		t.Fatalf("err = %v, want ErrUnsupportedVersion", err)
	}
}

func TestUnmarshalRejectsUnknownType(t *testing.T) {
	buf := make([]byte, headerLen)
	buf[0] = byte(15)<<4 | protocolVersion // type 15 doesn't exist (max real type is 4)
	if _, err := Unmarshal(buf); err != ErrUnknownType {
		t.Fatalf("err = %v, want ErrUnknownType", err)
	}
}

func TestUnmarshalRejectsTruncatedExtension(t *testing.T) {
	buf := make([]byte, headerLen+1) // claims an extension follows but there's only 1 byte left, not the required 2+
	buf[0] = byte(STState)<<4 | protocolVersion
	buf[1] = extSelectiveAck
	if _, err := Unmarshal(buf); err != ErrMalformedExtension {
		t.Fatalf("err = %v, want ErrMalformedExtension", err)
	}
}

func TestUnmarshalRejectsUndersizedSACK(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(byte(STState)<<4 | protocolVersion)
	buf.WriteByte(extSelectiveAck)
	buf.Write(make([]byte, 18)) // rest of the fixed header
	buf.WriteByte(0)            // no further extension
	buf.WriteByte(2)            // length 2 - below BEP 29's own 4-byte minimum
	buf.Write([]byte{0x00, 0x00})
	if _, err := Unmarshal(buf.Bytes()); err != ErrMalformedSACK {
		t.Fatalf("err = %v, want ErrMalformedSACK", err)
	}
}

func TestSackBitIndexing(t *testing.T) {
	const ackNr = 100
	sack := make([]byte, 4) // 32 bits, seq 102..133

	// Bit 0 of the mask is ackNr+2, per BEP 29 - not ackNr+1.
	setSackBit(sack, ackNr, ackNr+2)
	if !sackHasBit(sack, ackNr, ackNr+2) {
		t.Fatal("bit for ackNr+2 not set after setSackBit")
	}
	if sackHasBit(sack, ackNr, ackNr+1) {
		t.Fatal("ackNr+1 must never be representable in the mask - it's the gap the SACK exists because of")
	}
	if sackHasBit(sack, ackNr, ackNr+3) {
		t.Fatal("an unset bit reported as set")
	}

	setSackBit(sack, ackNr, ackNr+10)
	if !sackHasBit(sack, ackNr, ackNr+10) {
		t.Fatal("bit for ackNr+10 not set")
	}
	// Confirm it landed in the expected byte/bit (idx=8, so byte 1 bit 0).
	if sack[1]&0x01 == 0 {
		t.Fatalf("expected byte 1 bit 0 set for ackNr+10, got sack=%08b", sack)
	}
}

func TestSackBitIndexingWrapsAroundSeqNrOverflow(t *testing.T) {
	// ackNr near the top of the uint16 range - the "ahead" sequence
	// numbers wrap around through 0, and sack indexing must still work.
	ackNr := uint16(65534)
	sack := make([]byte, 4)
	seq := ackNr + 5 // wraps at runtime: 65534+5 = 3 (mod 65536)

	setSackBit(sack, ackNr, seq)
	if !sackHasBit(sack, ackNr, seq) {
		t.Fatalf("wrapped seq %d not correctly marked/read back", seq)
	}
}
