package utp

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// Known, deliberate v1 simplifications, stated up front rather than
// discovered mid-review:
//   - One retransmit timer covers the oldest outstanding packet ("go back
//     1"), not a per-packet timer — simpler, and still correct, just a
//     little slower to recover from a loss deep in a large window than a
//     more elaborate scheme would be.
//   - Selective ACKs advance which individual packets are considered
//     acked (so they're never needlessly retransmitted), but there is no
//     duplicate-ACK-triggered fast retransmit — recovery for the one
//     genuinely missing packet still goes through the RTO timer. A real,
//     documented gap, not an oversight: implementing fast retransmit
//     correctly is real added complexity for a secondary optimization,
//     not the core correctness or congestion-control behavior this
//     feature exists for.
//   - The advertised receive window is a fixed, generous constant, not
//     computed from actual read-buffer occupancy — this implementation's
//     own bound against a hostile/broken peer is the reorder buffer's own
//     size cap, not receiver-side flow control.
//   - No periodic keepalive packets to hold a NAT mapping open on an idle
//     connection.

const (
	// maxReorderPackets bounds how many out-of-order packets this side
	// will buffer for one connection before refusing more — protects
	// against a peer (malicious or broken) parking this connection's
	// memory usage on an unbounded gap.
	maxReorderPackets = 1024
	// advertisedWindow is the fixed wnd_size this implementation sends —
	// see the package-level simplifications note above.
	advertisedWindow = 1 << 20
	// closeLinger is how long a *Conn stays registered with its Socket
	// after both directions have finished closing, so a last, delayed
	// retransmission of the peer's own FIN/ACK still finds a real
	// connection to answer rather than triggering a spurious RESET.
	closeLinger = 3 * time.Second
)

// synRetries is how many times performHandshake resends ST_SYN before
// giving up — a var, not a const, so a test proving the "peer never
// replies" failure path doesn't have to genuinely wait out several real
// RTO-doubling rounds (the same "interval is a var so tests can shrink
// it" convention internal/mse.HandshakeTimeout and internal/torrent's
// pexInterval already use).
var synRetries = 4

var (
	ErrConnClosed  = errors.New("utp: connection closed")
	ErrConnRefused = errors.New("utp: connection refused (RESET)")
	ErrConnTimeout = errors.New("utp: connection attempt timed out")
)

type connState int

const (
	stateConnecting connState = iota
	stateConnected
	stateClosing // we've sent FIN; waiting for the peer's own close
	stateClosed
)

// sendEntry is one outstanding (sent, not yet acked) packet.
type sendEntry struct {
	pkt           *Packet
	sentAt        time.Time
	retransmitted bool // Karn's algorithm: never sample RTT from this one
}

// Conn is one µTP connection — a net.Conn. Exactly one goroutine (run)
// owns every mutable field below the "actor state" marker; Read/Write/
// Close/deadline setters only ever touch the channel-based primitives
// beneath the "external API" marker, the same "one actor goroutine,
// external callers only touch channels" shape internal/torrent.Torrent's
// own actor already establishes.
type Conn struct {
	sock       *Socket
	remoteAddr *net.UDPAddr
	recvID     uint16
	sendID     uint16
	initiator  bool

	// --- external API: safe to touch from any goroutine ---
	writeCh   chan []byte
	readCh    chan []byte
	recvCh    chan *Packet // fed by Socket's demux loop
	closeCh   chan struct{}
	closeOnce sync.Once

	// closeErr is written from both the actor goroutine (a clean FIN, a
	// RESET, or generic teardown) and directly by Close() itself (called
	// from whatever goroutine owns this Conn) - the one piece of "actor
	// state" that isn't actually actor-exclusive, so it gets its own lock
	// rather than living under the "only run() touches this" rule
	// everything else here follows.
	closeErrMu sync.Mutex
	closeErr   error

	connectedCh chan struct{} // closed once the handshake resolves (success or failure)
	connectErr  error         // valid only after connectedCh closes

	deadlineMu    sync.Mutex
	readDeadline  time.Time
	writeDeadline time.Time

	readLeftover []byte // bytes from a previous readCh receive the caller's buffer didn't fully consume

	// --- actor state: only run() and its own callees touch these ---
	state   connState
	seqNr   uint16 // next seq_nr to use for a packet that consumes sequence space (SYN/DATA/FIN)
	ackNr   uint16 // highest seq_nr received in-order and fully processed
	haveAck bool   // whether ackNr means anything yet (false only before a SYN/first packet is processed)

	// sendBuf holds every sent-but-not-yet-acked packet, keyed by seq_nr.
	// An entry SACKed out of order is removed from here immediately (see
	// ackEntry), which is what keeps the "go back 1" retransmit timer
	// (handleRTO) from ever re-sending something the peer already has:
	// it only ever looks at what's still actually in this map.
	sendBuf map[uint16]*sendEntry

	reorder map[uint16]*Packet // out-of-order received DATA packets awaiting the gap

	// readChClosed guards against closing readCh twice: once as soon as
	// we know no more inbound data will ever arrive (a clean FIN, fully
	// processed in order — see closeReadSide), and, as a fallback for
	// every other teardown path (RESET, our own Close(), a lost
	// connection), again in teardown. Only ever touched by the actor
	// goroutine, so a plain bool suffices.
	readChClosed bool

	pendingWrite []byte // leftover from writeCh not yet packetized

	cachedDiff uint32 // timestamp_difference_microseconds to embed in our next outgoing packet — see doc.go

	lc       *ledbat
	rto      *rtoEstimator
	rtoTimer *time.Timer

	finSeq     uint16 // valid once we've sent our own FIN
	haveFinSeq bool
	peerFin    bool   // the peer has sent us a FIN
	peerFinSeq uint16 // valid once peerFin is true — its own seq_nr, so we know once ackNr has actually reached it (it may itself arrive out of order and sit in reorder for a while)
}

func randomUint16() uint16 {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint16(b[:])
}

func newConn(sock *Socket, remoteAddr *net.UDPAddr, recvID, sendID uint16, initiator bool) *Conn {
	c := &Conn{
		sock:        sock,
		remoteAddr:  remoteAddr,
		recvID:      recvID,
		sendID:      sendID,
		initiator:   initiator,
		writeCh:     make(chan []byte),
		readCh:      make(chan []byte, 64),
		recvCh:      make(chan *Packet, 64),
		closeCh:     make(chan struct{}),
		connectedCh: make(chan struct{}),
		sendBuf:     make(map[uint16]*sendEntry),
		reorder:     make(map[uint16]*Packet),
		lc:          newLedbat(),
		rto:         newRTOEstimator(),
	}
	c.seqNr = randomUint16()
	if c.seqNr == 0 {
		c.seqNr = 1
	}
	return c
}

// --- net.Conn ---------------------------------------------------------

func (c *Conn) Read(p []byte) (int, error) {
	if len(c.readLeftover) > 0 {
		n := copy(p, c.readLeftover)
		c.readLeftover = c.readLeftover[n:]
		return n, nil
	}

	timer, stop := c.deadlineTimer(c.getReadDeadline())
	defer stop()

	select {
	case chunk, ok := <-c.readCh:
		if !ok {
			return 0, c.closedErrOrEOF()
		}
		n := copy(p, chunk)
		if n < len(chunk) {
			c.readLeftover = chunk[n:]
		}
		return n, nil
	case <-c.closeCh:
		// Drain anything already queued before reporting closed/EOF, so a
		// FIN racing the last real data never loses bytes already
		// delivered by the actor.
		select {
		case chunk, ok := <-c.readCh:
			if ok {
				n := copy(p, chunk)
				if n < len(chunk) {
					c.readLeftover = chunk[n:]
				}
				return n, nil
			}
		default:
		}
		return 0, c.closedErrOrEOF()
	case <-timer:
		return 0, errTimeout{}
	}
}

func (c *Conn) closedErrOrEOF() error {
	if err := c.getCloseErr(); err != nil && err != io.EOF {
		return err
	}
	return io.EOF
}

// setCloseErrOnce records err as the connection's close reason, but only
// if nothing has claimed that slot yet — whichever of the actor's several
// possible close reasons (clean FIN, RESET, generic teardown) or a
// direct Close() call gets there first wins, matching every other
// "closed exactly once, for one reason" primitive throughout this type.
func (c *Conn) setCloseErrOnce(err error) {
	c.closeErrMu.Lock()
	defer c.closeErrMu.Unlock()
	if c.closeErr == nil {
		c.closeErr = err
	}
}

func (c *Conn) getCloseErr() error {
	c.closeErrMu.Lock()
	defer c.closeErrMu.Unlock()
	return c.closeErr
}

func (c *Conn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	buf := append([]byte(nil), p...)

	timer, stop := c.deadlineTimer(c.getWriteDeadline())
	defer stop()

	select {
	case c.writeCh <- buf:
		return len(p), nil
	case <-c.closeCh:
		return 0, ErrConnClosed
	case <-timer:
		return 0, errTimeout{}
	}
}

func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		// Distinct from the io.EOF the actor sets on a clean peer-
		// initiated FIN (see closeReadSide's own callers) - this side
		// asked to close, so its own further Read/Write calls should
		// report that plainly rather than claim the peer was the one
		// who finished. setCloseErrOnce so a close reason the actor
		// already recorded (e.g. we're racing a real FIN that arrived
		// moments before) is never overwritten.
		c.setCloseErrOnce(ErrConnClosed)
		close(c.closeCh)
	})
	return nil
}

func (c *Conn) LocalAddr() net.Addr  { return c.sock.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr { return c.remoteAddr }

func (c *Conn) SetDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.readDeadline, c.writeDeadline = t, t
	c.deadlineMu.Unlock()
	return nil
}

func (c *Conn) SetReadDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.readDeadline = t
	c.deadlineMu.Unlock()
	return nil
}

func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.writeDeadline = t
	c.deadlineMu.Unlock()
	return nil
}

func (c *Conn) getReadDeadline() time.Time {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	return c.readDeadline
}

func (c *Conn) getWriteDeadline() time.Time {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	return c.writeDeadline
}

// deadlineTimer returns a channel that fires at deadline (or never, for a
// zero deadline) plus a stop function the caller must always invoke.
func (c *Conn) deadlineTimer(deadline time.Time) (<-chan time.Time, func()) {
	if deadline.IsZero() {
		return nil, func() {}
	}
	d := time.Until(deadline)
	if d <= 0 {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch, func() {}
	}
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}

// errTimeout satisfies net.Error the way every real net.Conn deadline
// timeout does, so callers using the standard "is this a timeout"
// type-assertion pattern (e.g. net/http's own transport) work unchanged.
type errTimeout struct{}

func (errTimeout) Error() string   { return "utp: i/o timeout" }
func (errTimeout) Timeout() bool   { return true }
func (errTimeout) Temporary() bool { return true }
