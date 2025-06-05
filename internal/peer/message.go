package peer

import (
	"encoding/binary"
	"fmt"
	//"io"
)

// MessageID identifies the type of a peer message.
type MessageID uint8

// Constants for message IDs
const (
	MsgChoke         MessageID = 0
	MsgUnchoke       MessageID = 1
	MsgInterested    MessageID = 2
	MsgNotInterested MessageID = 3
	MsgHave          MessageID = 4
	MsgBitfield      MessageID = 5
	MsgRequest       MessageID = 6
	MsgPiece         MessageID = 7
	MsgCancel        MessageID = 8
	MsgPort          MessageID = 9 // For DHT, not strictly needed for basic client
)

// Message represents a message exchanged between peers after the handshake.
// Format: <length_prefix (4 bytes)><message_id (1 byte)><payload (variable length)>
type Message struct {
	ID      MessageID
	Payload []byte
}

// Serialize converts a Message struct into a byte slice for sending.
// It prepends the length prefix.
func (m *Message) Serialize() []byte {
	if m == nil {
		// Keep-alive message (length prefix of 0)
		return make([]byte, 4) // Just 4 zero bytes
	}
	length := uint32(1 + len(m.Payload)) // 1 byte for ID + payload length
	buf := make([]byte, 4+length)
	binary.BigEndian.PutUint32(buf[0:4], length)
	buf[4] = byte(m.ID)
	copy(buf[5:], m.Payload)
	return buf
}

// String returns a human-readable representation of the message ID.
func (id MessageID) String() string {
	switch id {
	case MsgChoke:
		return "Choke"
	case MsgUnchoke:
		return "Unchoke"
	case MsgInterested:
		return "Interested"
	case MsgNotInterested:
		return "NotInterested"
	case MsgHave:
		return "Have"
	case MsgBitfield:
		return "Bitfield"
	case MsgRequest:
		return "Request"
	case MsgPiece:
		return "Piece"
	case MsgCancel:
		return "Cancel"
	case MsgPort:
		return "Port"
	default:
		return fmt.Sprintf("UnknownMsg(%d)", id)
	}
}

// --- Specific Message Payload Structures (examples) ---

// MsgHavePayload represents the payload for a Have message.
type MsgHavePayload struct {
	PieceIndex uint32
}

// Parse parses the payload into a MsgHavePayload struct.
func (p *MsgHavePayload) Parse(payload []byte) error {
	if len(payload) != 4 {
		return fmt.Errorf("have payload must be 4 bytes, got %d", len(payload))
	}
	p.PieceIndex = binary.BigEndian.Uint32(payload)
	return nil
}

// Serialize converts MsgHavePayload to its byte representation.
func (p *MsgHavePayload) Serialize() []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, p.PieceIndex)
	return buf
}


// MsgRequestPayload represents the payload for a Request message.
type MsgRequestPayload struct {
	Index  uint32 // piece index
	Begin  uint32 // byte offset within the piece
	Length uint32 // length of the requested block (typically 16KB)
}

// Parse parses the payload into a MsgRequestPayload struct.
func (p *MsgRequestPayload) Parse(payload []byte) error {
	if len(payload) != 12 { // 3 * 4 bytes
		return fmt.Errorf("request payload must be 12 bytes, got %d", len(payload))
	}
	p.Index = binary.BigEndian.Uint32(payload[0:4])
	p.Begin = binary.BigEndian.Uint32(payload[4:8])
	p.Length = binary.BigEndian.Uint32(payload[8:12])
	return nil
}

// Serialize converts MsgRequestPayload to its byte representation.
func (p *MsgRequestPayload) Serialize() []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], p.Index)
	binary.BigEndian.PutUint32(buf[4:8], p.Begin)
	binary.BigEndian.PutUint32(buf[8:12], p.Length)
	return buf
}


// MsgPiecePayload represents the payload for a Piece message.
type MsgPiecePayload struct {
	Index uint32 // piece index
	Begin uint32 // byte offset within the piece
	Block []byte // actual block data
}

// Parse parses the payload into a MsgPiecePayload struct.
// Note: The block data is a reference to the original payload slice.
func (p *MsgPiecePayload) Parse(payload []byte) error {
	if len(payload) < 8 { // 4 bytes for Index + 4 bytes for Begin
		return fmt.Errorf("piece payload must be at least 8 bytes, got %d", len(payload))
	}
	p.Index = binary.BigEndian.Uint32(payload[0:4])
	p.Begin = binary.BigEndian.Uint32(payload[4:8])
	p.Block = payload[8:]
	return nil
}

// Serialize converts MsgPiecePayload to its byte representation.
func (p *MsgPiecePayload) Serialize() []byte {
	buf := make([]byte, 8+len(p.Block))
	binary.BigEndian.PutUint32(buf[0:4], p.Index)
	binary.BigEndian.PutUint32(buf[4:8], p.Begin)
	copy(buf[8:], p.Block)
	return buf
}

// TODO: Add structures and Parse/Serialize methods for MsgBitfield, MsgCancel if needed.
// MsgChoke, MsgUnchoke, MsgInterested, MsgNotInterested have no payload.

type Bitfield []byte

// HasPiece checks if the bitfield indicates possession of a piece at a given index.
func (bf Bitfield) HasPiece(index uint32) bool {
	byteIndex := index / 8
	offset := index % 8
	if byteIndex >= uint32(len(bf)) {
		return false // Index out of bounds
	}
	return (bf[byteIndex]>>(7-offset))&1 != 0
}

// SetPiece marks a piece as possessed in the bitfield.
// Note: This modifies the bitfield in place.
// The bitfield should be pre-allocated to the correct size.
func (bf Bitfield) SetPiece(index uint32) {
	byteIndex := index / 8
	offset := index % 8
	if byteIndex >= uint32(len(bf)) {
		// This should not happen if bitfield is correctly sized.
		// Can't set piece if index is out of bounds.
		return
	}
	bf[byteIndex] |= (1 << (7 - offset))
}

// NewBitfield creates a new bitfield of the correct size for a given number of pieces.
// Initially, all pieces are marked as not possessed.
func NewBitfield(numPieces int) Bitfield {
    if numPieces <= 0 {
        return nil
    }
	numBytes := (numPieces + 7) / 8 // Ceiling division
	return make(Bitfield, numBytes)
}