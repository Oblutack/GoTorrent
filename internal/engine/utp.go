package engine

import (
	"context"
	"fmt"
	"net"

	"github.com/Oblutack/GoTorrent/internal/udpmux"
	"github.com/Oblutack/GoTorrent/internal/utp"
)

// isDHTPacket classifies a raw UDP datagram as KRPC (DHT, BEP 5) — every
// KRPC message is a bencoded dictionary, which always starts with 'd'
// (0x64). Registered with udpmux ahead of isUTPPacket in StartUTP purely
// for readability: the two shapes can never actually collide (0x64's low
// nibble is 4, never µTP's fixed ProtocolVersion of 1 — see isUTPPacket),
// so which one is tried first makes no real difference.
func isDHTPacket(data []byte) bool {
	return len(data) > 0 && data[0] == 'd'
}

// isUTPPacket classifies a raw UDP datagram as µTP (BEP 29): a full fixed
// header present, the low nibble of byte 0 matching the protocol's fixed
// version, and the high nibble a real packet type (ST_DATA..ST_SYN, i.e.
// 0-4). A false positive here is harmless either way — anything routed to
// the µTP facade that isn't genuinely well-formed still fails Socket's own
// Unmarshal and is dropped there, the same "unclaimed/garbage datagram is
// silently dropped" tolerance every UDP-based protocol in this codebase
// already has to have.
func isUTPPacket(data []byte) bool {
	if len(data) < 20 {
		return false
	}
	if data[0]&0x0f != utp.ProtocolVersion {
		return false
	}
	return data[0]>>4 <= byte(utp.STSyn)
}

// StartUTP binds inbound µTP support (BEP 29) on the same UDP port DHT
// already uses (or is about to), sharing that one real socket via
// internal/udpmux — the standard real-world shape, since a peer that
// learned this client's TCP port from a tracker/DHT/PEX will try µTP on
// that identical port, never a different one. It returns once the socket
// is bound; accepting inbound connections continues in the background
// until ctx is cancelled.
//
// A zero port or Defaults.UTPPolicy == utp.PolicyDisabled (the default) is
// a complete no-op — no socket bound, no goroutine started — matching
// every other Start* method's "0/disabled means don't" convention, so a
// fleet that never touches UTPPolicy pays nothing for this feature
// existing.
//
// Must run before StartDHT for the port-sharing to actually happen:
// StartDHT checks whether this call already populated e.udpMux and, if
// so, hands DHT a udpmux facade (dht.Config.Conn) instead of letting it
// bind its own socket on the same port, which would otherwise fail
// outright (two subsystems of one process cannot both bind the same UDP
// port). internal/bootstrap.Engine sequences this correctly; any other
// caller assembling an Engine by hand must do the same.
func (e *Engine) StartUTP(ctx context.Context, port uint16) error {
	if port == 0 || e.defaults.UTPPolicy == utp.PolicyDisabled {
		return nil
	}

	pc, err := net.ListenUDP("udp", &net.UDPAddr{Port: int(port)})
	if err != nil {
		return fmt.Errorf("engine: starting uTP: %w", err)
	}
	mux := udpmux.New(pc)
	sock := utp.NewSocket(mux.For(isUTPPacket))

	e.mu.Lock()
	e.udpMux = mux
	e.utpSocket = sock
	e.mu.Unlock()

	go func() {
		<-ctx.Done()
		sock.Close()
		mux.Close()
	}()
	go e.utpAcceptLoop(sock)
	return nil
}

// utpAcceptLoop routes every inbound µTP connection through the exact same
// handleIncoming path a real inbound TCP connection already goes through
// (acceptLoop, engine.go) — handleIncoming operates on a plain net.Conn
// and neither knows nor needs to know whether the concrete type
// underneath is a *net.TCPConn or a *utp.Conn.
func (e *Engine) utpAcceptLoop(sock *utp.Socket) {
	for {
		conn, err := sock.Accept()
		if err != nil {
			return
		}
		go e.handleIncoming(conn)
	}
}
