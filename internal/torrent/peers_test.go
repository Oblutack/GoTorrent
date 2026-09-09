package torrent

import (
	"testing"
	"time"
)

// TestPeersReportsAConnectedRealPeer drives a real download against a real
// fake seeder and checks Peers() reports it with sane values while the
// transfer is in flight - the wire-level correctness of choke/interest
// state and bitfield reporting are already covered by internal/peer's own
// tests, so this only checks the seam between peerConn/peer.Client and the
// snapshot Peers() builds from them.
func TestPeersReportsAConnectedRealPeer(t *testing.T) {
	mi, content := buildTorrent(t, "peerlist.bin", 16384, []fileSpec{{length: 16384 * 4}})
	seeder := newFakeSeeder(t, mi, content)

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())

	deadline := time.Now().Add(5 * time.Second)
	var peers []PeerSnapshot
	for time.Now().Before(deadline) {
		peers = tr.Peers()
		if len(peers) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(peers) != 1 {
		t.Fatalf("Peers() = %d entries, want 1", len(peers))
	}
	p := peers[0]
	if p.Addr == "" {
		t.Fatal("PeerSnapshot.Addr is empty")
	}
	if !p.Outbound {
		t.Fatal("PeerSnapshot.Outbound = false for a peer this client dialed itself")
	}

	// The seeder starts unchoked with a full bitfield, so this client
	// should quickly see PeerChoking=false and Progress=1.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		peers = tr.Peers()
		if len(peers) == 1 && !peers[0].PeerChoking && peers[0].Progress == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(peers) != 1 || peers[0].PeerChoking || peers[0].Progress != 1 {
		t.Fatalf("Peers()[0] never reached (unchoked, Progress=1): %+v", peers)
	}
}

func TestPeersEmptyWhenNoConnections(t *testing.T) {
	mi, _ := buildTorrent(t, "nopeers.bin", 16384, []fileSpec{{length: 16384 * 2}})
	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	if peers := tr.Peers(); len(peers) != 0 {
		t.Fatalf("Peers() = %d entries, want 0", len(peers))
	}
}
