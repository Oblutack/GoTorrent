package ws

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// ErrClosed is returned by ReadMessage/WriteMessage once the connection has
// been closed, whether by the peer's close frame, a local Close call, or a
// read/write failure.
var ErrClosed = errors.New("ws: connection closed")

// outboundQueueSize bounds how many not-yet-written frames Conn will queue
// before send starts dropping — small on purpose: this is a live push
// stream (4.2's event feed), where a slow subscriber missing a message is
// far better than one slow browser tab stalling delivery to every other
// connected client, the same non-blocking-send reasoning peer.Client's own
// Events channel already uses.
const outboundQueueSize = 32

// Conn is one upgraded WebSocket connection. Exactly one goroutine writes
// to the underlying net.Conn (sendLoop, draining outbound) and exactly one
// reads (whichever goroutine calls ReadMessage) — the same one-reader/
// one-writer contract internal/peer.Client documents for the same reason:
// concurrent writes to a single net.Conn interleave and corrupt the frame
// stream.
type Conn struct {
	conn       net.Conn
	r          *bufio.Reader
	remoteAddr string

	outbound  chan []byte
	done      chan struct{}
	closeOnce sync.Once
	closed    atomic.Bool
}

// newConn takes ownership of c and r. r must be the *bufio.Reader Hijack
// itself returned (wrapped in a *bufio.ReadWriter), never a fresh one
// built from c directly — Hijack's ReadWriter may already have buffered
// bytes the client sent right after the handshake request, in the same
// TCP segment; a fresh bufio.Reader over the raw net.Conn would silently
// skip past them, corrupting the very first frame read.
func newConn(c net.Conn, r *bufio.Reader) *Conn {
	conn := &Conn{
		conn:       c,
		r:          r,
		remoteAddr: remoteAddrString(c),
		outbound:   make(chan []byte, outboundQueueSize),
		done:       make(chan struct{}),
	}
	go conn.sendLoop()
	return conn
}

// RemoteAddr is the underlying connection's remote address, fixed at
// construction (safe to read from any goroutine without synchronization).
func (c *Conn) RemoteAddr() string { return c.remoteAddr }

// sendLoop is the only goroutine that writes to conn, and the only one
// that ever calls conn.Close (not Close itself — see its own doc comment
// on why closing the socket is deferred to here). On <-c.done it drains
// whatever is already queued before closing the socket: Close is commonly
// called immediately after one last WriteMessage/sendFrame (an event
// stream ending, a close-frame echo just having been queued by
// ReadMessage), and since send only enqueues rather than writing
// synchronously, closing the raw socket immediately would race that write
// and silently drop it.
func (c *Conn) sendLoop() {
	for {
		select {
		case data := <-c.outbound:
			if _, err := c.conn.Write(data); err != nil {
				c.closed.Store(true)
				c.conn.Close()
				return
			}
		case <-c.done:
			for {
				select {
				case data := <-c.outbound:
					c.conn.Write(data)
				default:
					c.conn.Close()
					return
				}
			}
		}
	}
}

// send enqueues a pre-serialized frame, non-blocking — see outboundQueueSize.
func (c *Conn) send(data []byte) error {
	if c.closed.Load() {
		return ErrClosed
	}
	select {
	case c.outbound <- data:
		return nil
	default:
		return fmt.Errorf("ws: outbound queue full for %s, dropping frame", c.remoteAddr)
	}
}

func (c *Conn) sendFrame(fin bool, opcode Opcode, payload []byte) error {
	var buf bytes.Buffer
	if err := writeFrame(&buf, fin, opcode, payload); err != nil {
		return err
	}
	return c.send(buf.Bytes())
}

// WriteMessage sends payload as one complete, unfragmented text or binary
// message (opcode must be OpText or OpBinary).
func (c *Conn) WriteMessage(opcode Opcode, payload []byte) error {
	if opcode != OpText && opcode != OpBinary {
		return fmt.Errorf("ws: WriteMessage opcode must be OpText or OpBinary, got %#x", opcode)
	}
	return c.sendFrame(true, opcode, payload)
}

// WriteJSON is a convenience most callers of this package actually want:
// 4.2's event stream is JSON text frames end to end.
func (c *Conn) WriteJSON(data []byte) error {
	return c.WriteMessage(OpText, data)
}

// Ping sends a ping frame; the peer is expected to answer with a pong,
// which ReadMessage consumes internally rather than surfacing it — callers
// that care about liveness watch for ReadMessage returning an error
// instead of watching for pongs themselves.
func (c *Conn) Ping() error {
	return c.sendFrame(true, OpPing, nil)
}

// ReadMessage blocks until one complete text or binary message has been
// reassembled from the wire, returning ErrClosed once the connection is
// closed (a close frame from the peer, a local Close, or a read failure).
// Ping/pong and close frames are handled internally — a ping is answered
// with a pong automatically, and a close frame triggers the close
// handshake — so a caller only ever sees the two message opcodes it
// actually has to do something with. Fragmented messages (fin=false
// followed by one or more continuation frames) are reassembled up to
// maxFramePayload in total; anything larger is a protocol error.
func (c *Conn) ReadMessage() (Opcode, []byte, error) {
	var assembling bool
	var messageOpcode Opcode
	var buf []byte

	for {
		if c.closed.Load() {
			return 0, nil, ErrClosed
		}
		f, err := readFrame(c.r)
		if err != nil {
			c.Close()
			return 0, nil, err
		}

		switch f.opcode {
		case OpPing:
			if err := c.sendFrame(true, OpPong, f.payload); err != nil {
				c.Close()
				return 0, nil, err
			}
			continue
		case OpPong:
			continue
		case OpClose:
			// Echo the close frame back (RFC 6455 section 5.5.1's required
			// handshake) and report closure to the caller.
			c.sendFrame(true, OpClose, f.payload)
			c.Close()
			return 0, nil, ErrClosed
		case OpText, OpBinary:
			if assembling {
				c.Close()
				return 0, nil, fmt.Errorf("ws: got a new message opcode %#x mid-fragmented-message", f.opcode)
			}
			messageOpcode = f.opcode
			buf = f.payload
			if f.fin {
				return messageOpcode, buf, nil
			}
			assembling = true
		case OpContinuation:
			if !assembling {
				c.Close()
				return 0, nil, fmt.Errorf("ws: continuation frame with no message in progress")
			}
			if len(buf)+len(f.payload) > maxFramePayload {
				c.Close()
				return 0, nil, fmt.Errorf("ws: reassembled message exceeds the %d byte limit", maxFramePayload)
			}
			buf = append(buf, f.payload...)
			if f.fin {
				return messageOpcode, buf, nil
			}
		default:
			c.Close()
			return 0, nil, fmt.Errorf("ws: unknown opcode %#x", f.opcode)
		}
	}
}

// Close stops accepting new sends and signals sendLoop to drain whatever
// is already queued, then close the underlying connection — see sendLoop's
// own doc comment for why the actual socket close happens there rather
// than here. Safe to call more than once and from any goroutine; does not
// block waiting for the drain to finish.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.done)
	})
	return nil
}
