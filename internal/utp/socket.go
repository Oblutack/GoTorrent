package utp

import (
	"context"
	"errors"
	"net"
	"sync"
)

// ErrSocketClosed is returned by Accept/DialContext once a Socket has
// been closed.
var ErrSocketClosed = errors.New("utp: socket closed")

type connKey struct {
	addr string
	id   uint16
}

// Socket owns one net.PacketConn and multiplexes it across every *Conn
// dialed or accepted through it — the UDP analogue of net.Listener, and
// (via DialContext, whose signature matches internal/peer.DialFunc
// exactly) a drop-in outbound dialer too.
type Socket struct {
	pc net.PacketConn

	mu     sync.Mutex
	conns  map[connKey]*Conn
	closed bool

	acceptCh  chan *Conn
	closeCh   chan struct{}
	closeOnce sync.Once
}

// NewSocket wraps an already-bound net.PacketConn — a real UDP socket, or
// (internal/engine's own inbound wiring) an internal/udpmux facade
// sharing one port with DHT.
func NewSocket(pc net.PacketConn) *Socket {
	s := &Socket{
		pc:       pc,
		conns:    make(map[connKey]*Conn),
		acceptCh: make(chan *Conn, 16),
		closeCh:  make(chan struct{}),
	}
	go s.readLoop()
	return s
}

func (s *Socket) LocalAddr() net.Addr { return s.pc.LocalAddr() }

func (s *Socket) readLoop() {
	buf := make([]byte, 65536)
	for {
		n, addr, err := s.pc.ReadFrom(buf)
		if err != nil {
			s.shutdown()
			return
		}
		pkt, err := Unmarshal(buf[:n])
		if err != nil {
			continue // drop garbage
		}
		udpAddr, ok := addr.(*net.UDPAddr)
		if !ok {
			continue
		}
		s.dispatch(pkt, udpAddr)
	}
}

func (s *Socket) dispatch(pkt *Packet, addr *net.UDPAddr) {
	key := connKey{addr: addr.String(), id: pkt.ConnID}
	s.mu.Lock()
	c, ok := s.conns[key]
	s.mu.Unlock()
	if ok {
		select {
		case c.recvCh <- pkt:
		default:
			// Full recvCh means this connection's own actor is badly
			// backed up - dropping is the same tolerance a lost UDP
			// datagram already requires this protocol to handle.
		}
		return
	}
	if pkt.Type != STSyn {
		return // unknown connection, not a new one - stray/expired traffic
	}

	c = newConn(s, addr, pkt.ConnID+1, pkt.ConnID, false)
	c.handshakeAccept(pkt)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.conns[connKey{addr: addr.String(), id: c.recvID}] = c
	s.mu.Unlock()

	go c.run()

	select {
	case s.acceptCh <- c:
	default:
		c.Close() // backlog full - mirrors a real listen backlog overflow
	}
}

// removeConn is called once by a *Conn's own teardown, from its actor
// goroutine — never called twice for the same Conn, so no ordering
// concern with a fresh connection reusing the same key while this one is
// still mid-teardown.
func (s *Socket) removeConn(c *Conn) {
	key := connKey{addr: c.remoteAddr.String(), id: c.recvID}
	s.mu.Lock()
	if existing, ok := s.conns[key]; ok && existing == c {
		delete(s.conns, key)
	}
	s.mu.Unlock()
}

// Accept waits for the next inbound connection whose handshake has
// already completed.
func (s *Socket) Accept() (net.Conn, error) {
	select {
	case c := <-s.acceptCh:
		return c, nil
	case <-s.closeCh:
		return nil, ErrSocketClosed
	}
}

// DialContext matches internal/peer.DialFunc's exact signature, so a
// *Socket is usable as a drop-in outbound dialer — network is ignored
// (always UDP/µTP).
func (s *Socket) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	raddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}

	recvID := randomUint16()
	c := newConn(s, raddr, recvID, recvID+1, true)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrSocketClosed
	}
	s.conns[connKey{addr: raddr.String(), id: recvID}] = c
	s.mu.Unlock()

	go c.run()

	if err := c.waitConnected(ctx); err != nil {
		c.Close()
		s.removeConn(c)
		return nil, err
	}
	return c, nil
}

// Close shuts the socket down: every connection still open is closed
// (its own FIN/linger sequence runs as normal, just triggered now rather
// than by its own caller), then the underlying net.PacketConn is closed,
// which unblocks readLoop.
func (s *Socket) Close() error {
	s.shutdown()
	return s.pc.Close()
}

func (s *Socket) shutdown() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		conns := make([]*Conn, 0, len(s.conns))
		for _, c := range s.conns {
			conns = append(conns, c)
		}
		s.mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
		close(s.closeCh)
	})
}
