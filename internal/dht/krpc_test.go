package dht

import (
	"net"
	"reflect"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/logger"
)

func TestMain(m *testing.M) {
	logger.Init(false)
	m.Run()
}

func TestQueryRoundTripsThroughBencode(t *testing.T) {
	payload, err := newQuery("aa", "find_node", findNodeArgs{ID: NodeID{1}, Target: NodeID{2}})
	if err != nil {
		t.Fatalf("newQuery: %v", err)
	}
	msg, err := parseMessage(payload)
	if err != nil {
		t.Fatalf("parseMessage: %v", err)
	}
	if msg.T != "aa" || msg.Y != "q" || msg.Q != "find_node" {
		t.Fatalf("got t=%q y=%q q=%q, want aa/q/find_node", msg.T, msg.Y, msg.Q)
	}
	var args findNodeArgs
	if err := bencode.Unmarshal(msg.A, &args); err != nil {
		t.Fatalf("decoding args: %v", err)
	}
	if args.ID != (NodeID{1}) || args.Target != (NodeID{2}) {
		t.Fatalf("got args %+v", args)
	}
}

func TestResponseRoundTripsThroughBencode(t *testing.T) {
	payload, err := newResponse("bb", getPeersResponse{
		ID:     NodeID{9},
		Token:  "tok",
		Values: [][]byte{{1, 2, 3, 4, 5, 6}},
	})
	if err != nil {
		t.Fatalf("newResponse: %v", err)
	}
	msg, err := parseMessage(payload)
	if err != nil {
		t.Fatalf("parseMessage: %v", err)
	}
	if msg.T != "bb" || msg.Y != "r" {
		t.Fatalf("got t=%q y=%q, want bb/r", msg.T, msg.Y)
	}
	var r getPeersResponse
	if err := bencode.Unmarshal(msg.R, &r); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if r.ID != (NodeID{9}) || r.Token != "tok" || len(r.Values) != 1 {
		t.Fatalf("got response %+v", r)
	}
}

func TestErrorRoundTripsThroughBencode(t *testing.T) {
	payload, err := newKRPCError("cc", 203, "bad token")
	if err != nil {
		t.Fatalf("newKRPCError: %v", err)
	}
	msg, err := parseMessage(payload)
	if err != nil {
		t.Fatalf("parseMessage: %v", err)
	}
	if msg.Y != "e" {
		t.Fatalf("got y=%q, want e", msg.Y)
	}
	err = krpcErrorFromE(msg.E)
	if err == nil {
		t.Fatal("krpcErrorFromE returned nil for a real error")
	}
}

func TestCompactNodeRoundTrip(t *testing.T) {
	id := NodeID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	addr := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 42), Port: 6881}

	encoded := encodeCompactNode(id, addr)
	if len(encoded) != compactNodeSize {
		t.Fatalf("encoded to %d bytes, want %d", len(encoded), compactNodeSize)
	}

	got := parseCompactNodes(encoded)
	if len(got) != 1 {
		t.Fatalf("parsed %d nodes, want 1", len(got))
	}
	if got[0].id != id {
		t.Fatalf("got id %s, want %s", got[0].id, id)
	}
	if !got[0].addr.IP.Equal(addr.IP) || got[0].addr.Port != addr.Port {
		t.Fatalf("got addr %s, want %s", got[0].addr, addr)
	}
}

func TestParseCompactNodesSkipsUnspecifiedEntries(t *testing.T) {
	id := NodeID{1}
	valid := encodeCompactNode(id, &net.UDPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 1})
	zeroed := encodeCompactNode(id, &net.UDPAddr{IP: net.IPv4zero, Port: 1})

	got := parseCompactNodes(append(append([]byte{}, valid...), zeroed...))
	if len(got) != 1 {
		t.Fatalf("got %d nodes, want 1 (the unspecified-IP entry should be skipped)", len(got))
	}
}

func TestParseCompactPeersRoundTrip(t *testing.T) {
	ip := net.IPv4(10, 0, 0, 1)
	encoded := encodeCompactPeer(ip, 6881)
	if len(encoded) != 6 {
		t.Fatalf("encoded to %d bytes, want 6", len(encoded))
	}
	got := parseCompactPeers([][]byte{encoded})
	if len(got) != 1 || !got[0].IP.Equal(ip) || got[0].Port != 6881 {
		t.Fatalf("got %+v", got)
	}
}

func TestCommonPrefixLen(t *testing.T) {
	var a, b NodeID
	a[0] = 0b11110000
	b[0] = 0b11110000
	if got := commonPrefixLen(a, b); got != len(a)*8 {
		t.Fatalf("identical IDs: got %d, want %d", got, len(a)*8)
	}

	b[0] = 0b11100000 // differs at bit index 3 (0-based from the top)
	if got := commonPrefixLen(a, b); got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
}

func TestXorDistanceOrdering(t *testing.T) {
	target := NodeID{0}
	near := NodeID{0, 0, 0, 0, 1}
	far := NodeID{1, 0, 0, 0, 0}
	if !xor(target, near).less(xor(target, far)) {
		t.Fatal("expected the ID differing only in a late byte to be closer than one differing in the first byte")
	}
}

func TestKRPCEnvelopeUnknownFieldsAreIgnored(t *testing.T) {
	// A real-world response often carries extra keys ("ip", "p", "v", ...)
	// this client doesn't model; the decoder must tolerate them rather than
	// erroring the whole message out.
	raw := []byte("d1:rd2:id20:aaaaaaaaaaaaaaaaaaaa2:ip6:xxxxxx1:v4:XX01e1:t2:zz1:y1:re")
	msg, err := parseMessage(raw)
	if err != nil {
		t.Fatalf("parseMessage with unknown fields: %v", err)
	}
	if msg.Y != "r" || msg.T != "zz" {
		t.Fatalf("got y=%q t=%q", msg.Y, msg.T)
	}
	var r idResponse
	if err := bencode.Unmarshal(msg.R, &r); err != nil {
		t.Fatalf("decoding r: %v", err)
	}
	if !reflect.DeepEqual(r.ID[:], []byte("aaaaaaaaaaaaaaaaaaaa")) {
		t.Fatalf("got id %q", r.ID)
	}
}
