package torrent

import (
	"testing"
	"time"
)

// TestStatsSeedCountAndMinAvailability proves Stats' new Stage 5 fields
// reach a real connected peer: a real fake seeder (which always reports a
// full bitfield on connect - see newFakeSeeder) counts as a seed, not a
// leech, and its bitfield feeds the picker's Availability index, so once
// it's registered every piece is known to have exactly one copy.
func TestStatsSeedCountAndMinAvailability(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "seedcount.bin", pieceLength, []fileSpec{{length: pieceLength * 2}})
	seeder := newFakeSeeder(t, mi, content)

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())

	deadline := time.Now().Add(3 * time.Second)
	var stats Stats
	for time.Now().Before(deadline) {
		stats = tr.Stats()
		if stats.PeerCount > 0 && stats.MinAvailability > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if stats.PeerCount != 1 {
		t.Fatalf("PeerCount = %d, want 1", stats.PeerCount)
	}
	if stats.SeedCount != 1 {
		t.Fatalf("SeedCount = %d, want 1 (the fake seeder always reports a full bitfield)", stats.SeedCount)
	}
	if stats.LeechCount != 0 {
		t.Fatalf("LeechCount = %d, want 0", stats.LeechCount)
	}
	if stats.MinAvailability != 1 {
		t.Fatalf("MinAvailability = %d, want 1 (the one connected peer has every piece)", stats.MinAvailability)
	}
}
