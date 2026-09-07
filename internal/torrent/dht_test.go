package torrent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/dht"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// fakeDHT is a DHTClient a test fully controls: it returns whatever peers
// were configured and counts how many times FindPeers was called, which is
// exactly what the private-torrent exemption needs to prove a negative
// (FindPeers is never called at all).
type fakeDHT struct {
	peers []tracker.PeerInfo
	calls atomic.Int32
}

func (f *fakeDHT) FindPeers(ctx context.Context, infoHash dht.NodeID, port uint16) []tracker.PeerInfo {
	f.calls.Add(1)
	return f.peers
}

// TestDHTLoopFeedsDiscoveredPeersToARealTorrent proves dhtLoop is wired all
// the way through: a torrent built from just an infohash (the magnet shape,
// no DialPeer call from the test and no tracker at all) discovers and
// connects to a real fake seeder purely because Config.DHT.FindPeers handed
// its address back.
func TestDHTLoopFeedsDiscoveredPeersToARealTorrent(t *testing.T) {
	mi, content := buildTorrent(t, "viaDHT.bin", 16384, []fileSpec{{length: 16384 * 2}})
	seeder := newFakeSeeder(t, mi, content)

	fake := &fakeDHT{peers: []tracker.PeerInfo{seeder.peerInfo()}}
	cfg := newTestConfig(t)
	cfg.DHT = fake

	tr, err := NewFromInfoHash(mi.InfoHash, cfg)
	if err != nil {
		t.Fatalf("NewFromInfoHash: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateFetchingMetadata, 2*time.Second)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tr.Stats().PeerCount > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tr.Stats().PeerCount == 0 {
		t.Fatal("no peer ever registered from a DHT-only peer source")
	}
	if fake.calls.Load() == 0 {
		t.Fatal("FindPeers was never called")
	}
}

// TestDHTLoopNeverRunsForAPrivateTorrent proves BEP 27 is honored: a torrent
// whose metadata already marks it private (the torrent.New path always has
// metadata from construction, unlike a magnet) must never call FindPeers,
// not even once.
func TestDHTLoopNeverRunsForAPrivateTorrent(t *testing.T) {
	mi, _ := buildTorrent(t, "private.bin", 16384, []fileSpec{{length: 16384}})
	mi.Info.Private = true

	fake := &fakeDHT{}
	cfg := newTestConfig(t)
	cfg.DHT = fake

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	// Give a real (misbehaving) loop a fair chance to have fired at least
	// once before asserting it never did.
	time.Sleep(200 * time.Millisecond)

	if got := fake.calls.Load(); got != 0 {
		t.Fatalf("FindPeers was called %d times for a private torrent, want 0", got)
	}
}

// TestDHTLoopDoesNothingWithoutADHTClient proves Config.DHT being nil (the
// default) disables the loop cleanly rather than panicking on a nil
// interface call.
func TestDHTLoopDoesNothingWithoutADHTClient(t *testing.T) {
	mi, _ := buildTorrent(t, "nodht.bin", 16384, []fileSpec{{length: 16384}})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)
	// No assertion beyond runInBackground's own cleanup succeeding: a nil
	// Config.DHT must not panic the actor or hang shutdown.
}
