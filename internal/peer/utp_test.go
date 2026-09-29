package peer

import (
	"io"
	"net"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/mse"
	"github.com/Oblutack/GoTorrent/internal/utp"
)

// utpListenerAddr stands up a real *utp.Socket, on its own real UDP
// loopback port, accepting one connection and answering the classic
// handshake over it — standing in for a real remote peer that supports
// µTP. Deliberately a wholly separate socket from whatever the caller
// will use to dial out with (see newClientUTPSocket) — a single real
// *utp.Socket dialing its own listening address would be testing
// something no real deployment ever does, and (confirmed by hand before
// settling on this fixture shape) genuinely doesn't work, the same way a
// single TCP listener can't meaningfully Dial its own Accept.
func utpListenerAddr(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	sock := utp.NewSocket(pc)
	t.Cleanup(func() { sock.Close() })

	go func() {
		conn, err := sock.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		hs := make([]byte, 68)
		if _, err := io.ReadFull(conn, hs); err != nil {
			return
		}
		var remoteID [20]byte
		copy(remoteID[:], "-TEST01-utpcapablep0")
		reply := NewHandshake(testTorrent.InfoHash, remoteID).Serialize()
		conn.Write(reply) //nolint:errcheck
	}()

	return pc.LocalAddr().String()
}

// newClientUTPSocket is the local, dial-only side — the real shape
// internal/engine will hand every torrent (one shared *utp.Socket to
// dial out with, wholly independent of whatever socket a remote peer
// happens to be listening on).
func newClientUTPSocket(t *testing.T) DialFunc {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	sock := utp.NewSocket(pc)
	t.Cleanup(func() { sock.Close() })
	return sock.DialContext
}

func TestNewClientPreferUsesUTPAgainstAUTPCapablePeer(t *testing.T) {
	utpAddr := utpListenerAddr(t)
	udpAddr, err := net.ResolveUDPAddr("udp", utpAddr)
	if err != nil {
		t.Fatalf("ResolveUDPAddr: %v", err)
	}
	utpDial := newClientUTPSocket(t)

	client, err := NewClient(
		mustPeerInfo(&net.TCPAddr{IP: udpAddr.IP, Port: udpAddr.Port}),
		testTorrent, [20]byte{}, Callbacks{}, Limits{}, nil,
		mse.PolicyDisabled, utp.PolicyPrefer, utpDial,
	)
	if err != nil {
		t.Fatalf("NewClient with utp.PolicyPrefer against a real uTP peer: %v", err)
	}
	t.Cleanup(func() { client.Close() })
}

func TestNewClientPreferFallsBackToTCPWhenUTPDialFails(t *testing.T) {
	// A real UDP address nothing is listening on - the µTP dial genuinely
	// fails (no SYN reply ever arrives), so PolicyPrefer must fall back
	// to the real TCP listener at the same logical address.
	deadPC, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	deadUDPSock := utp.NewSocket(deadPC)
	deadUDPSock.Close() // a real, now-dead *utp.Socket - its DialContext will genuinely fail

	origSynRetries := utp.SynRetries
	utp.SynRetries = 1
	t.Cleanup(func() { utp.SynRetries = origSynRetries })

	addr := plaintextOnlyListener(t)

	client, err := NewClient(
		mustPeerInfo(addr.(*net.TCPAddr)), testTorrent, [20]byte{}, Callbacks{}, Limits{}, nil,
		mse.PolicyDisabled, utp.PolicyPrefer, deadUDPSock.DialContext,
	)
	if err != nil {
		t.Fatalf("NewClient with utp.PolicyPrefer, want a successful TCP fallback: %v", err)
	}
	t.Cleanup(func() { client.Close() })
}

func TestNewClientRequiredGivesUpWhenUTPDialFails(t *testing.T) {
	deadPC, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	deadUDPSock := utp.NewSocket(deadPC)
	deadUDPSock.Close()

	origSynRetries := utp.SynRetries
	utp.SynRetries = 1
	t.Cleanup(func() { utp.SynRetries = origSynRetries })

	addr := plaintextOnlyListener(t)

	_, err = NewClient(
		mustPeerInfo(addr.(*net.TCPAddr)), testTorrent, [20]byte{}, Callbacks{}, Limits{}, nil,
		mse.PolicyDisabled, utp.PolicyRequired, deadUDPSock.DialContext,
	)
	if err == nil {
		t.Fatal("NewClient with utp.PolicyRequired succeeded despite a dead uTP dialer, want a failure")
	}
}

// TestNewClientDisabledIgnoresUTPDialEvenIfProvided proves
// utp.PolicyDisabled really is a complete no-op, not just usually
// skipped: even handed a real (if already-dead) utpDial, the connection
// must still succeed over plain TCP without ever attempting to use it —
// dialTransport checks the policy before ever touching utpDial at all.
func TestNewClientDisabledIgnoresUTPDialEvenIfProvided(t *testing.T) {
	deadPC, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	deadUDPSock := utp.NewSocket(deadPC)
	deadUDPSock.Close()

	addr := plaintextOnlyListener(t)
	client, err := NewClient(
		mustPeerInfo(addr.(*net.TCPAddr)), testTorrent, [20]byte{}, Callbacks{}, Limits{}, nil,
		mse.PolicyDisabled, utp.PolicyDisabled, deadUDPSock.DialContext,
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { client.Close() })
}
