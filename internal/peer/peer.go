package peer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"net"
	"strconv"
	"time"

	"github.com/Oblutack/GoTorrent/internal/tracker"
)

const (
	ProtocolString    = "BitTorrent protocol"
	protocolStringLen = byte(len(ProtocolString))
	handshakeTimeout  = 10 * time.Second
	readTimeout       = 5 * time.Second
)

// Handshake represents the initial handshake message.
type Handshake struct {
	Pstrlen  byte
	Pstr     [19]byte
	Reserved [8]byte
	InfoHash [20]byte
	PeerID   [20]byte
}

func NewHandshake(infoHash, peerID [20]byte) *Handshake {
	hs := &Handshake{
		Pstrlen:  protocolStringLen,
		InfoHash: infoHash,
		PeerID:   peerID,
	}
	copy(hs.Pstr[:], ProtocolString)
	return hs
}

func (h *Handshake) Serialize() []byte {
	buf := make([]byte, 1+len(h.Pstr)+len(h.Reserved)+len(h.InfoHash)+len(h.PeerID))
	buf[0] = h.Pstrlen
	curr := 1
	curr += copy(buf[curr:], h.Pstr[:])
	curr += copy(buf[curr:], h.Reserved[:])
	curr += copy(buf[curr:], h.InfoHash[:])
	curr += copy(buf[curr:], h.PeerID[:])
	return buf
}

func ReadHandshake(r io.Reader) (*Handshake, error) {
	handshakeBytes := make([]byte, 68)
	if conn, ok := r.(net.Conn); ok {
		conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
		defer conn.SetReadDeadline(time.Time{})
	}
	_, err := io.ReadFull(r, handshakeBytes)
	if err != nil {
		return nil, fmt.Errorf("peer: failed to read handshake: %w", err)
	}
	hs := &Handshake{}
	hs.Pstrlen = handshakeBytes[0]
	if hs.Pstrlen != protocolStringLen {
		return nil, fmt.Errorf("peer: invalid pstrlen %d, expected %d", hs.Pstrlen, protocolStringLen)
	}
	curr := 1
	copy(hs.Pstr[:], handshakeBytes[curr:curr+19])
	curr += 19
	copy(hs.Reserved[:], handshakeBytes[curr:curr+8])
	curr += 8
	copy(hs.InfoHash[:], handshakeBytes[curr:curr+20])
	curr += 20
	copy(hs.PeerID[:], handshakeBytes[curr:curr+20])
	if string(hs.Pstr[:]) != ProtocolString {
		return nil, fmt.Errorf("peer: invalid pstr '%s', expected '%s'", string(hs.Pstr[:]), ProtocolString)
	}
	return hs, nil
}

type BlockRequest struct {
	Index  uint32
	Begin  uint32
	Length uint32
}

type PieceBlock struct {
	Index uint32
	Begin uint32
	Block []byte
}

// Client represents a connection to a single BitTorrent peer.
type Client struct {
	Conn               net.Conn
	InfoHash           [20]byte
	OurID              [20]byte
	RemoteID           [20]byte
	Choked             bool
	Bitfield           Bitfield
	numPiecesInTorrent int
	WorkQueue          chan *BlockRequest
	Results            chan *PieceBlock

	// Dependencies injected from the session for seeding
	ourBitfield       Bitfield
	readBlockFromDisk func(index, begin, length uint32) ([]byte, error)
}

// NewClient attempts to connect to a peer and perform a handshake.
func NewClient(peerInfo tracker.PeerInfo, infoHash, ourID [20]byte, numPiecesInTorrent int, ourBitfield Bitfield, readBlockFunc func(index, begin, length uint32) ([]byte, error)) (*Client, error) {
	address := net.JoinHostPort(peerInfo.IP.String(), strconv.Itoa(int(peerInfo.Port)))
	logger.Logf("peer: attempting to connect to %s", address)
	conn, err := net.DialTimeout("tcp", address, handshakeTimeout)
	if err != nil {
		return nil, fmt.Errorf("peer: failed to dial %s: %w", address, err)
	}

	ourHandshake := NewHandshake(infoHash, ourID)
	_, err = conn.Write(ourHandshake.Serialize())
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("peer: failed to send handshake to %s: %w", address, err)
	}

	peerHandshake, err := ReadHandshake(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("peer: failed to read handshake from %s: %w", address, err)
	}

	if !bytes.Equal(peerHandshake.InfoHash[:], infoHash[:]) {
		conn.Close()
		return nil, fmt.Errorf("peer: handshake InfoHash mismatch with %s. Got %x, expected %x",
			address, peerHandshake.InfoHash, infoHash)
	}
	logger.Logf("peer: handshake successful with %s (PeerID: %x)", address, peerHandshake.PeerID)

	return &Client{
		Conn:               conn,
		InfoHash:           infoHash,
		OurID:              ourID,
		RemoteID:           peerHandshake.PeerID,
		Choked:             true,
		Bitfield:           NewBitfield(numPiecesInTorrent),
		numPiecesInTorrent: numPiecesInTorrent,
		WorkQueue:          make(chan *BlockRequest, 5),
		Results:            make(chan *PieceBlock),
		ourBitfield:        ourBitfield,
		readBlockFromDisk:  readBlockFunc,
	}, nil
}

// Run is the main loop for a peer connection.
func (c *Client) Run() {
	defer c.Conn.Close()
	defer close(c.Results)

	logger.Logf("Starting communication loop for peer %s", c.Conn.RemoteAddr())

	if err := c.SendInterested(); err != nil {
		logger.Logf("Error sending Interested to %s: %v", c.Conn.RemoteAddr(), err)
		return
	}

	for {
		msg, err := c.ReadMessage()
		if err != nil {
			logger.Logf("Error reading message from peer %s, closing connection: %v", c.Conn.RemoteAddr(), err)
			return
		}
		if msg == nil {
			continue // Keep-alive
		}

		switch msg.ID {
		case MsgChoke:
			c.Choked = true
			logger.Logf("Peer %s choked us.", c.Conn.RemoteAddr())
		case MsgUnchoke:
			c.Choked = false
			logger.Logf("Peer %s unchoked us.", c.Conn.RemoteAddr())
		case MsgHave:
			var havePayload MsgHavePayload
			if err := havePayload.Parse(msg.Payload); err == nil {
				if int(havePayload.PieceIndex) < c.numPiecesInTorrent {
					c.Bitfield.SetPiece(havePayload.PieceIndex)
				}
			}
		case MsgBitfield:
			if len(msg.Payload) == len(c.Bitfield) {
				copy(c.Bitfield, msg.Payload)
			}
		case MsgPiece:
			var piecePayload MsgPiecePayload
			if err := piecePayload.Parse(msg.Payload); err == nil {
				c.Results <- &PieceBlock{
					Index: piecePayload.Index,
					Begin: piecePayload.Begin,
					Block: piecePayload.Block,
				}
			}
		case MsgRequest:
			var reqPayload MsgRequestPayload
			if err := reqPayload.Parse(msg.Payload); err == nil {
				logger.Logf("Peer %s requested piece %d, offset %d, length %d",
					c.Conn.RemoteAddr(), reqPayload.Index, reqPayload.Begin, reqPayload.Length)

				if c.ourBitfield.HasPiece(reqPayload.Index) {
					blockData, err := c.readBlockFromDisk(reqPayload.Index, reqPayload.Begin, reqPayload.Length)
					if err != nil {
						logger.Logf("Error reading block from disk for peer request: %v", err)
					} else {
						err := c.SendPiece(reqPayload.Index, reqPayload.Begin, blockData)
						if err != nil {
							logger.Logf("Error sending Piece message to peer %s: %v", c.Conn.RemoteAddr(), err)
						} else {
							logger.Logf("Sent piece %d, block offset %d to peer %s",
								reqPayload.Index, reqPayload.Begin, c.Conn.RemoteAddr())
							// TODO: Update Uploaded stats
						}
					}
				} else {
					logger.Logf("Peer %s requested piece %d which we don't have.", c.Conn.RemoteAddr(), reqPayload.Index)
				}
			} else {
				logger.Logf("Error parsing Request message from peer %s: %v", c.Conn.RemoteAddr(), err)
			}
		} // End switch

		if !c.Choked {
			select {
			case work := <-c.WorkQueue:
				if c.Bitfield.HasPiece(work.Index) {
					err := c.SendRequest(work.Index, work.Begin, work.Length)
					if err != nil {
						logger.Logf("Peer %s: failed to send request: %v", c.Conn.RemoteAddr(), err)
						// TODO: Put work back in the main session's queue.
					}
				} else {
					logger.Logf("Peer %s: was assigned work for piece %d it doesn't have.", c.Conn.RemoteAddr(), work.Index)
					// TODO: Put work back in the main session's queue.
				}
			default:
				// No work in queue, do nothing.
			}
		}
	}
}

// Close closes the connection to the peer.
func (c *Client) Close() error {
	if c.Conn != nil {
		return c.Conn.Close()
	}
	return nil
}

// ReadMessage reads and parses a single message from the peer.
func (c *Client) ReadMessage() (*Message, error) {
	c.Conn.SetReadDeadline(time.Now().Add(3 * time.Minute))
	defer c.Conn.SetReadDeadline(time.Time{})

	lengthPrefix := make([]byte, 4)
	_, err := io.ReadFull(c.Conn, lengthPrefix)
	if err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lengthPrefix)
	if length == 0 {
		return nil, nil // Keep-alive
	}
	if length > 1024*1024*2 { // 2MB sanity check
		return nil, fmt.Errorf("message length %d too large", length)
	}
	messageBytes := make([]byte, length)
	_, err = io.ReadFull(c.Conn, messageBytes)
	if err != nil {
		return nil, err
	}
	return &Message{
		ID:      MessageID(messageBytes[0]),
		Payload: messageBytes[1:],
	}, nil
}

// SendMessage serializes and sends a message to the peer.
func (c *Client) SendMessage(id MessageID, payload []byte) error {
	msg := &Message{ID: id, Payload: payload}
	_, err := c.Conn.Write(msg.Serialize())
	if err != nil {
		return fmt.Errorf("failed to send message ID %s: %w", id, err)
	}
	return nil
}

// Helper methods for sending specific messages
func (c *Client) SendInterested() error    { return c.SendMessage(MsgInterested, nil) }
func (c *Client) SendNotInterested() error { return c.SendMessage(MsgNotInterested, nil) }
func (c *Client) SendHave(pieceIndex uint32) error {
	logger.Logf("Sending HAVE for piece %d to %s", pieceIndex, c.Conn.RemoteAddr())
	payload := MsgHavePayload{PieceIndex: pieceIndex}
	return c.SendMessage(MsgHave, payload.Serialize())
}
func (c *Client) SendRequest(index, begin, length uint32) error {
	payload := MsgRequestPayload{Index: index, Begin: begin, Length: length}
	return c.SendMessage(MsgRequest, payload.Serialize())
}
func (c *Client) SendPiece(index, begin uint32, block []byte) error {
	payload := MsgPiecePayload{Index: index, Begin: begin, Block: block}
	return c.SendMessage(MsgPiece, payload.Serialize())
}
