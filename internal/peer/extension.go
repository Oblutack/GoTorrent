package peer

import (
	"encoding/binary"
	"fmt"
	"net"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// extensionReservedByte / extensionReservedBit mark support for the BEP 10
// extension protocol in the handshake's reserved field: bit 20 counting from
// the right across all 8 bytes, i.e. bit 0x10 of byte 5 (0-indexed).
const (
	extensionReservedByte = 5
	extensionReservedBit  = 0x10
)

// localUtMetadataID is the id this client always advertises for ut_metadata
// in its own extended handshake's "m" dict. A peer wanting to send us a
// ut_metadata message uses this id as the wire byte.
const localUtMetadataID = 1

// localUtPexID is ut_pex's counterpart to localUtMetadataID — the id this
// client always advertises for BEP 11 peer exchange in its own extended
// handshake's "m" dict.
const localUtPexID = 2

// MetadataPieceSize is BEP 9's fixed chunk size for info-dictionary
// transfer, same as a regular block. Exported so a caller assembling a
// fetch (internal/torrent) can compute how many pieces a given
// metadata_size implies.
const MetadataPieceSize = 16 * 1024

// maxMetadataSize bounds a peer's claimed metadata_size, so a lie doesn't
// drive an absurd allocation before we ever get to verify anything against
// the infohash. Matches metainfo.MaxTorrentFileSize's spirit — an info
// dictionary is always smaller than the .torrent file containing it.
const maxMetadataSize = 32 << 20

// ut_metadata message types (BEP 9).
const (
	utMetadataRequest = 0
	utMetadataData    = 1
	utMetadataReject  = 2
)

// extHandshakeWire is the extended handshake's bencoded body (BEP 10).
type extHandshakeWire struct {
	M            map[string]int `bencode:"m"`
	MetadataSize int            `bencode:"metadata_size,omitempty"`
	V            string         `bencode:"v,omitempty"`
}

// utMetadataWire is the small bencoded header in front of every ut_metadata
// message (BEP 9); a "data" message has the requested bytes appended
// immediately after it, outside the bencoding.
type utMetadataWire struct {
	MsgType   int `bencode:"msg_type"`
	Piece     int `bencode:"piece"`
	TotalSize int `bencode:"total_size,omitempty"`
}

// MetadataPiece is one 16 KiB chunk of the info dictionary, received via
// BEP 9. Delivered on Client.MetadataPieces the same way a regular block
// arrives on Results — data-bearing, so it gets its own channel rather than
// riding on the lightweight Events one.
type MetadataPiece struct {
	Piece     int
	TotalSize int
	Data      []byte
}

// sendExtendedHandshake tells the peer which extensions we support. Callers
// must already know the peer negotiated the extension protocol
// (peerSupportsExt) — sending this to a peer that never advertised the
// reserved bit is a protocol violation most peers would simply ignore, but
// there's no reason to find out.
func (c *Client) sendExtendedHandshake() error {
	hs := extHandshakeWire{M: map[string]int{"ut_metadata": localUtMetadataID, "ut_pex": localUtPexID}}
	if c.metadataBytes != nil {
		if b := c.metadataBytes(); b != nil {
			hs.MetadataSize = len(b)
		}
	}
	return c.sendExtendedMessage(0, hs, nil)
}

// SupportsUtMetadata reports whether the peer's extended handshake (already
// received) advertised ut_metadata support.
func (c *Client) SupportsUtMetadata() bool { return c.peerUtMetadataID.Load() != 0 }

// PeerMetadataSize is the metadata_size the peer's extended handshake
// advertised, or 0 if it hasn't sent one (or hasn't handshaken at all yet).
func (c *Client) PeerMetadataSize() int { return int(c.peerMetadataSize.Load()) }

// SendMetadataRequest asks the peer for one 16 KiB piece of the info
// dictionary. It fails if the peer hasn't told us it supports ut_metadata.
func (c *Client) SendMetadataRequest(piece int) error {
	id := int(c.peerUtMetadataID.Load())
	if id == 0 {
		return fmt.Errorf("peer %s does not support ut_metadata", c.Conn.RemoteAddr())
	}
	return c.sendExtendedMessage(id, utMetadataWire{MsgType: utMetadataRequest, Piece: piece}, nil)
}

// sendExtendedMessage bencodes header and sends it as an extended message to
// peerExtID (the id *the peer* assigned this extension in their own
// handshake — BEP 10 ids are chosen independently by each side), with
// trailing appended verbatim after the bencoded body.
func (c *Client) sendExtendedMessage(peerExtID int, header any, trailing []byte) error {
	body, err := bencode.Marshal(header)
	if err != nil {
		return fmt.Errorf("encoding extended message: %w", err)
	}
	payload := make([]byte, 1+len(body)+len(trailing))
	payload[0] = byte(peerExtID)
	n := copy(payload[1:], body)
	copy(payload[1+n:], trailing)
	return c.SendMessage(MsgExtended, payload)
}

// handleExtendedHandshake processes an incoming extended-message-id 0
// (BEP 10's handshake), recording the peer's ut_metadata id and advertised
// metadata size if present.
func (c *Client) handleExtendedHandshake(body []byte) error {
	var hs extHandshakeWire
	if err := bencode.Unmarshal(body, &hs); err != nil {
		return fmt.Errorf("malformed extended handshake: %w", err)
	}
	if id, ok := hs.M["ut_metadata"]; ok && id > 0 && id <= 255 {
		c.peerUtMetadataID.Store(int32(id))
	}
	if hs.MetadataSize > 0 && hs.MetadataSize <= maxMetadataSize {
		c.peerMetadataSize.Store(int64(hs.MetadataSize))
	}
	if id, ok := hs.M["ut_pex"]; ok && id > 0 && id <= 255 {
		c.peerUtPexID.Store(int32(id))
	}
	c.notify(Event{Kind: EventExtendedHandshake})
	return nil
}

// handleUtMetadataMessage processes an incoming message addressed to our
// locally-advertised ut_metadata id.
func (c *Client) handleUtMetadataMessage(body []byte) error {
	var raw bencode.RawMessage
	if err := bencode.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("malformed ut_metadata message: %w", err)
	}
	var hdr utMetadataWire
	if err := bencode.Unmarshal(raw, &hdr); err != nil {
		return fmt.Errorf("malformed ut_metadata header: %w", err)
	}

	switch hdr.MsgType {
	case utMetadataData:
		data := body[len(raw):]
		select {
		case c.MetadataPieces <- MetadataPiece{Piece: hdr.Piece, TotalSize: hdr.TotalSize, Data: append([]byte(nil), data...)}:
		case <-c.done:
		}

	case utMetadataReject:
		logger.Logf("Peer %s: rejected metadata piece %d\n", c.Conn.RemoteAddr(), hdr.Piece)
		c.notify(Event{Kind: EventMetadataReject, PieceIndex: uint32(hdr.Piece)})

	case utMetadataRequest:
		c.serveMetadataRequest(hdr.Piece)
	}
	return nil
}

// serveMetadataRequest answers a peer's ut_metadata request if we have the
// metadata to serve. hdr.Piece is not bounds-checked against a piece count
// we don't necessarily know here — an out-of-range offset just yields an
// empty slice, which the peer's own reassembly will reject as short, and a
// negative piece is turned away as a reject like any other unservable one.
func (c *Client) serveMetadataRequest(piece int) {
	var data []byte
	if c.metadataBytes != nil {
		data = c.metadataBytes()
	}
	if data == nil || piece < 0 {
		c.sendUtMetadataReject(piece)
		return
	}
	start := piece * MetadataPieceSize
	if start >= len(data) {
		c.sendUtMetadataReject(piece)
		return
	}
	end := start + MetadataPieceSize
	if end > len(data) {
		end = len(data)
	}

	id := int(c.peerUtMetadataID.Load())
	if id == 0 {
		return // they asked for metadata without ever completing the extended handshake; nothing to answer with
	}
	if err := c.sendExtendedMessage(id, utMetadataWire{MsgType: utMetadataData, Piece: piece, TotalSize: len(data)}, data[start:end]); err != nil {
		logger.Warning.Printf("Peer %s: failed to send metadata piece %d: %v\n", c.Conn.RemoteAddr(), piece, err)
	}
}

func (c *Client) sendUtMetadataReject(piece int) {
	id := int(c.peerUtMetadataID.Load())
	if id == 0 {
		return
	}
	if err := c.sendExtendedMessage(id, utMetadataWire{MsgType: utMetadataReject, Piece: piece}, nil); err != nil {
		logger.Logf("Peer %s: failed to send metadata reject for piece %d: %v\n", c.Conn.RemoteAddr(), piece, err)
	}
}

// --- ut_pex (BEP 11) --------------------------------------------------

// utPexWire is ut_pex's bencoded body. Only the IPv4 fields are supported —
// "added6"/"added6.f"/"dropped6" (the IPv6 variants) are not, matching the
// IPv6 gap already documented for the UDP tracker (2.4) and the DHT (2.5).
// "added.f" (per-peer flag bytes — encryption/seed hints) is accepted on
// receive for forward compatibility but never populated on send: this
// client has nothing meaningful to put in it yet.
type utPexWire struct {
	Added   []byte `bencode:"added,omitempty"`
	AddedF  []byte `bencode:"added.f,omitempty"`
	Dropped []byte `bencode:"dropped,omitempty"`
}

// PEXUpdate is one BEP 11 message: peers the sender has connected to or
// dropped since its last PEX message. Delivered on Client.PEXUpdates.
type PEXUpdate struct {
	Added   []tracker.PeerInfo
	Dropped []tracker.PeerInfo
}

// SupportsUtPex reports whether the peer's extended handshake (already
// received) advertised ut_pex support.
func (c *Client) SupportsUtPex() bool { return c.peerUtPexID.Load() != 0 }

// SendPEX sends one BEP 11 update. It is a no-op (not an error) if the peer
// never advertised ut_pex support, so callers can broadcast to every
// connected peer without checking SupportsUtPex themselves first.
func (c *Client) SendPEX(added, dropped []tracker.PeerInfo) error {
	id := int(c.peerUtPexID.Load())
	if id == 0 {
		return nil
	}
	wire := utPexWire{Added: encodeCompactPeerList(added), Dropped: encodeCompactPeerList(dropped)}
	return c.sendExtendedMessage(id, wire, nil)
}

func (c *Client) handleUtPexMessage(body []byte) error {
	var wire utPexWire
	if err := bencode.Unmarshal(body, &wire); err != nil {
		return fmt.Errorf("malformed ut_pex message: %w", err)
	}
	update := PEXUpdate{Added: decodeCompactPeerList(wire.Added), Dropped: decodeCompactPeerList(wire.Dropped)}
	if len(update.Added) == 0 && len(update.Dropped) == 0 {
		return nil
	}
	select {
	case c.PEXUpdates <- update:
	case <-c.done:
	}
	return nil
}

// decodeCompactPeerList and encodeCompactPeerList handle ut_pex's "added"/
// "dropped" fields: the same packed 6-byte-per-peer (4-byte IPv4 + 2-byte
// big-endian port) layout BEP 23 uses for a tracker's compact peer list.
// Written locally rather than shared with internal/tracker or internal/dht,
// which each have their own equally small copy — three near-identical
// four-line loops across independent packages is cheaper than the coupling
// a shared helper would add.
func decodeCompactPeerList(raw []byte) []tracker.PeerInfo {
	const entry = 6
	out := make([]tracker.PeerInfo, 0, len(raw)/entry)
	for off := 0; off+entry <= len(raw); off += entry {
		ip := make(net.IP, 4)
		copy(ip, raw[off:off+4])
		port := binary.BigEndian.Uint16(raw[off+4 : off+6])
		if port == 0 || ip.IsUnspecified() {
			continue
		}
		out = append(out, tracker.PeerInfo{IP: ip, Port: port})
	}
	return out
}

func encodeCompactPeerList(peers []tracker.PeerInfo) []byte {
	buf := make([]byte, 0, 6*len(peers))
	for _, p := range peers {
		ip4 := p.IP.To4()
		if ip4 == nil {
			continue // IPv6 not supported yet, see utPexWire's doc comment
		}
		entry := make([]byte, 6)
		copy(entry[0:4], ip4)
		binary.BigEndian.PutUint16(entry[4:6], p.Port)
		buf = append(buf, entry...)
	}
	return buf
}
