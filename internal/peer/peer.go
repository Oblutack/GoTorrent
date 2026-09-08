package peer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bitfield"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/ratelimit"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

const (
	ProtocolString    = "BitTorrent protocol"
	protocolStringLen = byte(len(ProtocolString))
	handshakeTimeout  = 10 * time.Second
	readTimeout       = 3 * time.Minute
	writeTimeout      = 30 * time.Second
	// MaxPipelineSize is the hard ceiling on outstanding requests queued to
	// one peer, regardless of what the torrent actor's adaptive pipelining
	// computes as a target. It exists so a runaway target (or a bug in the
	// adaptation) can never grow the channel's memory footprint unboundedly.
	MaxPipelineSize = 500

	// MaxBlockLength is the largest block a peer may ask us for. BEP 3 fixes
	// the request size at 16 KiB; a peer asking for more is either broken or
	// probing for an allocation primitive, so we drop it.
	MaxBlockLength = 16384

	// maxMessageLength caps a single wire frame. The largest legitimate
	// message is a bitfield, which is numPieces/8 bytes.
	maxMessageLength = 2 * 1024 * 1024

	// outboundQueueSize is how many serialized frames may wait for sendLoop.
	outboundQueueSize = 64
)

var (
	// ErrClientClosed is returned when a send is attempted on a closed peer.
	ErrClientClosed = errors.New("peer: connection closed")
	// ErrOutboundFull is returned when the peer cannot drain its send queue
	// fast enough. Callers must treat this as a dropped message, not a fatal
	// error: blocking here would stall the session.
	ErrOutboundFull = errors.New("peer: outbound queue full")
)

// TorrentInfo is the subset of the metainfo a peer connection needs in order
// to validate what the remote side sends us.
type TorrentInfo struct {
	InfoHash    [20]byte
	NumPieces   int
	PieceLength int64
	TotalLength int64
}

// PieceLen returns the length of a specific piece, accounting for the short
// final piece.
func (t TorrentInfo) PieceLen(index uint32) int64 {
	if t.NumPieces <= 0 {
		return 0
	}
	if int(index) == t.NumPieces-1 {
		last := t.TotalLength - int64(t.NumPieces-1)*t.PieceLength
		if last < 0 {
			return 0
		}
		return last
	}
	return t.PieceLength
}

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
	// Advertise BEP 10 extension-protocol support unconditionally — this
	// client always understands the envelope, even before it supported any
	// extension riding inside it.
	hs.Reserved[extensionReservedByte] |= extensionReservedBit
	// Advertise BEP 6 (Fast extension) support unconditionally too — see
	// fast.go.
	hs.Reserved[fastReservedByte] |= fastReservedBit
	return hs
}

// SupportsExtensions reports whether the reserved bits in a handshake
// advertise BEP 10 extension-protocol support.
func (h *Handshake) SupportsExtensions() bool {
	return h.Reserved[extensionReservedByte]&extensionReservedBit != 0
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

// EventKind identifies a control-plane change on a peer connection: state a
// torrent actor needs to react to but that is not itself a data block.
type EventKind uint8

const (
	// EventBitfield fires once, after a Bitfield message replaces the peer's
	// advertised pieces wholesale.
	EventBitfield EventKind = iota
	// EventHave fires per piece the peer announces after the initial bitfield.
	EventHave
	// EventChokeChanged fires when the peer chokes or unchokes us.
	EventChokeChanged
	// EventInterestedChanged fires when the peer's interest in us changes.
	EventInterestedChanged
	// EventExtendedHandshake fires once, after the peer's BEP 10 extended
	// handshake is processed — the owner can then check SupportsUtMetadata
	// and PeerMetadataSize to decide whether to fetch metadata from this
	// peer.
	EventExtendedHandshake
	// EventMetadataReject fires when the peer refuses a requested metadata
	// piece (BEP 9 msg_type 2). PieceIndex is the rejected piece.
	EventMetadataReject
	// EventRejectRequest fires when the peer explicitly declines a block we
	// requested (BEP 6) — PieceIndex/Begin/Length identify it, so the owner
	// can stop waiting on that exact block instead of only finding out via
	// its own request timeout.
	EventRejectRequest
)

func (k EventKind) String() string {
	switch k {
	case EventBitfield:
		return "Bitfield"
	case EventHave:
		return "Have"
	case EventChokeChanged:
		return "ChokeChanged"
	case EventInterestedChanged:
		return "InterestedChanged"
	case EventExtendedHandshake:
		return "ExtendedHandshake"
	case EventMetadataReject:
		return "MetadataReject"
	case EventRejectRequest:
		return "RejectRequest"
	default:
		return fmt.Sprintf("UnknownEvent(%d)", k)
	}
}

// Event is a control-plane notification from a Client's read loop. It carries
// no peer pointer — the owner already knows which Client it came from, since
// each Client's Events channel is private to it.
type Event struct {
	Kind       EventKind
	PieceIndex uint32 // valid for EventHave, EventMetadataReject, EventRejectRequest
	Begin      uint32 // valid for EventRejectRequest
	Length     uint32 // valid for EventRejectRequest
}

// eventQueueSize bounds how many pending events a slow consumer may leave
// unread. Have/choke/interest traffic is low-rate compared to blocks, so this
// is generous headroom rather than a tight budget.
const eventQueueSize = 256

// Limits bounds how fast one connection may exchange data by waiting on
// every limiter in Down/Up in turn — nil or empty means unlimited on that
// direction. Waiting on each sequentially rather than jointly is a
// deliberate simplification: worst case it is a little more conservative
// than the true minimum of several simultaneously-contended limiters, never
// less, so a connection carrying both a per-torrent and a process-wide cap
// (3.3) never exceeds either one. The same *ratelimit.Limiter passed to
// every Client sharing a budget (a torrent, or the whole process) is what
// makes a limit apply in aggregate rather than per-peer.
type Limits struct {
	Down []*ratelimit.Limiter
	Up   []*ratelimit.Limiter
}

// wait blocks until every limiter in ls grants n bytes' worth of budget, or
// done fires first — see Limits' doc comment for why this is sequential
// rather than joint.
func waitAll(ls []*ratelimit.Limiter, done <-chan struct{}, n int) bool {
	for _, l := range ls {
		if !l.Wait(done, n) {
			return false
		}
	}
	return true
}

// Callbacks are the owner's hooks for serving another peer's requests. All
// three are optional; a nil one just means "we never have anything to
// serve" for that kind of request rather than a panic.
type Callbacks struct {
	// HasPiece reports whether we have a given piece, for answering Request.
	HasPiece func(index uint32) bool
	// ReadBlock reads one block off disk, for answering Request.
	ReadBlock func(index, begin, length uint32) ([]byte, error)
	// MetadataBytes returns the raw info-dictionary bytes if known, for
	// answering a BEP 9 ut_metadata request — nil means we don't have
	// metadata yet ourselves (e.g. we're mid-magnet too).
	MetadataBytes func() []byte
	// UploadOnly reports whether we currently want nothing further from
	// peers (BEP 21) — true once seeding, or once every wanted file (3.2
	// priorities) is complete even if the torrent as a whole isn't. Nil
	// means never announce upload_only at all (advertised as false).
	UploadOnly func() bool
}

// Client represents a connection to a single BitTorrent peer.
//
// Concurrency contract: exactly one goroutine ever writes to Conn (sendLoop),
// exactly one ever reads from it (Run). Every field shared with the session
// is either immutable after construction, an atomic, or guarded by
// bitfieldMu — nothing is a plain field read across goroutines.
type Client struct {
	Conn     net.Conn
	OurID    [20]byte
	RemoteID [20]byte

	// torrentInfo starts out possibly NumPieces==0 (the magnet-link path)
	// and is upgraded exactly once, by UpgradeMetadata, when the owner
	// learns the real metadata — from a goroutine other than this
	// connection's own read loop, hence the atomic rather than a plain
	// field like everything else that never changes after construction.
	torrentInfo atomic.Pointer[TorrentInfo]

	// Choke/interest state. Written by the read loop and by the session's
	// choking algorithm, read by both plus writeLoop.
	amChoking      atomic.Bool // we are choking the peer
	amInterested   atomic.Bool // we are interested in the peer
	peerChoking    atomic.Bool // the peer is choking us
	peerInterested atomic.Bool // the peer is interested in us

	// bitfield is written by the read loop (Have/Bitfield/HaveAll/HaveNone)
	// and read by the session's rarity scan. pendingBitfield holds a peer's
	// raw Bitfield bytes received before torrentInfo had a nonzero
	// NumPieces to validate them against; pendingHaveAll is the same idea
	// for a BEP 6 HaveAll (HaveNone needs no such flag — an empty bitfield
	// is already the default). UpgradeMetadata applies whichever is set
	// once NumPieces is known. Only one of the two is ever set, since BEP
	// 3/6 both say a peer sends exactly one of Bitfield/HaveAll/HaveNone,
	// and only as its first message.
	bitfieldMu      sync.RWMutex
	bitfield        *bitfield.Bitfield
	pendingBitfield []byte
	pendingHaveAll  bool

	// allowedFast holds the piece indices the peer has told us (BEP 6's
	// AllowedFast) we may request even while choked. Separate lock from
	// bitfieldMu: written only by the read loop, read only by the owner's
	// picking logic, no reason to contend with bitfield access for it.
	allowedFastMu sync.RWMutex
	allowedFast   map[uint32]bool

	WorkQueue chan *BlockRequest
	Results   chan *PieceBlock

	// Events carries control-plane changes (Have, Bitfield, choke/interest,
	// extended handshake, metadata reject) to whatever owns this Client. It
	// is closed alongside Results when Run returns.
	Events chan Event

	// MetadataPieces carries BEP 9 metadata chunks as they arrive — data-
	// bearing like Results, not lightweight like Events. Closed alongside
	// Results when Run returns.
	MetadataPieces chan MetadataPiece

	// PEXUpdates carries BEP 11 peer-exchange messages as they arrive —
	// data-bearing like Results, not lightweight like Events. Closed
	// alongside Results when Run returns.
	PEXUpdates chan PEXUpdate

	// outbound carries serialized frames to the single writer goroutine.
	outbound  chan []byte
	done      chan struct{}
	closeOnce sync.Once

	lastPieceReceived atomic.Int64 // unix seconds
	uploaded          atomic.Int64 // cumulative bytes served to this peer

	// limits bounds our transfer rate on this connection; see Limits. Set
	// once at construction, read by the read loop (download) and sendLoop
	// (upload), so it needs no synchronization of its own.
	limits Limits

	// peerSupportsExt is read from the handshake's reserved bits at
	// construction and never changes, so it needs no synchronization.
	peerSupportsExt bool
	// peerSupportsFast mirrors peerSupportsExt for BEP 6 (Fast extension).
	peerSupportsFast bool
	// peerUtMetadataID is the id (BEP 10, from the peer's own "m" dict) to
	// address them by when we want to send a ut_metadata message; 0 means
	// they haven't told us, or don't support it.
	peerUtMetadataID atomic.Int32
	// peerMetadataSize is their advertised metadata_size, once known.
	peerMetadataSize atomic.Int64
	// peerUtPexID mirrors peerUtMetadataID for BEP 11 ut_pex.
	peerUtPexID atomic.Int32
	// peerUploadOnlyID mirrors peerUtMetadataID for BEP 21 upload_only.
	peerUploadOnlyID atomic.Int32
	// peerUploadOnly is the peer's most recently announced BEP 21 status
	// (via the extended handshake's own "upload_only" key, or a live
	// upload_only message) — false until they say otherwise. Parsed and
	// stored, but nothing in this client acts on it yet: same "received,
	// validated, intentionally not acted on" shape as SuggestPiece (BEP 6).
	peerUploadOnly atomic.Bool

	// Dependencies injected from the owner for serving another peer's
	// requests. See Callbacks.
	hasPiece          func(index uint32) bool
	readBlockFromDisk func(index, begin, length uint32) ([]byte, error)
	metadataBytes     func() []byte
	uploadOnly        func() bool
}

// DialFunc dials one outbound connection, matching net.Dialer.DialContext's
// signature exactly so a *proxy.Dialer's method value can be passed
// directly. A nil DialFunc passed to NewClient dials directly.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// NewClient attempts to connect to a peer and perform a handshake. A zero
// Limits leaves both directions unlimited; a zero Callbacks means this
// connection never serves anything to the peer (fine for a client that only
// ever downloads). dial is optional (nil dials directly with net.Dialer) —
// 3.7's proxy support passes a *proxy.Dialer's DialContext method value so
// outbound peer connections tunnel through a configured SOCKS5/HTTP proxy.
func NewClient(
	peerInfo tracker.PeerInfo,
	torrent TorrentInfo,
	ourID [20]byte,
	callbacks Callbacks,
	limits Limits,
	dial DialFunc,
) (*Client, error) {
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	address := net.JoinHostPort(peerInfo.IP.String(), strconv.Itoa(int(peerInfo.Port)))
	logger.Logf("peer: attempting to connect to %s\n", address)
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	conn, err := dial(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("peer: failed to dial %s: %w", address, err)
	}

	ourHandshake := NewHandshake(torrent.InfoHash, ourID)
	conn.SetWriteDeadline(time.Now().Add(handshakeTimeout))
	_, err = conn.Write(ourHandshake.Serialize())
	conn.SetWriteDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("peer: failed to send handshake to %s: %w", address, err)
	}

	peerHandshake, err := ReadHandshake(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("peer: failed to read handshake from %s: %w", address, err)
	}

	if !bytes.Equal(peerHandshake.InfoHash[:], torrent.InfoHash[:]) {
		conn.Close()
		return nil, fmt.Errorf("peer: handshake InfoHash mismatch with %s. Got %x, expected %x",
			address, peerHandshake.InfoHash, torrent.InfoHash)
	}
	logger.Logf("peer: handshake successful with %s (PeerID: %x)\n", address, peerHandshake.PeerID)

	return newClient(conn, torrent, ourID, peerHandshake, callbacks, limits), nil
}

// AcceptClient completes a connection we accepted (as opposed to dialed): the
// caller has already read theirHandshake — it had to, in order to learn the
// infohash and route the connection to the right owner in the first place —
// so this only needs to send our own reply handshake before constructing a
// Client exactly like NewClient does for an outbound connection.
func AcceptClient(
	conn net.Conn,
	theirHandshake *Handshake,
	torrent TorrentInfo,
	ourID [20]byte,
	callbacks Callbacks,
	limits Limits,
) (*Client, error) {
	ourHandshake := NewHandshake(torrent.InfoHash, ourID)
	conn.SetWriteDeadline(time.Now().Add(handshakeTimeout))
	_, err := conn.Write(ourHandshake.Serialize())
	conn.SetWriteDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("peer: failed to send handshake reply to %s: %w", conn.RemoteAddr(), err)
	}
	logger.Logf("peer: accepted connection from %s (PeerID: %x)\n", conn.RemoteAddr(), theirHandshake.PeerID)

	return newClient(conn, torrent, ourID, theirHandshake, callbacks, limits), nil
}

// newClient builds a Client once a handshake has been exchanged in either
// direction, shared by NewClient and AcceptClient.
func newClient(conn net.Conn, torrent TorrentInfo, ourID [20]byte, peerHandshake *Handshake, callbacks Callbacks, limits Limits) *Client {
	c := &Client{
		Conn:              conn,
		OurID:             ourID,
		RemoteID:          peerHandshake.PeerID,
		bitfield:          bitfield.New(torrent.NumPieces),
		WorkQueue:         make(chan *BlockRequest, MaxPipelineSize),
		Results:           make(chan *PieceBlock),
		Events:            make(chan Event, eventQueueSize),
		MetadataPieces:    make(chan MetadataPiece),
		PEXUpdates:        make(chan PEXUpdate),
		outbound:          make(chan []byte, outboundQueueSize),
		done:              make(chan struct{}),
		limits:            limits,
		peerSupportsExt:   peerHandshake.SupportsExtensions(),
		peerSupportsFast:  peerHandshake.SupportsFast(),
		hasPiece:          callbacks.HasPiece,
		readBlockFromDisk: callbacks.ReadBlock,
		metadataBytes:     callbacks.MetadataBytes,
		uploadOnly:        callbacks.UploadOnly,
	}
	c.torrentInfo.Store(&torrent)
	c.amChoking.Store(true)   // we start by choking the peer
	c.peerChoking.Store(true) // assume the peer is choking us initially
	c.lastPieceReceived.Store(time.Now().Unix())

	return c
}

// --- state accessors -------------------------------------------------------

// AmChoking reports whether we are currently choking this peer.
func (c *Client) AmChoking() bool { return c.amChoking.Load() }

// AmInterested reports whether we have told the peer we want its pieces.
func (c *Client) AmInterested() bool { return c.amInterested.Load() }

// PeerChoking reports whether the peer is currently choking us.
func (c *Client) PeerChoking() bool { return c.peerChoking.Load() }

// PeerInterested reports whether the peer wants pieces from us.
func (c *Client) PeerInterested() bool { return c.peerInterested.Load() }

// LastPieceReceivedUnix is the unix time of the most recent block from this
// peer, used by the session to drop stalled connections.
func (c *Client) LastPieceReceivedUnix() int64 { return c.lastPieceReceived.Load() }

// info returns the connection's current understanding of the torrent's
// geometry — NumPieces==0 until metadata is known (or, before UpgradeMetadata
// is ever called, for the life of a connection that started as a magnet).
func (c *Client) info() TorrentInfo { return *c.torrentInfo.Load() }

// UpgradeMetadata is called at most once, by the owner, when metadata
// becomes known for a connection that started before it did (the
// magnet-link path). It publishes the real NumPieces/PieceLength/
// TotalLength for every future validation on this connection, and applies
// whatever Bitfield or HaveAll the peer sent before there was anything to
// validate it against — see pendingBitfield's field comment for why that
// particular message can't just be re-requested if it was missed the first
// time.
func (c *Client) UpgradeMetadata(info TorrentInfo) error {
	c.torrentInfo.Store(&info)

	c.bitfieldMu.Lock()
	defer c.bitfieldMu.Unlock()

	if c.pendingHaveAll {
		c.pendingHaveAll = false
		c.pendingBitfield = nil
		c.bitfield = bitfield.Full(info.NumPieces)
		return nil
	}

	raw := c.pendingBitfield
	c.pendingBitfield = nil
	if raw == nil {
		// No Bitfield/HaveAll ever arrived — some clients skip it when they
		// have nothing to offer yet (or sent HaveNone, which needs no
		// pending state: an empty bitfield is already what that declares).
		// Resize to the now-known width so a future Have has a
		// correctly-sized bitfield to set a bit in.
		c.bitfield = bitfield.New(info.NumPieces)
		return nil
	}
	bf, err := bitfield.FromBytes(raw, info.NumPieces)
	if err != nil {
		return fmt.Errorf("pending bitfield is invalid against the now-known piece count: %w", err)
	}
	c.bitfield = bf
	return nil
}

// HasPiece reports whether the peer has advertised the given piece.
func (c *Client) HasPiece(index uint32) bool {
	c.bitfieldMu.RLock()
	defer c.bitfieldMu.RUnlock()
	return c.bitfield.Has(int(index))
}

func (c *Client) setPiece(index uint32) {
	c.bitfieldMu.Lock()
	c.bitfield.Set(int(index))
	c.bitfieldMu.Unlock()
}

// setBitfield replaces the peer's bitfield wholesale. It reports false if the
// payload is not exactly the expected width, which is a protocol violation.
func (c *Client) setBitfield(raw []byte) error {
	c.bitfieldMu.Lock()
	defer c.bitfieldMu.Unlock()
	return c.bitfield.CopyFrom(raw)
}

// BitfieldSnapshot returns a copy of what the peer has advertised, for the
// availability index to fold in and out.
func (c *Client) BitfieldSnapshot() *bitfield.Bitfield {
	c.bitfieldMu.RLock()
	defer c.bitfieldMu.RUnlock()
	return c.bitfield.Clone()
}

// notify pushes an Event to whoever owns this Client. It only ever runs on
// the read-loop goroutine (Run and the handleMessage it calls), so it is the
// single producer that makes closing Events from a deferred call in Run safe.
//
// The send is non-blocking: dropping an event under extreme backpressure
// degrades a heuristic (the availability index used for rarest-first) rather
// than breaking correctness, since HasPiece already reflects every Have and
// Bitfield synchronously for whoever calls it directly.
func (c *Client) notify(ev Event) {
	select {
	case c.Events <- ev:
	default:
		logger.Logf("Peer %s: event queue full, dropping %s\n", c.Conn.RemoteAddr(), ev.Kind)
	}
}

// --- lifecycle -------------------------------------------------------------

// Close shuts the connection down and unblocks every goroutine attached to it.
// It is safe to call from any goroutine and any number of times.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		if c.Conn != nil {
			err = c.Conn.Close()
		}
	})
	return err
}

// Run is the main read loop for a peer connection. It owns the only read on
// Conn and returns when the connection dies or the peer misbehaves.
func (c *Client) Run() {
	// Ordered so the writers stop before the owner sees Results/Events close.
	defer close(c.Events)
	defer close(c.Results)
	defer close(c.MetadataPieces)
	defer close(c.PEXUpdates)
	defer c.Close()

	logger.Logf("Starting communication loop for peer %s\n", c.Conn.RemoteAddr())

	go c.sendLoop()
	go c.writeLoop()

	if err := c.SendInterested(); err != nil {
		logger.Logf("Error sending Interested to %s: %v\n", c.Conn.RemoteAddr(), err)
		return
	}
	if err := c.sendInitialState(); err != nil {
		// Not fatal: BEP 3 already tolerates a peer that never sends its
		// initial state at all, so this is no worse than that.
		logger.Logf("Error sending initial piece state to %s: %v\n", c.Conn.RemoteAddr(), err)
	}
	if c.peerSupportsExt {
		if err := c.sendExtendedHandshake(); err != nil {
			// Not fatal to the connection: we just won't get metadata or any
			// other extension traffic from this peer.
			logger.Logf("Error sending extended handshake to %s: %v\n", c.Conn.RemoteAddr(), err)
		}
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

		if !c.handleMessage(msg) {
			return
		}
	}
}

// handleMessage processes one wire message. It returns false when the peer has
// violated the protocol and the connection must be torn down.
func (c *Client) handleMessage(msg *Message) bool {
	switch msg.ID {
	case MsgChoke:
		c.peerChoking.Store(true)
		logger.Logf("Peer %s choked us.\n", c.Conn.RemoteAddr())
		c.notify(Event{Kind: EventChokeChanged})

	case MsgUnchoke:
		c.peerChoking.Store(false)
		logger.Logf("Peer %s unchoked us.\n", c.Conn.RemoteAddr())
		c.notify(Event{Kind: EventChokeChanged})

	case MsgInterested:
		// Note: we deliberately do NOT unchoke here. The choker is the sole
		// authority on AmChoking; an ad-hoc unchoke on this path would
		// permanently disagree with it.
		c.peerInterested.Store(true)
		logger.Logf("Peer %s is now interested in us.\n", c.Conn.RemoteAddr())
		c.notify(Event{Kind: EventInterestedChanged})

	case MsgNotInterested:
		c.peerInterested.Store(false)
		logger.Logf("Peer %s is no longer interested in us.\n", c.Conn.RemoteAddr())
		c.notify(Event{Kind: EventInterestedChanged})

	case MsgHave:
		var havePayload MsgHavePayload
		if err := havePayload.Parse(msg.Payload); err != nil {
			logger.Warning.Printf("Peer %s: malformed Have: %v\n", c.Conn.RemoteAddr(), err)
			return false
		}
		// NumPieces == 0 means our own metadata is not known yet (the
		// magnet-link path before BEP 9 completes). There is no range to
		// validate against and no bitfield of ours to update, but the event
		// still fires: the owner may care that the peer has pieces at all.
		// Unlike Bitfield, an early Have isn't cached for UpgradeMetadata to
		// replay — a peer completing a piece during the brief metadata-fetch
		// window goes unrecorded until its next Have or a fresh Bitfield.
		// Accepted as a narrow, self-healing gap rather than a second cache.
		if c.info().NumPieces == 0 {
			c.notify(Event{Kind: EventHave, PieceIndex: havePayload.PieceIndex})
			break
		}
		if int64(havePayload.PieceIndex) >= int64(c.info().NumPieces) {
			logger.Warning.Printf("Peer %s: Have for out-of-range piece %d\n", c.Conn.RemoteAddr(), havePayload.PieceIndex)
			return false
		}
		c.setPiece(havePayload.PieceIndex)
		c.notify(Event{Kind: EventHave, PieceIndex: havePayload.PieceIndex})

	case MsgBitfield:
		// With no metadata yet, our bitfield is necessarily zero-width, so
		// any real peer's Bitfield message is unvalidatable rather than
		// invalid — cache the raw bytes for UpgradeMetadata to apply once
		// metadata (and a real width to check them against) exists.
		if c.info().NumPieces == 0 {
			c.bitfieldMu.Lock()
			c.pendingBitfield = append([]byte(nil), msg.Payload...)
			c.bitfieldMu.Unlock()
			c.notify(Event{Kind: EventBitfield})
			break
		}
		if err := c.setBitfield(msg.Payload); err != nil {
			logger.Warning.Printf("Peer %s: rejected Bitfield: %v\n", c.Conn.RemoteAddr(), err)
			return false
		}
		c.notify(Event{Kind: EventBitfield})

	case MsgPiece:
		var piecePayload MsgPiecePayload
		if err := piecePayload.Parse(msg.Payload); err != nil {
			logger.Warning.Printf("Peer %s: malformed Piece: %v\n", c.Conn.RemoteAddr(), err)
			return false
		}
		if err := c.validateBlock(piecePayload.Index, piecePayload.Begin, uint32(len(piecePayload.Block))); err != nil {
			logger.Warning.Printf("Peer %s: rejected Piece: %v\n", c.Conn.RemoteAddr(), err)
			return false
		}
		// Throttling here, before the block is handed off, delays this
		// connection's next ReadMessage call — the natural way to cap
		// download rate without a separate reader goroutine or buffering
		// scheme, since nothing else reads from this peer meanwhile.
		if !waitAll(c.limits.Down, c.done, len(piecePayload.Block)) {
			return false
		}
		c.lastPieceReceived.Store(time.Now().Unix())
		select {
		case c.Results <- &PieceBlock{
			Index: piecePayload.Index,
			Begin: piecePayload.Begin,
			Block: piecePayload.Block,
		}:
		case <-c.done:
			return false
		}

	case MsgRequest:
		var reqPayload MsgRequestPayload
		if err := reqPayload.Parse(msg.Payload); err != nil {
			logger.Warning.Printf("Peer %s: malformed Request: %v\n", c.Conn.RemoteAddr(), err)
			return false
		}
		if err := c.validateRequest(reqPayload.Index, reqPayload.Begin, reqPayload.Length); err != nil {
			logger.Warning.Printf("Peer %s: rejected Request: %v\n", c.Conn.RemoteAddr(), err)
			return false
		}
		c.serveRequest(reqPayload)

	case MsgHaveAll:
		if len(msg.Payload) != 0 {
			logger.Warning.Printf("Peer %s: HaveAll with a non-empty payload\n", c.Conn.RemoteAddr())
			return false
		}
		c.applyHaveAll()

	case MsgHaveNone:
		if len(msg.Payload) != 0 {
			logger.Warning.Printf("Peer %s: HaveNone with a non-empty payload\n", c.Conn.RemoteAddr())
			return false
		}
		// Nothing to apply: bitfield.New (or whatever UpgradeMetadata builds
		// once NumPieces is known, absent any pending state) is already the
		// all-empty state HaveNone declares. Still notify: the owner may
		// care that the peer's initial state has arrived at all.
		c.notify(Event{Kind: EventBitfield})

	case MsgSuggestPiece:
		var p MsgHavePayload
		if err := p.Parse(msg.Payload); err != nil {
			logger.Warning.Printf("Peer %s: malformed SuggestPiece: %v\n", c.Conn.RemoteAddr(), err)
			return false
		}
		// Advisory only (BEP 6: a peer "MAY" act on this disk-cache-locality
		// hint). Parsed and validated so a malformed one still drops the
		// connection like any other malformed message, but otherwise
		// intentionally ignored — this client has no cache-locality signal
		// of its own to weigh it against.

	case MsgAllowedFast:
		var p MsgHavePayload
		if err := p.Parse(msg.Payload); err != nil {
			logger.Warning.Printf("Peer %s: malformed AllowedFast: %v\n", c.Conn.RemoteAddr(), err)
			return false
		}
		c.markAllowedFast(p.PieceIndex)

	case MsgRejectRequest:
		var p MsgRequestPayload
		if err := p.Parse(msg.Payload); err != nil {
			logger.Warning.Printf("Peer %s: malformed RejectRequest: %v\n", c.Conn.RemoteAddr(), err)
			return false
		}
		c.notify(Event{Kind: EventRejectRequest, PieceIndex: p.Index, Begin: p.Begin, Length: p.Length})

	case MsgExtended:
		if len(msg.Payload) < 1 {
			logger.Warning.Printf("Peer %s: empty Extended message\n", c.Conn.RemoteAddr())
			return false
		}
		extID, body := msg.Payload[0], msg.Payload[1:]
		var err error
		switch {
		case extID == 0:
			err = c.handleExtendedHandshake(body)
		case int(extID) == localUtMetadataID:
			err = c.handleUtMetadataMessage(body)
		case int(extID) == localUtPexID:
			err = c.handleUtPexMessage(body)
		case int(extID) == localUploadOnlyID:
			err = c.handleUploadOnlyMessage(body)
		default:
			// An extended id for something we didn't advertise support for —
			// either a stale id from before a renegotiation, or the peer
			// sending garbage. Neither is a protocol violation worth
			// dropping the connection over.
			logger.Logf("Peer %s: extended message for unknown id %d, ignoring\n", c.Conn.RemoteAddr(), extID)
		}
		if err != nil {
			logger.Warning.Printf("Peer %s: %v\n", c.Conn.RemoteAddr(), err)
			return false
		}
	}

	return true
}

// serveRequest answers a validated Request from a peer, if we are willing.
func (c *Client) serveRequest(req MsgRequestPayload) {
	if c.AmChoking() || c.hasPiece == nil || !c.hasPiece(req.Index) {
		logger.Logf("Ignoring request from peer %s for piece %d (we don't have it or we are choking them).\n",
			c.Conn.RemoteAddr(), req.Index)
		// A peer without Fast extension support gets silence, per BEP 3's
		// original (and still valid) behavior — they have no RejectRequest
		// to understand anyway. A Fast-capable peer gets an explicit
		// answer instead of waiting out its own request timeout.
		if c.peerSupportsFast {
			if err := c.SendMessage(MsgRejectRequest, req.Serialize()); err != nil {
				logger.Logf("Peer %s: failed to send RejectRequest: %v\n", c.Conn.RemoteAddr(), err)
			}
		}
		return
	}

	logger.Logf("Peer %s requested piece %d, offset %d, length %d\n",
		c.Conn.RemoteAddr(), req.Index, req.Begin, req.Length)

	blockData, err := c.readBlockFromDisk(req.Index, req.Begin, req.Length)
	if err != nil {
		logger.Error.Printf("Error reading block from disk for peer request: %v\n", err)
		return
	}
	if err := c.SendPiece(req.Index, req.Begin, blockData); err != nil {
		logger.Warning.Printf("Error sending Piece message to peer %s: %v\n", c.Conn.RemoteAddr(), err)
		return
	}
	c.uploaded.Add(int64(len(blockData)))
	logger.Logf("Sent piece %d, block offset %d to peer %s\n", req.Index, req.Begin, c.Conn.RemoteAddr())
}

// Uploaded returns the cumulative bytes served to this peer, for the owner's
// aggregate upload accounting (torrent.Stats, resume data, tracker announce).
func (c *Client) Uploaded() int64 { return c.uploaded.Load() }

// validateRequest rejects a peer request before its length ever reaches
// make([]byte, n). An unvalidated length here is a remote allocation
// primitive, so a bad request costs the peer its connection.
func (c *Client) validateRequest(index, begin, length uint32) error {
	if length == 0 || length > MaxBlockLength {
		return fmt.Errorf("request length %d outside (0, %d]", length, MaxBlockLength)
	}
	return c.validateBlock(index, begin, length)
}

// validateBlock checks that [begin, begin+length) lies inside piece index.
func (c *Client) validateBlock(index, begin, length uint32) error {
	info := c.info()
	if int64(index) >= int64(info.NumPieces) {
		return fmt.Errorf("piece index %d out of range (%d pieces)", index, info.NumPieces)
	}
	pieceLen := info.PieceLen(index)
	// uint64 arithmetic so begin+length cannot wrap.
	if uint64(begin)+uint64(length) > uint64(pieceLen) {
		return fmt.Errorf("block [%d,%d) exceeds piece %d length %d",
			begin, uint64(begin)+uint64(length), index, pieceLen)
	}
	return nil
}

// writeLoop turns assigned work into Request messages. It exits on done, so a
// disconnect no longer leaks a goroutine per peer.
func (c *Client) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case work := <-c.WorkQueue:
			// If we are choked and the peer hasn't granted this specific
			// piece via BEP 6's AllowedFast, drop the request — the owner
			// times the block out and re-assigns it, so dropping is cheaper
			// than stalling. An allowed-fast piece is sent through despite
			// the choke; that is the entire point of the grant.
			if c.PeerChoking() && !c.IsAllowedFast(work.Index) {
				continue
			}
			if !c.HasPiece(work.Index) {
				logger.Logf("Peer %s: was assigned work for piece %d it doesn't have.\n", c.Conn.RemoteAddr(), work.Index)
				continue
			}
			if err := c.SendRequest(work.Index, work.Begin, work.Length); err != nil {
				logger.Warning.Printf("Peer %s: failed to send request: %v\n", c.Conn.RemoteAddr(), err)
			}
		}
	}
}

// sendLoop is the only goroutine that ever writes to Conn. Serializing writes
// here is what stops a large Piece frame from being interleaved with a Have
// broadcast and arriving as garbage on the far end.
func (c *Client) sendLoop() {
	for {
		select {
		case <-c.done:
			return
		case frame := <-c.outbound:
			if !waitAll(c.limits.Up, c.done, len(frame)) {
				return
			}
			if err := c.Conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				c.Close()
				return
			}
			if _, err := c.Conn.Write(frame); err != nil {
				logger.Warning.Printf("Peer %s: write failed, closing: %v\n", c.Conn.RemoteAddr(), err)
				c.Close()
				return
			}
		}
	}
}

// --- sending ---------------------------------------------------------------

// ReadMessage reads and parses a single message from the peer.
func (c *Client) ReadMessage() (*Message, error) {
	c.Conn.SetReadDeadline(time.Now().Add(readTimeout))
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
	if length > maxMessageLength {
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

// SendMessage serializes a message and queues it for the writer goroutine.
func (c *Client) SendMessage(id MessageID, payload []byte) error {
	msg := &Message{ID: id, Payload: payload}
	if err := c.send(msg.Serialize()); err != nil {
		return fmt.Errorf("failed to send message ID %s: %w", id, err)
	}
	return nil
}

// send hands a fully serialized frame to sendLoop. It never blocks: the
// session broadcasts Have while holding its mutex, so one slow peer must not
// be able to stall the whole download.
func (c *Client) send(frame []byte) error {
	select {
	case <-c.done:
		return ErrClientClosed
	default:
	}
	select {
	case c.outbound <- frame:
		return nil
	default:
		return ErrOutboundFull
	}
}

// Helper methods for sending specific messages
func (c *Client) SendInterested() error {
	c.amInterested.Store(true)
	return c.SendMessage(MsgInterested, nil)
}

func (c *Client) SendNotInterested() error {
	c.amInterested.Store(false)
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

// SendCancel withdraws a previously sent Request. BEP 3 gives Cancel the
// same payload layout as Request, so MsgRequestPayload is reused rather than
// defining an identical type.
func (c *Client) SendCancel(index, begin, length uint32) error {
	payload := MsgRequestPayload{Index: index, Begin: begin, Length: length}
	return c.SendMessage(MsgCancel, payload.Serialize())
}

func (c *Client) SendPiece(index, begin uint32, block []byte) error {
	payload := MsgPiecePayload{Index: index, Begin: begin, Block: block}
	return c.SendMessage(MsgPiece, payload.Serialize())
}

// SendChoke chokes the peer. It owns the AmChoking flag so that the flag and
// the wire message can never disagree.
func (c *Client) SendChoke() error {
	c.amChoking.Store(true)
	return c.SendMessage(MsgChoke, nil)
}

// SendUnchoke unchokes the peer and owns the AmChoking flag.
func (c *Client) SendUnchoke() error {
	c.amChoking.Store(false)
	return c.SendMessage(MsgUnchoke, nil)
}
