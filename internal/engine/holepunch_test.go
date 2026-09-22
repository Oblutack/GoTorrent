package engine

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/torrent"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// listeningEngine returns a real Engine already listening on a real,
// ephemeral loopback port — the same probe-then-bind pattern
// TestListenRoutesInboundConnectionToTheRightTorrent already uses.
func listeningEngine(t *testing.T) (e *Engine, port uint16) {
	t.Helper()
	e = newTestEngine(t)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probing for a free port: %v", err)
	}
	p := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	e.defaults.ListenPort = uint16(p)

	if err := e.Listen(context.Background()); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	return e, uint16(p)
}

func loopback(port uint16) tracker.PeerInfo {
	return tracker.PeerInfo{IP: net.ParseIP("127.0.0.1"), Port: port}
}

// TestHolepunchConnectsTwoPeersThatNeverDialedEachOther is the real,
// end-to-end proof of BEP 55's whole point: three real engines (real TCP
// listeners, real handshakes, real BEP 10 extended messages) where B
// never dials C directly at all — the simulated "C is unreachable from B"
// case a real NAT would cause — and only reaches it via A relaying a
// rendezvous. B's own eventual connection to C is a real TCP dial this
// client's existing peer-discovery path makes (see holepunch.go's own
// doc comment on why that's TCP, not µTP), triggered by a real "connect"
// message that arrived over a real wire.
func TestHolepunchConnectsTwoPeersThatNeverDialedEachOther(t *testing.T) {
	eA, portA := listeningEngine(t) // the relay
	eB, _ := listeningEngine(t)     // the initiator — behind a simulated NAT: never dials C
	eC, portC := listeningEngine(t) // the target

	path, hash := writeTorrentFile(t, t.TempDir(), "holepunch")
	for _, e := range []*Engine{eA, eB, eC} {
		if _, err := e.Add(path, ""); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	trA, _ := eA.Get(hash)
	trB, _ := eB.Get(hash)
	trC, _ := eC.Get(hash)
	waitForState(t, trA, torrent.StateDownloading, 5*time.Second)
	waitForState(t, trB, torrent.StateDownloading, 5*time.Second)
	waitForState(t, trC, torrent.StateDownloading, 5*time.Second)

	// B dials A, A dials C — the two legs that DO work directly. B never
	// dials C: that's the whole scenario.
	trB.DialPeer(loopback(portA))
	trA.DialPeer(loopback(portC))

	relayAddr := waitForPeerAddr(t, trB, 5*time.Second)
	waitForPeerCount(t, trA, 2, 5*time.Second) // connected to both B and C

	if err := trB.RequestHolepunch(relayAddr, net.ParseIP("127.0.0.1"), portC); err != nil {
		t.Fatalf("RequestHolepunch: %v", err)
	}

	waitForPeerCount(t, trB, 2, 5*time.Second) // A, plus C via the holepunch
	wantPort := fmt.Sprintf("%d", portC)
	found := false
	for _, p := range trB.Peers() {
		if _, port, err := net.SplitHostPort(p.Addr); err == nil && port == wantPort {
			found = true
		}
	}
	if !found {
		t.Fatalf("B's peers %v never came to include C's address (port %d)", trB.Peers(), portC)
	}
}

func waitForPeerAddr(t *testing.T, tr *torrent.Torrent, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if peers := tr.Peers(); len(peers) > 0 {
			return peers[0].Addr
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("torrent never got a connected peer within %s", timeout)
	return ""
}

func waitForPeerCount(t *testing.T, tr *torrent.Torrent, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if tr.Stats().PeerCount >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("torrent's PeerCount never reached %d within %s (stuck at %d)", want, timeout, tr.Stats().PeerCount)
}
