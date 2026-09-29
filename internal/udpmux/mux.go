// Package udpmux lets more than one protocol share a single bound UDP
// port — the real-world requirement internal/engine needs to satisfy so
// DHT and µTP (internal/utp) can coexist on the exact same port number a
// client already advertises for inbound TCP connections, since a peer
// discovering that address via a tracker, DHT, or PEX will try µTP on
// that identical port, not a different one.
package udpmux

import (
	"errors"
	"net"
	"sync"
	"time"
)

// ErrClosed is returned by a facade's ReadFrom/WriteTo once its Mux (or
// the facade itself) has been closed.
var ErrClosed = errors.New("udpmux: closed")

// MatchFunc classifies one incoming datagram's raw bytes, deciding
// whether a given consumer wants it.
type MatchFunc func(data []byte) bool

// Mux wraps one real net.PacketConn and demultiplexes it across any
// number of consumers, each registered via For with its own MatchFunc.
// Exactly one goroutine (run) ever reads from the real underlying
// net.PacketConn; every registered consumer gets its own facade fed by a
// private channel — the same "one real reader, N logical consumers"
// shape a Go channel-fanout naturally provides.
type Mux struct {
	pc net.PacketConn

	mu        sync.Mutex
	consumers []*Conn

	closeCh   chan struct{}
	closeOnce sync.Once
}

// New wraps an already-bound net.PacketConn (a real UDP socket, or — in a
// test — anything else implementing the interface) and starts demuxing it
// immediately.
func New(pc net.PacketConn) *Mux {
	m := &Mux{pc: pc, closeCh: make(chan struct{})}
	go m.run()
	return m
}

func (m *Mux) run() {
	buf := make([]byte, 65536)
	for {
		n, addr, err := m.pc.ReadFrom(buf)
		if err != nil {
			_ = m.Close()
			return
		}
		data := append([]byte(nil), buf[:n]...)

		m.mu.Lock()
		consumers := m.consumers
		m.mu.Unlock()

		for _, c := range consumers {
			if c.match(data) {
				c.deliver(data, addr)
				break // first match wins; see For's own doc comment
			}
		}
		// A datagram nothing claims is silently dropped - the same
		// tolerance any protocol running over UDP already has to have.
	}
}

// For returns a net.PacketConn facade that receives every datagram this
// Mux reads for which match returns true, tried in registration order
// (first match wins) — registered consumers are expected to be mutually
// exclusive by construction (e.g. DHT/KRPC's bencoded 'd'-prefixed
// datagrams vs µTP's own type/version byte can never both claim the same
// real packet), but the ordering rule exists so a caller can be explicit
// about precedence if that ever isn't strictly true.
func (m *Mux) For(match MatchFunc) net.PacketConn {
	c := &Conn{
		mux:     m,
		match:   match,
		msgCh:   make(chan datagram, 64),
		closeCh: make(chan struct{}),
	}
	m.mu.Lock()
	m.consumers = append(m.consumers, c)
	m.mu.Unlock()
	return c
}

// LocalAddr is the real underlying socket's bound address — every facade
// this Mux hands out shares it, since they're all really the same port.
func (m *Mux) LocalAddr() net.Addr { return m.pc.LocalAddr() }

// Close shuts the Mux down: the real underlying socket is closed (which
// unblocks run()), and every facade's own ReadFrom unblocks with
// ErrClosed.
func (m *Mux) Close() error {
	var err error
	m.closeOnce.Do(func() {
		err = m.pc.Close()
		close(m.closeCh)
	})
	return err
}

type datagram struct {
	data []byte
	addr net.Addr
}

// Conn is one Mux consumer's own net.PacketConn view onto the shared
// socket — reads only ever see datagrams this consumer's own MatchFunc
// claimed; writes pass straight through to the real shared socket, since
// outgoing traffic is never ambiguous.
type Conn struct {
	mux   *Mux
	match MatchFunc
	msgCh chan datagram

	closeCh   chan struct{}
	closeOnce sync.Once

	deadlineMu   sync.Mutex
	readDeadline time.Time
}

func (c *Conn) deliver(data []byte, addr net.Addr) {
	select {
	case c.msgCh <- datagram{data: data, addr: addr}:
	default:
		// This consumer is backed up - drop, the same tolerance a real
		// lost UDP datagram already requires every caller to handle.
	}
}

func (c *Conn) ReadFrom(p []byte) (int, net.Addr, error) {
	timer, stop := deadlineTimer(c.getReadDeadline())
	defer stop()
	select {
	case d := <-c.msgCh:
		n := copy(p, d.data)
		return n, d.addr, nil
	case <-c.closeCh:
		return 0, nil, ErrClosed
	case <-c.mux.closeCh:
		return 0, nil, ErrClosed
	case <-timer:
		return 0, nil, errTimeout{}
	}
}

func (c *Conn) WriteTo(p []byte, addr net.Addr) (int, error) {
	return c.mux.pc.WriteTo(p, addr)
}

func (c *Conn) Close() error {
	c.closeOnce.Do(func() { close(c.closeCh) })
	return nil
}

func (c *Conn) LocalAddr() net.Addr { return c.mux.LocalAddr() }

// SetDeadline/SetReadDeadline bound ReadFrom. SetWriteDeadline is a
// deliberate no-op: WriteTo never blocks at this layer (it passes
// straight through to the real shared socket), and neither of this
// package's own two callers (internal/dht, internal/utp) ever call it.
func (c *Conn) SetDeadline(t time.Time) error    { return c.SetReadDeadline(t) }
func (c *Conn) SetWriteDeadline(time.Time) error { return nil }
func (c *Conn) SetReadDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.readDeadline = t
	c.deadlineMu.Unlock()
	return nil
}

func (c *Conn) getReadDeadline() time.Time {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	return c.readDeadline
}

func deadlineTimer(deadline time.Time) (<-chan time.Time, func()) {
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

type errTimeout struct{}

func (errTimeout) Error() string   { return "udpmux: i/o timeout" }
func (errTimeout) Timeout() bool   { return true }
func (errTimeout) Temporary() bool { return true }
