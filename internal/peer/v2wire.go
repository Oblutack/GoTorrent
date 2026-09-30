package peer

import (
	"encoding/binary"
	"fmt"
)

// BEP 52's three new top-level message IDs — plain message-type bytes,
// like every other message in this protocol, not routed through BEP 10's
// extension dict the way ut_metadata/ut_pex are. 21-23 are unused by any
// earlier BEP this codebase implements (0-9, 13-17, 20 are all already
// spoken for — see message.go).
const (
	MsgHashRequest MessageID = 21
	MsgHashes      MessageID = 22
	MsgHashReject  MessageID = 23
)

// v2ReservedByte/v2ReservedBit mark support for BEP 52's hash-exchange
// messages in the handshake's reserved bytes — "the 4th most significant
// bit in the last byte," i.e. byte 7 (0-indexed, the same byte the Fast
// extension's own reserved bit already lives in — byte7Reserved.go's
// fastReservedByte/fastReservedBit, bit 0x04) counting from the most
// significant bit: 0x80 is 1st, 0x40 2nd, 0x20 3rd, 0x10 4th. No collision
// with Fast's own bit in that same byte.
const (
	v2ReservedByte = 7
	v2ReservedBit  = 0x10
)

// hashPayloadFixedLen is every field before the variable-length hashes
// list: pieces_root (32, BEP 52's own SHA-256 digest size) plus
// base_layer/index/length/proof_layers, each a 4-byte big-endian integer —
// this protocol's universal integer encoding (the spec's own words:
// "All later integers sent in the protocol are encoded as four bytes
// big-endian"), applied here since BEP 52's prose gives the field order
// ("a pieces root, base layer, index, length, and proof layers") but no
// explicit byte-offset table the way request/piece's fields don't either.
const hashPayloadFixedLen = 32 + 4 + 4 + 4 + 4

// MsgHashRequestPayload is 'hash request' (and, with identical fields,
// 'hash reject') message's payload — see BEP 52's peer-messages section
// for what each field means; internal/torrent is where the logic that
// decides what to request/serve/reject actually lives, this package is
// the wire format alone.
type MsgHashRequestPayload struct {
	PiecesRoot  [32]byte
	BaseLayer   uint32
	Index       uint32
	Length      uint32
	ProofLayers uint32
}

func (p *MsgHashRequestPayload) Parse(payload []byte) error {
	if len(payload) != hashPayloadFixedLen {
		return fmt.Errorf("hash request/reject payload must be %d bytes, got %d", hashPayloadFixedLen, len(payload))
	}
	copy(p.PiecesRoot[:], payload[0:32])
	p.BaseLayer = binary.BigEndian.Uint32(payload[32:36])
	p.Index = binary.BigEndian.Uint32(payload[36:40])
	p.Length = binary.BigEndian.Uint32(payload[40:44])
	p.ProofLayers = binary.BigEndian.Uint32(payload[44:48])
	return nil
}

func (p *MsgHashRequestPayload) Serialize() []byte {
	buf := make([]byte, hashPayloadFixedLen)
	copy(buf[0:32], p.PiecesRoot[:])
	binary.BigEndian.PutUint32(buf[32:36], p.BaseLayer)
	binary.BigEndian.PutUint32(buf[36:40], p.Index)
	binary.BigEndian.PutUint32(buf[40:44], p.Length)
	binary.BigEndian.PutUint32(buf[44:48], p.ProofLayers)
	return buf
}

// MsgHashesPayload is the 'hashes' message: a 'hash request”s own fields
// (correlating the reply with the request it answers) plus the actual
// hash values, each 32 bytes, concatenated — "hashes starts with the base
// layer and ends with the uncle hash closest to the root," per BEP 52's
// own wording; internal/torrent is what actually builds/consumes this
// list correctly (proof-layer omission included), this type is just the
// wire container.
type MsgHashesPayload struct {
	PiecesRoot  [32]byte
	BaseLayer   uint32
	Index       uint32
	Length      uint32
	ProofLayers uint32
	Hashes      [][32]byte
}

func (p *MsgHashesPayload) Parse(payload []byte) error {
	if len(payload) < hashPayloadFixedLen {
		return fmt.Errorf("hashes payload must be at least %d bytes, got %d", hashPayloadFixedLen, len(payload))
	}
	copy(p.PiecesRoot[:], payload[0:32])
	p.BaseLayer = binary.BigEndian.Uint32(payload[32:36])
	p.Index = binary.BigEndian.Uint32(payload[36:40])
	p.Length = binary.BigEndian.Uint32(payload[40:44])
	p.ProofLayers = binary.BigEndian.Uint32(payload[44:48])

	rest := payload[hashPayloadFixedLen:]
	if len(rest)%32 != 0 {
		return fmt.Errorf("hashes payload's hash list is %d bytes, not a multiple of 32", len(rest))
	}
	p.Hashes = make([][32]byte, len(rest)/32)
	for i := range p.Hashes {
		copy(p.Hashes[i][:], rest[i*32:(i+1)*32])
	}
	return nil
}

func (p *MsgHashesPayload) Serialize() []byte {
	buf := make([]byte, hashPayloadFixedLen+32*len(p.Hashes))
	copy(buf[0:32], p.PiecesRoot[:])
	binary.BigEndian.PutUint32(buf[32:36], p.BaseLayer)
	binary.BigEndian.PutUint32(buf[36:40], p.Index)
	binary.BigEndian.PutUint32(buf[40:44], p.Length)
	binary.BigEndian.PutUint32(buf[44:48], p.ProofLayers)
	for i, h := range p.Hashes {
		copy(buf[hashPayloadFixedLen+i*32:hashPayloadFixedLen+(i+1)*32], h[:])
	}
	return buf
}

// SupportsV2Hashes reports whether the peer's handshake advertised BEP 52
// hash-exchange support — mirrors fastReservedByte's own
// SupportsFast-equivalent accessor shape (see fast.go).
func (h *Handshake) SupportsV2Hashes() bool {
	return h.Reserved[v2ReservedByte]&v2ReservedBit != 0
}

// SupportsV2Hashes reports whether this connection's remote peer
// advertised BEP 52 hash-exchange support in its own handshake — mirrors
// SupportsUtHolepunch's own shape (holepunch.go).
func (c *Client) SupportsV2Hashes() bool { return c.peerSupportsV2Hashes }
