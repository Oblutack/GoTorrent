package dht

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/udpmux"
)

// isKRPC classifies a real KRPC datagram the same way internal/engine's
// own real classifier will: every KRPC message is a bencoded dictionary,
// which always starts with 'd'.
func isKRPC(data []byte) bool { return len(data) > 0 && data[0] == 'd' }

// TestNewUsesAnExternallyProvidedConn proves Config.Conn is a real,
// working seam, not just a field that compiles: a DHT node built on top
// of an internal/udpmux facade — sharing one real UDP socket the way
// internal/engine's own inbound µTP wiring will — still completes a real
// ping round trip against an ordinary node that bound its own socket the
// normal way.
func TestNewUsesAnExternallyProvidedConn(t *testing.T) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	mux := udpmux.New(pc)
	t.Cleanup(func() { mux.Close() })
	facade := mux.For(isKRPC)

	a, err := New(Config{Conn: facade})
	if err != nil {
		t.Fatalf("New with an external Conn: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	b := newTestNode(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gotID, err := a.ping(ctx, loopback(b))
	if err != nil {
		t.Fatalf("ping over the external Conn: %v", err)
	}
	if gotID != b.ID() {
		t.Fatalf("ping returned id %s, want %s", gotID, b.ID())
	}
}

// TestAddrReflectsTheExternalConnsRealSocket confirms Addr()'s existing
// LocalAddr().(*net.UDPAddr) assertion still holds when conn is a
// udpmux facade rather than a real *net.UDPConn directly - facade
// LocalAddr() delegates straight through to the real shared socket's, so
// this must keep working unchanged.
func TestAddrReflectsTheExternalConnsRealSocket(t *testing.T) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	mux := udpmux.New(pc)
	t.Cleanup(func() { mux.Close() })
	facade := mux.For(isKRPC)

	d, err := New(Config{Conn: facade})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	if d.Addr().Port != pc.LocalAddr().(*net.UDPAddr).Port {
		t.Fatalf("Addr().Port = %d, want the real shared socket's port %d", d.Addr().Port, pc.LocalAddr().(*net.UDPAddr).Port)
	}
}
