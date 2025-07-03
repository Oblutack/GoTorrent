// In internal/peer/peer.go
package peer

import (
	"bytes" // For comparing byte slices
	"encoding/binary"
	"fmt"
	"io"
	"log" // For InfoHash size
	"net"
	"strconv"
	"time"

	"github.com/Oblutack/GoTorrent/internal/tracker"
)

const (
	// ProtocolString is the string identifier for the BitTorrent protocol.
	ProtocolString    = "BitTorrent protocol"
	protocolStringLen = byte(len(ProtocolString)) // Should be 19
	handshakeTimeout  = 10 * time.Second          // Timeout for handshake process
	readTimeout       = 5 * time.Second           // Timeout for reading messages
)

// Handshake represents the initial handshake message in the BitTorrent protocol.
// Total length is 1 (pstrlen) + 19 (pstr) + 8 (reserved) + 20 (infohash) + 20 (peerid) = 68 bytes.
type Handshake struct {
	Pstrlen  byte     // Length of pstr, should be 19
	Pstr     [19]byte // String identifier of the protocol
	Reserved [8]byte  // 8 reserved bytes, all should be 0 for standard behavior
	InfoHash [20]byte // SHA1 hash of the info dictionary of the torrent
	PeerID   [20]byte // 20-byte unique ID for the client
}

// NewHandshake creates a new Handshake struct with the given infohash and our peerID.
func NewHandshake(infoHash, peerID [20]byte) *Handshake {
	hs := &Handshake{
		Pstrlen:  protocolStringLen,
		InfoHash: infoHash,
		PeerID:   peerID,
	}
	copy(hs.Pstr[:], ProtocolString)
	// Reserved bytes are already zero by default for new arrays.
	return hs
}

// Serialize converts the Handshake struct into a byte slice for sending.
func (h *Handshake) Serialize() []byte {
	buf := make([]byte, 1+len(h.Pstr)+len(h.Reserved)+len(h.InfoHash)+len(h.PeerID)) // 68 bytes
	buf[0] = h.Pstrlen
	curr := 1
	curr += copy(buf[curr:], h.Pstr[:])
	curr += copy(buf[curr:], h.Reserved[:])
	curr += copy(buf[curr:], h.InfoHash[:])
	curr += copy(buf[curr:], h.PeerID[:])
	return buf
}

// ReadHandshake reads and parses a handshake from the reader.
// It returns the parsed Handshake from the peer and an error if any.
func ReadHandshake(r io.Reader) (*Handshake, error) {
	handshakeBytes := make([]byte, 68) // Handshake is fixed 68 bytes

	// Set a deadline for reading the handshake
	// This requires r to be a net.Conn or similar that supports SetReadDeadline
	if conn, ok := r.(net.Conn); ok {
		conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
		defer conn.SetReadDeadline(time.Time{}) // Clear deadline after read
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

	// Validate pstr
	if string(hs.Pstr[:]) != ProtocolString {
		return nil, fmt.Errorf("peer: invalid pstr '%s', expected '%s'", string(hs.Pstr[:]), ProtocolString)
	}

	return hs, nil
}

// Client represents a connection to a single BitTorrent peer.
type Client struct {
	Conn     net.Conn
	Peer     tracker.PeerInfo // From tracker package (IP, Port of the remote peer)
	RemoteID [20]byte         // PeerID of the remote peer (once handshake is done)
	InfoHash [20]byte         // Infohash of the torrent we are interested in
	OurID    [20]byte		  // Our PeerID
	PeerBitfield Bitfield
	NumPiecesInTorrent int          

	// State related to the peer
	// ChokedByUs      bool // Are we choking this peer? Default true
	// InterestedInUs  bool // Is this peer interested in us? Default false
	// ChokedByPeer    bool // Is this peer choking us? Default true
	// InterestedByUs  bool // Are we interested in this peer? Default false
	// PeerBitfield    Bitfield // Pieces the peer has
}

// TODO: Add tracker.PeerInfo to peer.go or import tracker package correctly if Client needs it.
// For now, let's assume we pass tracker.PeerInfo to a function that creates a Client.
// Or, if tracker.PeerInfo is defined in a way that's hard to import,
// we can redefine a simpler PeerAddr struct here.
// Let's assume we have `import "github.com/Oblutack/GoTorrent/internal/tracker"`

// NewClient attempts to connect to a peer and perform a handshake.
func NewClient(peerInfo tracker.PeerInfo, infoHash, ourID [20]byte, numPiecesInTorrent int) (*Client, error) {
	address := net.JoinHostPort(peerInfo.IP.String(), strconv.Itoa(int(peerInfo.Port)))
	log.Printf("peer: attempting to connect to %s", address)

	conn, err := net.DialTimeout("tcp", address, handshakeTimeout)
	if err != nil {
		return nil, fmt.Errorf("peer: failed to dial %s: %w", address, err)
	}
	// No defer conn.Close() here, the Client struct will own the connection.
	// The caller of NewClient will be responsible for closing it.

	// 1. Send our handshake
	ourHandshake := NewHandshake(infoHash, ourID)
	_, err = conn.Write(ourHandshake.Serialize())
	if err != nil {
		conn.Close() // Close connection on error
		return nil, fmt.Errorf("peer: failed to send handshake to %s: %w", address, err)
	}
	log.Printf("peer: sent handshake to %s", address)


	// 2. Read peer's handshake
	log.Printf("peer: waiting for handshake from %s", address)
	peerHandshake, err := ReadHandshake(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("peer: failed to read handshake from %s: %w", address, err)
	}
	log.Printf("peer: received handshake from %s", address)


	// 3. Validate peer's handshake
	if !bytes.Equal(peerHandshake.InfoHash[:], infoHash[:]) {
		conn.Close()
		return nil, fmt.Errorf("peer: handshake InfoHash mismatch with %s. Got %x, expected %x",
			address, peerHandshake.InfoHash, infoHash)
	}
	log.Printf("peer: handshake InfoHash matched with %s", address)


	// Handshake successful
	client := &Client{
		Conn:     conn,
		Peer:     peerInfo,
		RemoteID: peerHandshake.PeerID,
		InfoHash: infoHash,
		OurID:    ourID,
		NumPiecesInTorrent: numPiecesInTorrent,
		PeerBitfield:       NewBitfield(numPiecesInTorrent), 
		// Initialize other state fields later
	}

	log.Printf("peer: handshake successful with %s (PeerID: %x)", address, client.RemoteID)
	return client, nil
}

// Close closes the connection to the peer.
func (c *Client) Close() error {
	if c.Conn != nil {
		return c.Conn.Close()
	}
	return nil
}

func (c *Client) ReadMessage() (*Message, error) {
	// Set a deadline for reading the length prefix.
	// Important to avoid blocking indefinitely if peer sends nothing.
	c.Conn.SetReadDeadline(time.Now().Add(readTimeout + 30*time.Second)) // Longer timeout for general reads
                                                                        // Can be adjusted or made dynamic
    defer c.Conn.SetReadDeadline(time.Time{}) // Clear deadline


	lengthPrefix := make([]byte, 4)
	_, err := io.ReadFull(c.Conn, lengthPrefix)
	if err != nil {
		return nil, fmt.Errorf("peer: failed to read message length prefix: %w", err)
	}

	length := binary.BigEndian.Uint32(lengthPrefix)

	if length == 0 {
		// Keep-alive message
		log.Printf("peer %s: received keep-alive", c.Conn.RemoteAddr())
		return nil, nil // Represent keep-alive as nil Message, nil error
	}

	if length > 1024*1024*2 { // Sanity check for message length (e.g., 2MB max)
		return nil, fmt.Errorf("peer: message length %d too large", length)
	}

	// Read message ID + payload
	messageBytes := make([]byte, length)
	_, err = io.ReadFull(c.Conn, messageBytes)
	if err != nil {
		return nil, fmt.Errorf("peer: failed to read message body (length %d): %w", length, err)
	}

	msg := &Message{
		ID:      MessageID(messageBytes[0]),
		Payload: messageBytes[1:],
	}
	// log.Printf("peer %s: received message ID %s, len %d", c.Conn.RemoteAddr(), msg.ID, len(msg.Payload))
	return msg, nil
}

// SendMessage serializes and sends a message to the peer.
// For messages without payload (Choke, Unchoke, Interested, NotInterested), payload can be nil.
func (c *Client) SendMessage(id MessageID, payload []byte) error {
	msg := &Message{ID: id, Payload: payload}
	// log.Printf("peer %s: sending message ID %s, len %d", c.Conn.RemoteAddr(), id, len(payload))
	_, err := c.Conn.Write(msg.Serialize())
	if err != nil {
		return fmt.Errorf("peer: failed to send message ID %s: %w", id, err)
	}
	return nil
}

// SendInterested sends an Interested message to the peer.
func (c *Client) SendInterested() error {
	// c.InterestedByUs = true // Update our state
	return c.SendMessage(MsgInterested, nil)
}

// SendNotInterested sends a NotInterested message to the peer.
func (c *Client) SendNotInterested() error {
    // c.InterestedByUs = false
	return c.SendMessage(MsgNotInterested, nil)
}

// SendHave sends a Have message to the peer.
func (c *Client) SendHave(pieceIndex uint32) error {
	payload := MsgHavePayload{PieceIndex: pieceIndex}
	return c.SendMessage(MsgHave, payload.Serialize())
}

// SendRequest sends a Request message to the peer.
func (c *Client) SendRequest(index, begin, length uint32) error {
	payload := MsgRequestPayload{Index: index, Begin: begin, Length: length}
	return c.SendMessage(MsgRequest, payload.Serialize())
}