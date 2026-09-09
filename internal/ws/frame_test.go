package ws

import (
	"bytes"
	"testing"
)

// maskPayload masks p in place with key, the same XOR-cycling operation
// readFrame uses to unmask — used here to build client-shaped (masked)
// frames for readFrame's own tests.
func maskPayload(p []byte, key [4]byte) {
	for i := range p {
		p[i] ^= key[i%4]
	}
}

// writeMaskedFrame writes a client-shaped (masked) frame directly to buf,
// bypassing writeFrame (which never masks, since it's the server-send
// path) — this is readFrame's test fixture for what a real browser client
// actually puts on the wire.
func writeMaskedFrame(t *testing.T, buf *bytes.Buffer, fin bool, opcode Opcode, payload []byte) {
	t.Helper()
	var head byte
	if fin {
		head = 0x80
	}
	head |= byte(opcode)
	buf.WriteByte(head)

	key := [4]byte{0x11, 0x22, 0x33, 0x44}
	masked := append([]byte(nil), payload...)
	maskPayload(masked, key)

	switch {
	case len(payload) < 126:
		buf.WriteByte(0x80 | byte(len(payload)))
	case len(payload) <= 0xFFFF:
		buf.WriteByte(0x80 | 126)
		buf.WriteByte(byte(len(payload) >> 8))
		buf.WriteByte(byte(len(payload)))
	default:
		buf.WriteByte(0x80 | 127)
		for i := 7; i >= 0; i-- {
			buf.WriteByte(byte(len(payload) >> (8 * i)))
		}
	}
	buf.Write(key[:])
	buf.Write(masked)
}

func TestReadFrameUnmasksPayload(t *testing.T) {
	var buf bytes.Buffer
	writeMaskedFrame(t, &buf, true, OpText, []byte("hello"))

	f, err := readFrame(&buf)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if !f.fin || f.opcode != OpText {
		t.Fatalf("fin=%v opcode=%#x, want fin=true opcode=OpText", f.fin, f.opcode)
	}
	if string(f.payload) != "hello" {
		t.Fatalf("payload = %q, want %q", f.payload, "hello")
	}
}

func TestReadFrameRejectsUnmaskedClientFrame(t *testing.T) {
	var buf bytes.Buffer
	// A plain writeFrame call never sets the mask bit - exactly what an
	// RFC-6455-compliant server must reject from a client.
	if err := writeFrame(&buf, true, OpText, []byte("hi")); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	if _, err := readFrame(&buf); err == nil {
		t.Fatal("readFrame accepted an unmasked frame, want an error")
	}
}

func TestReadFrameHandlesExtendedLengths(t *testing.T) {
	for _, size := range []int{0, 1, 125, 126, 127, 65535, 65536, 70000} {
		t.Run("", func(t *testing.T) {
			payload := bytes.Repeat([]byte{0xAB}, size)
			var buf bytes.Buffer
			writeMaskedFrame(t, &buf, true, OpBinary, payload)

			f, err := readFrame(&buf)
			if err != nil {
				t.Fatalf("readFrame(size=%d): %v", size, err)
			}
			if len(f.payload) != size {
				t.Fatalf("payload length = %d, want %d", len(f.payload), size)
			}
			if !bytes.Equal(f.payload, payload) {
				t.Fatal("payload content mismatch")
			}
		})
	}
}

func TestReadFrameRejectsOversizedPayload(t *testing.T) {
	var buf bytes.Buffer
	writeMaskedFrame(t, &buf, true, OpBinary, bytes.Repeat([]byte{0}, maxFramePayload+1))
	if _, err := readFrame(&buf); err == nil {
		t.Fatal("readFrame accepted a payload over maxFramePayload, want an error")
	}
}

func TestReadFrameRejectsFragmentedControlFrame(t *testing.T) {
	var buf bytes.Buffer
	writeMaskedFrame(t, &buf, false /* fin */, OpPing, []byte("x"))
	if _, err := readFrame(&buf); err == nil {
		t.Fatal("readFrame accepted a fragmented control frame, want an error")
	}
}

func TestReadFrameRejectsOversizedControlFrame(t *testing.T) {
	var buf bytes.Buffer
	writeMaskedFrame(t, &buf, true, OpPing, bytes.Repeat([]byte{0}, 126))
	if _, err := readFrame(&buf); err == nil {
		t.Fatal("readFrame accepted a control frame over 125 bytes, want an error")
	}
}

func TestReadFrameRejectsReservedBits(t *testing.T) {
	var buf bytes.Buffer
	writeMaskedFrame(t, &buf, true, OpText, []byte("x"))
	raw := buf.Bytes()
	raw[0] |= 0x40 // set RSV1
	if _, err := readFrame(bytes.NewReader(raw)); err == nil {
		t.Fatal("readFrame accepted a frame with a reserved bit set, want an error")
	}
}

func TestWriteFrameNeverMasks(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, true, OpText, []byte("hello")); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	if buf.Bytes()[1]&0x80 != 0 {
		t.Fatal("writeFrame set the mask bit - a server must never mask outgoing frames")
	}
}

func TestWriteFrameRoundTripsThroughAManualUnmaskedParse(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, true, OpBinary, []byte("payload data")); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	raw := buf.Bytes()
	if raw[0] != 0x80|byte(OpBinary) {
		t.Fatalf("first byte = %#x, want fin=1 opcode=OpBinary", raw[0])
	}
	if raw[1] != byte(len("payload data")) {
		t.Fatalf("length byte = %d, want %d", raw[1], len("payload data"))
	}
	if string(raw[2:]) != "payload data" {
		t.Fatalf("payload = %q", raw[2:])
	}
}
