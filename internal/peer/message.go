package peer

import (
	"encoding/binary"
	"fmt"
)

type MessageID uint8

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
	MsgPort          MessageID = 9
)

type Message struct {
	ID      MessageID
	Payload []byte
}

func (m *Message) Serialize() []byte {
	if m == nil {

		return make([]byte, 4)
	}
	length := uint32(1 + len(m.Payload))
	buf := make([]byte, 4+length)
	binary.BigEndian.PutUint32(buf[0:4], length)
	buf[4] = byte(m.ID)
	copy(buf[5:], m.Payload)
	return buf
}

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

type MsgHavePayload struct {
	PieceIndex uint32
}

func (p *MsgHavePayload) Parse(payload []byte) error {
	if len(payload) != 4 {
		return fmt.Errorf("have payload must be 4 bytes, got %d", len(payload))
	}
	p.PieceIndex = binary.BigEndian.Uint32(payload)
	return nil
}

func (p *MsgHavePayload) Serialize() []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, p.PieceIndex)
	return buf
}

type MsgRequestPayload struct {
	Index  uint32
	Begin  uint32
	Length uint32
}

func (p *MsgRequestPayload) Parse(payload []byte) error {
	if len(payload) != 12 {
		return fmt.Errorf("request payload must be 12 bytes, got %d", len(payload))
	}
	p.Index = binary.BigEndian.Uint32(payload[0:4])
	p.Begin = binary.BigEndian.Uint32(payload[4:8])
	p.Length = binary.BigEndian.Uint32(payload[8:12])
	return nil
}

func (p *MsgRequestPayload) Serialize() []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], p.Index)
	binary.BigEndian.PutUint32(buf[4:8], p.Begin)
	binary.BigEndian.PutUint32(buf[8:12], p.Length)
	return buf
}

type MsgPiecePayload struct {
	Index uint32
	Begin uint32
	Block []byte
}

func (p *MsgPiecePayload) Parse(payload []byte) error {
	if len(payload) < 8 {
		return fmt.Errorf("piece payload must be at least 8 bytes, got %d", len(payload))
	}
	p.Index = binary.BigEndian.Uint32(payload[0:4])
	p.Begin = binary.BigEndian.Uint32(payload[4:8])
	p.Block = payload[8:]
	return nil
}

func (p *MsgPiecePayload) Serialize() []byte {
	buf := make([]byte, 8+len(p.Block))
	binary.BigEndian.PutUint32(buf[0:4], p.Index)
	binary.BigEndian.PutUint32(buf[4:8], p.Begin)
	copy(buf[8:], p.Block)
	return buf
}

type Bitfield []byte

func (bf Bitfield) HasPiece(index uint32) bool {
	byteIndex := index / 8
	offset := index % 8
	if byteIndex >= uint32(len(bf)) {
		return false
	}
	return (bf[byteIndex]>>(7-offset))&1 != 0
}

func (bf Bitfield) SetPiece(index uint32) {
	byteIndex := index / 8
	offset := index % 8
	if byteIndex >= uint32(len(bf)) {

		return
	}
	bf[byteIndex] |= (1 << (7 - offset))
}

func NewBitfield(numPieces int) Bitfield {
	if numPieces <= 0 {
		return nil
	}
	numBytes := (numPieces + 7) / 8
	return make(Bitfield, numBytes)
}
