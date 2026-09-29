package peer

import (
	"bufio"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/mse"
)

// plaintextOnlyListener stands up a loopback listener that only ever
// answers a classic (unencrypted) BitTorrent handshake — standing in for
// a real peer that doesn't understand MSE at all, which is exactly what
// PolicyPrefer's fallback redial exists to cope with. It accepts every
// connection in a loop, not just one: PolicyPrefer's own real behavior
// against this fake peer makes two separate TCP connections (a first,
// doomed MSE attempt, then the fallback), and a real listening peer would
// of course accept both.
func plaintextOnlyListener(t *testing.T) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				hs := make([]byte, 68)
				if _, err := io.ReadFull(conn, hs); err != nil {
					return
				}
				var remoteID [20]byte
				copy(remoteID[:], "-TEST01-plaintextonl")
				reply := NewHandshake(testTorrent.InfoHash, remoteID).Serialize()
				conn.Write(reply)
			}()
		}
	}()
	return ln.Addr()
}

// mseCapableListener stands up a loopback listener that negotiates a real
// MSE/PE handshake (via the real internal/mse package — the same one
// already covered by its own standalone test suite; this exercises the
// integration between the two packages, not mse's own protocol
// correctness again) before answering the classic handshake underneath
// it, standing in for a real peer that does support encryption.
func mseCapableListener(t *testing.T) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		looksLegacy, err := mse.LooksLikeHandshake(br)
		if err != nil || looksLegacy {
			return
		}
		wrapped, _, _, err := mse.ReceiveHandshake(conn, br, [][]byte{testTorrent.InfoHash[:]}, mse.DefaultSelector)
		if err != nil {
			return
		}
		hs := make([]byte, 68)
		if _, err := io.ReadFull(wrapped, hs); err != nil {
			return
		}
		var remoteID [20]byte
		copy(remoteID[:], "-TEST01-msecapablepe")
		reply := NewHandshake(testTorrent.InfoHash, remoteID).Serialize()
		wrapped.Write(reply)
	}()
	return ln.Addr()
}

func TestNewClientPreferFallsBackToPlaintextAgainstALegacyPeer(t *testing.T) {
	// The doomed first (MSE) attempt against a legacy-only peer can only
	// ever be detected by A's own read deadline expiring — there is no
	// message a non-MSE peer could send to signal "I don't understand
	// this" — so shrink it rather than have this one test genuinely wait
	// out 10 real seconds.
	orig := mse.HandshakeTimeout
	mse.HandshakeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { mse.HandshakeTimeout = orig })

	addr := plaintextOnlyListener(t)
	client, err := NewClient(mustPeerInfo(addr.(*net.TCPAddr)), testTorrent, [20]byte{}, Callbacks{}, Limits{}, nil, mse.PolicyPrefer)
	if err != nil {
		t.Fatalf("NewClient with PolicyPrefer against a legacy peer: %v", err)
	}
	t.Cleanup(func() { client.Close() })
}

func TestNewClientPreferUsesEncryptionAgainstAnMSECapablePeer(t *testing.T) {
	addr := mseCapableListener(t)
	client, err := NewClient(mustPeerInfo(addr.(*net.TCPAddr)), testTorrent, [20]byte{}, Callbacks{}, Limits{}, nil, mse.PolicyPrefer)
	if err != nil {
		t.Fatalf("NewClient with PolicyPrefer against an MSE-capable peer: %v", err)
	}
	t.Cleanup(func() { client.Close() })
}

func TestNewClientRequiredRefusesToFallBackToALegacyPeer(t *testing.T) {
	addr := plaintextOnlyListener(t)
	_, err := NewClient(mustPeerInfo(addr.(*net.TCPAddr)), testTorrent, [20]byte{}, Callbacks{}, Limits{}, nil, mse.PolicyRequired)
	if err == nil {
		t.Fatal("NewClient with PolicyRequired succeeded against a legacy-only peer, want a failure")
	}
}

func TestNewClientRequiredSucceedsAgainstAnMSECapablePeer(t *testing.T) {
	addr := mseCapableListener(t)
	client, err := NewClient(mustPeerInfo(addr.(*net.TCPAddr)), testTorrent, [20]byte{}, Callbacks{}, Limits{}, nil, mse.PolicyRequired)
	if err != nil {
		t.Fatalf("NewClient with PolicyRequired against an MSE-capable peer: %v", err)
	}
	t.Cleanup(func() { client.Close() })
}

// TestNewClientDisabledNeverAttemptsEncryption confirms PolicyDisabled is
// the same code path as before this feature existed: it must succeed
// against a plaintext-only peer directly, with no MSE attempt at all
// (an MSE attempt first would still eventually fall through since there's
// no fallback wired for PolicyDisabled — this test's real point is that
// a plaintext-only peer is reached on the very first try, not after some
// failed detour).
func TestNewClientDisabledNeverAttemptsEncryption(t *testing.T) {
	addr := plaintextOnlyListener(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		client, err := NewClient(mustPeerInfo(addr.(*net.TCPAddr)), testTorrent, [20]byte{}, Callbacks{}, Limits{}, nil, mse.PolicyDisabled)
		if err != nil {
			t.Errorf("NewClient with PolicyDisabled: %v", err)
			return
		}
		client.Close()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("NewClient with PolicyDisabled took too long — suggests it tried MSE first")
	}
}
