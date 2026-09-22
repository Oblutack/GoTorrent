package peer

import (
	"encoding/binary"
	"fmt"
	"net"
)

// Holepunch message types (BEP 55).
const (
	HolepunchRendezvous uint8 = 0
	HolepunchConnect    uint8 = 1
	HolepunchError      uint8 = 2
)

// Holepunch error codes (BEP 55) — only meaningful when Type ==
// HolepunchError; 0 in every other message, per spec.
const (
	HolepunchErrNoSuchPeer   uint32 = 1 // the address in the rendezvous request is not a usable endpoint at all (unspecified IP or port 0)
	HolepunchErrNotConnected uint32 = 2 // the relay is not currently connected to that address
	HolepunchErrNoSupport    uint32 = 3 // the target peer never advertised ut_holepunch
	HolepunchErrNoSelf       uint32 = 4 // the address names the relay's own listening endpoint
)

// HolepunchErrorString names an error code for logging. An unrecognized
// code (a newer peer using one this client doesn't know about yet) prints
// as its raw number rather than being treated as a protocol violation —
// BEP 10's whole point is that extensions can grow without breaking older
// implementations.
func HolepunchErrorString(code uint32) string {
	switch code {
	case HolepunchErrNoSuchPeer:
		return "no such peer"
	case HolepunchErrNotConnected:
		return "not connected"
	case HolepunchErrNoSupport:
		return "no support"
	case HolepunchErrNoSelf:
		return "no self"
	default:
		return fmt.Sprintf("unknown error %d", code)
	}
}

const (
	holepunchAddrIPv4 = 0
	holepunchAddrIPv6 = 1
)

// HolepunchMessage is BEP 55's ut_holepunch payload. Like BEP 21's
// upload_only, this is a fixed raw binary layout, not bencoded — the wire
// format is exactly:
//
//	msg_type   (1 byte)
//	addr_type  (1 byte)   0x00 = IPv4, 0x01 = IPv6
//	addr       (4 or 16 bytes, matching addr_type)
//	port       (2 bytes, big-endian)
//	err_code   (4 bytes, big-endian; 0 outside a HolepunchError message)
type HolepunchMessage struct {
	Type    uint8
	Addr    net.IP
	Port    uint16
	ErrCode uint32
}

// marshalHolepunch encodes m to the wire format described on
// HolepunchMessage. IPv4 and IPv6 are both supported — unlike this
// project's usual documented IPv6 gap elsewhere (DHT, UDP trackers,
// ut_pex), the format itself makes no IPv4 assumption anywhere, so there
// is no reduced-scope shortcut to take here.
func marshalHolepunch(m HolepunchMessage) ([]byte, error) {
	var addrType byte
	var addrBytes []byte
	if ip4 := m.Addr.To4(); ip4 != nil {
		addrType, addrBytes = holepunchAddrIPv4, ip4
	} else if ip16 := m.Addr.To16(); ip16 != nil {
		addrType, addrBytes = holepunchAddrIPv6, ip16
	} else {
		return nil, fmt.Errorf("holepunch: invalid address %v", m.Addr)
	}

	buf := make([]byte, 1+1+len(addrBytes)+2+4)
	buf[0] = m.Type
	buf[1] = addrType
	copy(buf[2:], addrBytes)
	off := 2 + len(addrBytes)
	binary.BigEndian.PutUint16(buf[off:], m.Port)
	binary.BigEndian.PutUint32(buf[off+2:], m.ErrCode)
	return buf, nil
}

// unmarshalHolepunch decodes a raw ut_holepunch payload. The format is
// fixed-size per addr_type, not self-describing like bencode, so any
// length other than exactly what that addr_type implies is malformed —
// there is no such thing as a valid short or long message to salvage
// part of.
func unmarshalHolepunch(body []byte) (HolepunchMessage, error) {
	if len(body) < 2 {
		return HolepunchMessage{}, fmt.Errorf("holepunch: message too short (%d bytes)", len(body))
	}
	msgType, addrType := body[0], body[1]
	var addrLen int
	switch addrType {
	case holepunchAddrIPv4:
		addrLen = 4
	case holepunchAddrIPv6:
		addrLen = 16
	default:
		return HolepunchMessage{}, fmt.Errorf("holepunch: unknown addr_type %d", addrType)
	}
	want := 2 + addrLen + 2 + 4
	if len(body) != want {
		return HolepunchMessage{}, fmt.Errorf("holepunch: message is %d bytes, want %d for addr_type %d", len(body), want, addrType)
	}

	ip := make(net.IP, addrLen)
	copy(ip, body[2:2+addrLen])
	off := 2 + addrLen
	port := binary.BigEndian.Uint16(body[off:])
	errCode := binary.BigEndian.Uint32(body[off+2:])
	return HolepunchMessage{Type: msgType, Addr: ip, Port: port, ErrCode: errCode}, nil
}

// SupportsUtHolepunch reports whether the peer's extended handshake
// (already received) advertised ut_holepunch support.
func (c *Client) SupportsUtHolepunch() bool { return c.peerUtHolepunchID.Load() != 0 }

// SendHolepunch sends one BEP 55 ut_holepunch message. It is a no-op (not
// an error) if the peer never advertised ut_holepunch support, the same
// "callers don't need to check Supports* themselves first" shape
// SendPEX/SendUploadOnly already follow.
func (c *Client) SendHolepunch(msgType uint8, addr net.IP, port uint16, errCode uint32) error {
	id := int(c.peerUtHolepunchID.Load())
	if id == 0 {
		return nil
	}
	raw, err := marshalHolepunch(HolepunchMessage{Type: msgType, Addr: addr, Port: port, ErrCode: errCode})
	if err != nil {
		return err
	}
	return c.sendExtendedRawMessage(id, raw)
}

// handleHolepunchMessage processes an incoming message addressed to our
// locally-advertised ut_holepunch id, delivering it on HolepunchMessages
// for the owner (internal/torrent) to act on — relaying a rendezvous,
// dialing a connect, or logging an error. What happens next is entirely a
// torrent-actor concern (it needs the connected-peers map and pending-
// request bookkeeping this package has no business owning), so this
// package's job ends at "parse it correctly and hand it off."
func (c *Client) handleHolepunchMessage(body []byte) error {
	msg, err := unmarshalHolepunch(body)
	if err != nil {
		return fmt.Errorf("malformed ut_holepunch message: %w", err)
	}
	select {
	case c.HolepunchMessages <- msg:
	case <-c.done:
	}
	return nil
}
