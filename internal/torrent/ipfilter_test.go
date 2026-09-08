package torrent

import (
	"net"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/ipfilter"
)

// TestDialRespectsIPFilter proves a torrent never connects to a peer whose
// address falls inside Config.IPFilter, even when told about it directly
// via DialPeer — the fake seeder here always binds to 127.0.0.1, so
// blocking that single address is enough to prove the check runs before
// any real connection attempt.
func TestDialRespectsIPFilter(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "filtered.bin", pieceLength, []fileSpec{{length: pieceLength * 2}})
	seeder := newFakeSeeder(t, mi, content)

	cfg := newTestConfig(t)
	filter := ipfilter.New()
	filter.Load([]ipfilter.Range{{Start: net.ParseIP("127.0.0.1"), End: net.ParseIP("127.0.0.1")}})
	cfg.IPFilter = filter

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())

	// Give it a real chance to connect, then prove it didn't.
	time.Sleep(300 * time.Millisecond)
	if got := tr.Stats().PeerCount; got != 0 {
		t.Fatalf("PeerCount = %d, want 0 (the only reachable peer's address is filtered)", got)
	}
}

// TestDialAllowsAnUnfilteredPeer is the control for the test above: the
// same setup with a filter that doesn't match must still connect normally,
// proving TestDialRespectsIPFilter's zero PeerCount reflects the filter,
// not some unrelated breakage.
func TestDialAllowsAnUnfilteredPeer(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "unfiltered.bin", pieceLength, []fileSpec{{length: pieceLength * 2}})
	seeder := newFakeSeeder(t, mi, content)

	cfg := newTestConfig(t)
	filter := ipfilter.New()
	filter.Load([]ipfilter.Range{{Start: net.ParseIP("8.8.8.8"), End: net.ParseIP("8.8.8.8")}})
	cfg.IPFilter = filter

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 15*time.Second)
}
