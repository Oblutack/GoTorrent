package torrent

import (
	"sync"
	"testing"
	"time"
)

// TestSetSequentialDownloadsPiecesInOrder proves the runtime toggle reaches
// all the way through the real control channel to the actor-owned picker -
// internal/picker's own TestSetStrategySwitchesAnAlreadyConstructedPicker
// already proves Sequential produces strictly increasing completion order
// in isolation; this only has to prove Torrent.SetSequential really
// reaches that picker, so it checks the one signal adaptive pipelining
// (several pieces legitimately in flight at once) can't blur: piece 0 is
// the first one verified. Under the default RarestFirst with every piece
// equally available, which piece comes first is a randomized tie-break,
// not reliably 0 - so this only passes if the toggle actually took effect.
func TestSetSequentialDownloadsPiecesInOrder(t *testing.T) {
	mi, content := buildTorrent(t, "sequential", 16384, []fileSpec{
		{path: []string{"data.bin"}, length: 16384 * 5},
	})
	seeder := newFakeSeeder(t, mi, content)

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var mu sync.Mutex
	var order []int
	tr.OnPieceVerified(func(index int) {
		mu.Lock()
		order = append(order, index)
		mu.Unlock()
	})

	runInBackground(t, tr)
	if err := tr.SetSequential(true); err != nil {
		t.Fatalf("SetSequential: %v", err)
	}
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 5*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 5 {
		t.Fatalf("verified %d pieces, want 5 (order so far: %v)", len(order), order)
	}
	if order[0] != 0 {
		t.Fatalf("first piece verified was %d, want 0 (sequential order didn't take effect: %v)", order[0], order)
	}
}
