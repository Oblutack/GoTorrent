package peer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

const (
	ProtocolString    = "BitTorrent protocol"
	protocolStringLen = byte(len(ProtocolString))
	handshakeTimeout  = 10 * time.Second
	readTimeout       = 5 * time.Second
	PipelineSize      = 50
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
	Bitfield           Bitfield
	numPiecesInTorrent int

	// Our state in relation to the peer
	AmChoking    bool // True if we are choking the peer
	AmInterested bool // True if we are interested in the peer

	// Peer's state in relation to us
	PeerChoking    bool // True if the peer is choking us
	PeerInterested bool // True if the peer is interested in us

	WorkQueue chan *BlockRequest
	Results   chan *PieceBlock

	// Dependencies injected from the session for seeding
	ourBitfield       Bitfield
	readBlockFromDisk func(index, begin, length uint32) ([]byte, error)
}

// NewClient attempts to connect to a peer and perform a handshake.
func NewClient(peerInfo tracker.PeerInfo, infoHash, ourID [20]byte, numPiecesInTorrent int, ourBitfield Bitfield, readBlockFunc func(index, begin, length uint32) ([]byte, error)) (*Client, error) {
	address := net.JoinHostPort(peerInfo.IP.String(), strconv.Itoa(int(peerInfo.Port)))
	logger.Logf("peer: attempting to connect to %s\n", address)
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
	logger.Logf("peer: handshake successful with %s (PeerID: %x)\n", address, peerHandshake.PeerID)

	return &Client{
		Conn:               conn,
		InfoHash:           infoHash,
		OurID:              ourID,
		RemoteID:           peerHandshake.PeerID,
		Bitfield:           NewBitfield(numPiecesInTorrent),
		numPiecesInTorrent: numPiecesInTorrent,
		AmChoking:          true,  // We start by choking the peer
		AmInterested:       false, // We are not interested yet
		PeerChoking:        true,  // Assume peer is choking us initially
		PeerInterested:     false, // Assume peer is not interested initially
		WorkQueue:          make(chan *BlockRequest, PipelineSize),
		Results:            make(chan *PieceBlock),
		ourBitfield:        ourBitfield,
		readBlockFromDisk:  readBlockFunc,
	}, nil
}

// Run is the main loop for a peer connection.
func (c *Client) Run() {
	defer c.Conn.Close()
	defer close(c.Results)

	logger.Logf("Starting communication loop for peer %s\n", c.Conn.RemoteAddr())

	if err := c.SendInterested(); err != nil {
		logger.Logf("Error sending Interested to %s: %v\n", c.Conn.RemoteAddr(), err)
		return
	}

	for {
		msg, err := c.ReadMessage()
		if err != nil {
			logger.Warning.Printf("Error reading message from peer %s, closing connection: %v\n", c.Conn.RemoteAddr(), err)
			return
		}
		if msg == nil {
			continue // Keep-alive
		}

		switch msg.ID {
		case MsgChoke:
			c.PeerChoking = true
			logger.Logf("Peer %s choked us.\n", c.Conn.RemoteAddr())
		case MsgUnchoke:
			c.PeerChoking = false
			logger.Logf("Peer %s unchoked us.\n", c.Conn.RemoteAddr())
		case MsgInterested:
			c.PeerInterested = true
			logger.Logf("Peer %s is now interested in us.\n", c.Conn.RemoteAddr())
			// Simple strategy: if they are interested, unchoke them.
			// A real client would have a more complex choking algorithm.
			if err := c.SendUnchoke(); err != nil {
				logger.Warning.Printf("Failed to send Unchoke to %s: %v\n", c.Conn.RemoteAddr(), err)
			} else {
				c.AmChoking = false
				logger.Logf("Sent Unchoke to interested peer %s.\n", c.Conn.RemoteAddr())
			}
		case MsgNotInterested:
			c.PeerInterested = false
			logger.Logf("Peer %s is no longer interested in us.\n", c.Conn.RemoteAddr())
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
				logger.Logf("Peer %s requested piece %d, offset %d, length %d\n",
					c.Conn.RemoteAddr(), reqPayload.Index, reqPayload.Begin, reqPayload.Length)

				// Respond to request if we have the piece and we are not choking them
				if c.ourBitfield.HasPiece(reqPayload.Index) && !c.AmChoking {
					blockData, err := c.readBlockFromDisk(reqPayload.Index, reqPayload.Begin, reqPayload.Length)
					if err != nil {
						logger.Error.Printf("Error reading block from disk for peer request: %v\n", err)
					} else {
						err := c.SendPiece(reqPayload.Index, reqPayload.Begin, blockData)
						if err != nil {
							logger.Warning.Printf("Error sending Piece message to peer %s: %v\n", c.Conn.RemoteAddr(), err)
						} else {
							logger.Logf("Sent piece %d, block offset %d to peer %s\n",
								reqPayload.Index, reqPayload.Begin, c.Conn.RemoteAddr())
							// TODO: Update Uploaded stats for session
						}
					}
				} else {
					logger.Logf("Ignoring request from peer %s for piece %d (we don't have it or we are choking them).\n",
						c.Conn.RemoteAddr(), reqPayload.Index)
				}
			} else {
				logger.Warning.Printf("Error parsing Request message from peer %s: %v\n", c.Conn.RemoteAddr(), err)
			}
		} // End switch

		// Logic to send block requests from our work queue
		if !c.PeerChoking { // Check if the PEER is not choking US
			select {
			case work := <-c.WorkQueue:
				if c.Bitfield.HasPiece(work.Index) {
					err := c.SendRequest(work.Index, work.Begin, work.Length)
					if err != nil {
						logger.Warning.Printf("Peer %s: failed to send request: %v\n", c.Conn.RemoteAddr(), err)
						// TODO: Put work back in the main session's queue.
					}
				} else {
					logger.Logf("Peer %s: was assigned work for piece %d it doesn't have.\n", c.Conn.RemoteAddr(), work.Index)
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
		return nil, nil
	}
	if length > 1024*1024*2 {
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
func (c *Client) SendInterested() error {
	c.AmInterested = true
	return c.SendMessage(MsgInterested, nil)
}
func (c *Client) SendNotInterested() error {
	c.AmInterested = false
	return c.SendMessage(MsgNotInterested, nil)
}
func (c *Client) SendHave(pieceIndex uint32) error {
	logger.Logf("Sending HAVE for piece %d to %s\n", pieceIndex, c.Conn.RemoteAddr())
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
func (c *Client) SendChoke() error {
	c.AmChoking = true
	return c.SendMessage(MsgChoke, nil)
}
func (c *Client) SendUnchoke() error {
	c.AmChoking = false
	return c.SendMessage(MsgUnchoke, nil)
}
