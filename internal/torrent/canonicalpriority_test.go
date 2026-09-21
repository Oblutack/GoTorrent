package torrent

import (
	"net"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// TestOrderDiscoveredPeersSortsByCanonicalPriority proves the ordering is
// real, not a no-op: a deliberately out-of-order input comes back sorted
// by peer.CanonicalPriority descending against Config.LocalIP.
func TestOrderDiscoveredPeersSortsByCanonicalPriority(t *testing.T) {
	local := net.ParseIP("10.0.0.1")
	const localPort = 6881

	candidates := []tracker.PeerInfo{
		{IP: net.ParseIP("203.0.113.10"), Port: 100},
		{IP: net.ParseIP("198.51.100.20"), Port: 200},
		{IP: net.ParseIP("192.0.2.30"), Port: 300},
	}

	tr := &Torrent{cfg: Config{LocalIP: local, ListenPort: localPort}}
	got := tr.orderDiscoveredPeers(candidates)

	if len(got) != len(candidates) {
		t.Fatalf("orderDiscoveredPeers dropped or added entries: got %d, want %d", len(got), len(candidates))
	}
	for i := 0; i+1 < len(got); i++ {
		pi := peer.CanonicalPriority(local, localPort, got[i].IP, got[i].Port)
		pj := peer.CanonicalPriority(local, localPort, got[i+1].IP, got[i+1].Port)
		if pi < pj {
			t.Fatalf("result not sorted descending by priority at index %d: %08x < %08x", i, pi, pj)
		}
	}

	// Confirm this genuinely reordered something, not a lucky already-sorted
	// input - the point of the test is proving real work happened.
	same := true
	for i := range got {
		if !got[i].IP.Equal(candidates[i].IP) || got[i].Port != candidates[i].Port {
			same = false
			break
		}
	}
	if same {
		t.Fatal("test setup produced an input that was already in priority order — can't tell sorting apart from a no-op; adjust the fixture")
	}
}

// TestOrderDiscoveredPeersIsANoOpWithoutLocalIP proves the graceful
// degradation: Config.LocalIP unset (the common case — port mapping never
// ran, or found no gateway) leaves discovery order untouched rather than
// erroring or panicking.
func TestOrderDiscoveredPeersIsANoOpWithoutLocalIP(t *testing.T) {
	candidates := []tracker.PeerInfo{
		{IP: net.ParseIP("203.0.113.10"), Port: 100},
		{IP: net.ParseIP("198.51.100.20"), Port: 200},
	}
	tr := &Torrent{cfg: Config{}} // LocalIP deliberately unset
	got := tr.orderDiscoveredPeers(candidates)

	for i := range got {
		if !got[i].IP.Equal(candidates[i].IP) || got[i].Port != candidates[i].Port {
			t.Fatalf("order changed at index %d despite no LocalIP configured: got %+v, want unchanged %+v", i, got[i], candidates[i])
		}
	}
}
