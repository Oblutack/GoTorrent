package peer

import (
	"net"
	"testing"
)

func TestHolepunchMarshalUnmarshalRoundTripsIPv4(t *testing.T) {
	want := HolepunchMessage{Type: HolepunchConnect, Addr: net.ParseIP("203.0.113.5").To4(), Port: 6881, ErrCode: 0}
	raw, err := marshalHolepunch(want)
	if err != nil {
		t.Fatalf("marshalHolepunch: %v", err)
	}
	// msg_type(1) + addr_type(1) + addr(4) + port(2) + err_code(4) = 12 bytes.
	if len(raw) != 12 {
		t.Fatalf("marshaled IPv4 message is %d bytes, want 12", len(raw))
	}
	if raw[1] != holepunchAddrIPv4 {
		t.Fatalf("addr_type byte = %d, want %d (IPv4)", raw[1], holepunchAddrIPv4)
	}

	got, err := unmarshalHolepunch(raw)
	if err != nil {
		t.Fatalf("unmarshalHolepunch: %v", err)
	}
	if got.Type != want.Type || !got.Addr.Equal(want.Addr) || got.Port != want.Port || got.ErrCode != want.ErrCode {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestHolepunchMarshalUnmarshalRoundTripsIPv6(t *testing.T) {
	want := HolepunchMessage{
		Type:    HolepunchError,
		Addr:    net.ParseIP("2001:db8::1"),
		Port:    51413,
		ErrCode: HolepunchErrNotConnected,
	}
	raw, err := marshalHolepunch(want)
	if err != nil {
		t.Fatalf("marshalHolepunch: %v", err)
	}
	// msg_type(1) + addr_type(1) + addr(16) + port(2) + err_code(4) = 24 bytes.
	if len(raw) != 24 {
		t.Fatalf("marshaled IPv6 message is %d bytes, want 24", len(raw))
	}
	if raw[1] != holepunchAddrIPv6 {
		t.Fatalf("addr_type byte = %d, want %d (IPv6)", raw[1], holepunchAddrIPv6)
	}

	got, err := unmarshalHolepunch(raw)
	if err != nil {
		t.Fatalf("unmarshalHolepunch: %v", err)
	}
	if got.Type != want.Type || !got.Addr.Equal(want.Addr) || got.Port != want.Port || got.ErrCode != want.ErrCode {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

// TestHolepunchWireFormatMatchesTheSpecByteLayout pins the exact byte
// layout BEP 55 defines — msg_type, addr_type, address, big-endian port,
// big-endian err_code — against hand-computed bytes, not just a
// marshal/unmarshal round trip (which would still pass if both sides
// shared the same bug).
func TestHolepunchWireFormatMatchesTheSpecByteLayout(t *testing.T) {
	msg := HolepunchMessage{
		Type:    HolepunchRendezvous,
		Addr:    net.IPv4(198, 51, 100, 7),
		Port:    0x1A85, // 6789
		ErrCode: 0,
	}
	raw, err := marshalHolepunch(msg)
	if err != nil {
		t.Fatalf("marshalHolepunch: %v", err)
	}
	want := []byte{
		0x00,            // msg_type = rendezvous
		0x00,            // addr_type = IPv4
		198, 51, 100, 7, // addr
		0x1A, 0x85, // port, big-endian
		0x00, 0x00, 0x00, 0x00, // err_code
	}
	if len(raw) != len(want) {
		t.Fatalf("got %d bytes, want %d: got=%x want=%x", len(raw), len(want), raw, want)
	}
	for i := range want {
		if raw[i] != want[i] {
			t.Fatalf("byte %d: got 0x%02x, want 0x%02x (full: got=%x want=%x)", i, raw[i], want[i], raw, want)
		}
	}
}

func TestUnmarshalHolepunchRejectsWrongLengthForAddrType(t *testing.T) {
	// addr_type says IPv4 (4-byte address) but only 2 address bytes follow.
	raw := []byte{0x01, 0x00, 0x01, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if _, err := unmarshalHolepunch(raw); err == nil {
		t.Fatal("unmarshalHolepunch accepted a length that doesn't match its own addr_type")
	}
}

func TestUnmarshalHolepunchRejectsUnknownAddrType(t *testing.T) {
	raw := []byte{0x00, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if _, err := unmarshalHolepunch(raw); err == nil {
		t.Fatal("unmarshalHolepunch accepted an addr_type that is neither IPv4 nor IPv6")
	}
}

func TestUnmarshalHolepunchRejectsTooShort(t *testing.T) {
	if _, err := unmarshalHolepunch([]byte{0x00}); err == nil {
		t.Fatal("unmarshalHolepunch accepted a 1-byte message")
	}
}

func TestHolepunchErrorStringNamesKnownCodesAndFallsBackForUnknownOnes(t *testing.T) {
	cases := map[uint32]string{
		HolepunchErrNoSuchPeer:   "no such peer",
		HolepunchErrNotConnected: "not connected",
		HolepunchErrNoSupport:    "no support",
		HolepunchErrNoSelf:       "no self",
	}
	for code, want := range cases {
		if got := HolepunchErrorString(code); got != want {
			t.Fatalf("HolepunchErrorString(%d) = %q, want %q", code, got, want)
		}
	}
	if got := HolepunchErrorString(99); got == "" {
		t.Fatal("HolepunchErrorString(99) returned an empty string for an unknown code")
	}
}
