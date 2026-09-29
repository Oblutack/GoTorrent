package utp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// PacketType is the 4-bit type field in every µTP packet's header.
type PacketType uint8

const (
	// STData is a regular data packet — the socket is connected and has
	// data to send.
	STData PacketType = 0
	// STFin finalizes the connection — the last packet either side sends.
	STFin PacketType = 1
	// STState carries an ACK with no data.
	STState PacketType = 2
	// STReset forcefully terminates the connection.
	STReset PacketType = 3
	// STSyn initiates a connection.
	STSyn PacketType = 4
)

func (t PacketType) String() string {
	switch t {
	case STData:
		return "ST_DATA"
	case STFin:
		return "ST_FIN"
	case STState:
		return "ST_STATE"
	case STReset:
		return "ST_RESET"
	case STSyn:
		return "ST_SYN"
	default:
		return fmt.Sprintf("ST_UNKNOWN(%d)", uint8(t))
	}
}

const (
	protocolVersion = 1
	headerLen       = 20
	// extSelectiveAck is BEP 29's one defined extension type today.
	extSelectiveAck = 1
	// minSackLen/sackLenMultiple: BEP 29 requires a Selective ACK bitmask
	// to be at least 4 bytes, in multiples of 4.
	minSackLen      = 4
	sackLenMultiple = 4
)

var (
	ErrShortHeader        = errors.New("utp: packet shorter than the fixed 20-byte header")
	ErrUnsupportedVersion = errors.New("utp: unsupported protocol version")
	ErrUnknownType        = errors.New("utp: unknown packet type")
	ErrMalformedExtension = errors.New("utp: malformed extension chain")
	ErrMalformedSACK      = errors.New("utp: malformed selective-ACK extension")
)

// Packet is one µTP datagram: the fixed header, an optional Selective ACK
// bitmask (nil when absent), and the payload (nil/empty for every type but
// ST_DATA in practice, though nothing here enforces that at this layer).
type Packet struct {
	Type          PacketType
	ConnID        uint16
	Timestamp     uint32 // microseconds, this packet's own send time
	TimestampDiff uint32 // microseconds — see doc.go / CLAUDE.md for why this is cached, not recomputed per packet
	WndSize       uint32
	SeqNr         uint16
	AckNr         uint16
	SACK          []byte
	Payload       []byte
}

// Marshal renders p as wire bytes.
func (p *Packet) Marshal() []byte {
	var extBlock []byte
	extByte := byte(0)
	if len(p.SACK) > 0 {
		extByte = extSelectiveAck
		extBlock = make([]byte, 2+len(p.SACK))
		extBlock[0] = 0 // no further extension follows
		extBlock[1] = byte(len(p.SACK))
		copy(extBlock[2:], p.SACK)
	}

	buf := make([]byte, headerLen+len(extBlock)+len(p.Payload))
	buf[0] = byte(p.Type)<<4 | protocolVersion
	buf[1] = extByte
	binary.BigEndian.PutUint16(buf[2:4], p.ConnID)
	binary.BigEndian.PutUint32(buf[4:8], p.Timestamp)
	binary.BigEndian.PutUint32(buf[8:12], p.TimestampDiff)
	binary.BigEndian.PutUint32(buf[12:16], p.WndSize)
	binary.BigEndian.PutUint16(buf[16:18], p.SeqNr)
	binary.BigEndian.PutUint16(buf[18:20], p.AckNr)

	n := headerLen
	if len(extBlock) > 0 {
		n += copy(buf[n:], extBlock)
	}
	copy(buf[n:], p.Payload)
	return buf
}

// Unmarshal parses buf as a µTP packet, walking its full extension chain
// (skipping any extension type this package doesn't understand — BEP 29's
// own forward-compatibility mechanism, not an error) and validating the
// one extension it does interpret, Selective ACK.
func Unmarshal(buf []byte) (*Packet, error) {
	if len(buf) < headerLen {
		return nil, ErrShortHeader
	}
	typeVer := buf[0]
	if typeVer&0x0f != protocolVersion {
		return nil, ErrUnsupportedVersion
	}
	p := &Packet{Type: PacketType(typeVer >> 4)}
	if p.Type > STSyn {
		return nil, ErrUnknownType
	}
	ext := buf[1]
	p.ConnID = binary.BigEndian.Uint16(buf[2:4])
	p.Timestamp = binary.BigEndian.Uint32(buf[4:8])
	p.TimestampDiff = binary.BigEndian.Uint32(buf[8:12])
	p.WndSize = binary.BigEndian.Uint32(buf[12:16])
	p.SeqNr = binary.BigEndian.Uint16(buf[16:18])
	p.AckNr = binary.BigEndian.Uint16(buf[18:20])

	off := headerLen
	for ext != 0 {
		if off+2 > len(buf) {
			return nil, ErrMalformedExtension
		}
		nextExt := buf[off]
		length := int(buf[off+1])
		off += 2
		if off+length > len(buf) {
			return nil, ErrMalformedExtension
		}
		data := buf[off : off+length]
		if ext == extSelectiveAck {
			if length < minSackLen || length%sackLenMultiple != 0 {
				return nil, ErrMalformedSACK
			}
			p.SACK = append([]byte(nil), data...)
		}
		off += length
		ext = nextExt
	}
	p.Payload = append([]byte(nil), buf[off:]...)
	return p, nil
}

// sackBaseOffset is how far past AckNr the Selective ACK bitmask's first
// bit sits — BEP 29's own words: "the first bit in the mask ... represents
// ack_nr + 2" (not +1: a missing ack_nr+1 is *why* a SACK is being sent).
const sackBaseOffset = 2

// sackHasBit reports whether seq is marked received in sack, relative to
// ackNr, using wraparound-safe sequence arithmetic throughout.
func sackHasBit(sack []byte, ackNr, seq uint16) bool {
	base := ackNr + sackBaseOffset
	if seqLess(seq, base) {
		return false
	}
	idx := seqDiff(base, seq)
	byteIdx := idx / 8
	if byteIdx < 0 || int(byteIdx) >= len(sack) {
		return false
	}
	bitIdx := uint(idx % 8)
	return sack[byteIdx]&(1<<bitIdx) != 0
}

// setSackBit marks seq received in sack, relative to ackNr. The caller
// must size sack large enough first (buildSACK below does).
func setSackBit(sack []byte, ackNr, seq uint16) {
	base := ackNr + sackBaseOffset
	idx := seqDiff(base, seq)
	if idx < 0 {
		return
	}
	byteIdx := int(idx / 8)
	if byteIdx >= len(sack) {
		return
	}
	bitIdx := uint(idx % 8)
	sack[byteIdx] |= 1 << bitIdx
}
