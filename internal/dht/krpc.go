package dht

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// message is the outer KRPC envelope (BEP 5): every query, response, and
// error shares this shape, differing only in which of q/a/r/e is present. A
// and R are captured raw (bencode.RawMessage) because their shape depends on
// which query this is — the caller decodes them into the right *Args or
// *Response type once it knows q, or once it already knows what it asked
// for.
type message struct {
	T string             `bencode:"t"`
	Y string             `bencode:"y"`
	Q string             `bencode:"q,omitempty"`
	A bencode.RawMessage `bencode:"a,omitempty"`
	R bencode.RawMessage `bencode:"r,omitempty"`
	// E is [code, message] per BEP 5 — a two-element heterogeneous list, so
	// it decodes into []any rather than a dedicated struct.
	E []any `bencode:"e,omitempty"`
}

type pingArgs struct {
	ID NodeID `bencode:"id"`
}

type findNodeArgs struct {
	ID     NodeID `bencode:"id"`
	Target NodeID `bencode:"target"`
}

type getPeersArgs struct {
	ID       NodeID `bencode:"id"`
	InfoHash NodeID `bencode:"info_hash"`
}

type announcePeerArgs struct {
	ID          NodeID `bencode:"id"`
	ImpliedPort int64  `bencode:"implied_port,omitempty"`
	InfoHash    NodeID `bencode:"info_hash"`
	Port        int64  `bencode:"port"`
	Token       string `bencode:"token"`
}

// idResponse is the reply shape for ping and announce_peer: nothing but the
// responder's own ID.
type idResponse struct {
	ID NodeID `bencode:"id"`
}

type findNodeResponse struct {
	ID    NodeID `bencode:"id"`
	Nodes []byte `bencode:"nodes"`
}

type getPeersResponse struct {
	ID     NodeID   `bencode:"id"`
	Token  string   `bencode:"token"`
	Nodes  []byte   `bencode:"nodes,omitempty"`
	Values [][]byte `bencode:"values,omitempty"`
}

func newQuery(t, q string, args any) ([]byte, error) {
	argBytes, err := bencode.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("dht: encoding %s args: %w", q, err)
	}
	return bencode.Marshal(message{T: t, Y: "q", Q: q, A: argBytes})
}

func newResponse(t string, r any) ([]byte, error) {
	rBytes, err := bencode.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("dht: encoding response: %w", err)
	}
	return bencode.Marshal(message{T: t, Y: "r", R: rBytes})
}

func newKRPCError(t string, code int, msg string) ([]byte, error) {
	return bencode.Marshal(message{T: t, Y: "e", E: []any{int64(code), msg}})
}

func parseMessage(data []byte) (*message, error) {
	var m message
	if err := bencode.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// krpcErrorFromE turns an "e" list into a Go error.
func krpcErrorFromE(e []any) error {
	if len(e) == 0 {
		return errors.New("dht: peer returned an empty error")
	}
	code, _ := e[0].(int64)
	msg := ""
	if len(e) > 1 {
		msg, _ = e[1].(string)
	}
	return fmt.Errorf("dht: peer error %d: %s", code, msg)
}

// compactNodeSize is one entry in a "nodes" byte string: a 20-byte NodeID
// followed by a 4-byte IPv4 address and a 2-byte big-endian port. The DHT is
// IPv4-only for now — see the package doc on BEP 32 as a known gap.
const compactNodeSize = 26

func parseCompactNodes(raw []byte) []nodeAddr {
	if len(raw)%compactNodeSize != 0 {
		// A malformed "nodes" field is not worth failing the whole response
		// over — just take however many whole entries fit.
		raw = raw[:len(raw)-len(raw)%compactNodeSize]
	}
	out := make([]nodeAddr, 0, len(raw)/compactNodeSize)
	for off := 0; off+compactNodeSize <= len(raw); off += compactNodeSize {
		var id NodeID
		copy(id[:], raw[off:off+20])
		ip := make(net.IP, 4)
		copy(ip, raw[off+20:off+24])
		port := binary.BigEndian.Uint16(raw[off+24 : off+26])
		if port == 0 || ip.IsUnspecified() {
			continue
		}
		out = append(out, nodeAddr{id: id, addr: &net.UDPAddr{IP: ip, Port: int(port)}})
	}
	return out
}

func encodeCompactNode(id NodeID, addr *net.UDPAddr) []byte {
	buf := make([]byte, compactNodeSize)
	copy(buf[0:20], id[:])
	ip4 := addr.IP.To4()
	copy(buf[20:24], ip4)
	binary.BigEndian.PutUint16(buf[24:26], uint16(addr.Port))
	return buf
}

func encodeCompactNodes(nodes []nodeAddr) []byte {
	buf := make([]byte, 0, compactNodeSize*len(nodes))
	for _, n := range nodes {
		buf = append(buf, encodeCompactNode(n.id, n.addr)...)
	}
	return buf
}

// parseCompactPeers decodes get_peers' "values": a list of 6-byte strings,
// each a packed IPv4 address and port (the same layout BEP 23 uses for a
// tracker's compact peer list).
func parseCompactPeers(values [][]byte) []tracker.PeerInfo {
	out := make([]tracker.PeerInfo, 0, len(values))
	for _, v := range values {
		if len(v) != 6 {
			continue
		}
		ip := make(net.IP, 4)
		copy(ip, v[0:4])
		port := binary.BigEndian.Uint16(v[4:6])
		if port == 0 || ip.IsUnspecified() {
			continue
		}
		out = append(out, tracker.PeerInfo{IP: ip, Port: port})
	}
	return out
}

func encodeCompactPeer(ip net.IP, port uint16) []byte {
	buf := make([]byte, 6)
	ip4 := ip.To4()
	copy(buf[0:4], ip4)
	binary.BigEndian.PutUint16(buf[4:6], port)
	return buf
}
