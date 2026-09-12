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
// in isolation; this only has to prove Torrent.SetSequential really reaches
// that picker.
//
// This is a genuinely timing-sensitive test (see internal/torrent's own
// Testing section in CLAUDE.md: "don't add a timing-sensitive test without
// [newThrottledFakeSeeder]") and an earlier version without a throttle was
// real, observed flaky under -race, not hypothetically so: request order
// was correctly 0,1,2,3,4 (Sequential's nextPiece always returns the lowest
// inactive index), but adaptive pipelining's minPipeline floor (4) means
// several pieces' responses can be in flight over the wire at once, and
// each arriving piece is verified in its own spawned goroutine (see
// torrent.go's "piece writes are synchronous... verification is just a
// read-back-and-hash, run off-actor") - so completion order isn't request
// order once more than one verify goroutine can be scheduled concurrently,
// and -race's scheduling overhead makes that reordering common rather than
// rare. newThrottledFakeSeeder's per-request delay serializes the fake
// seeder's own responses (its handle loop reads and answers one request at
// a time, sleeping before each write), which in turn keeps each piece's
// verify goroutine well clear of the next arriving response - restoring a
// reliable window the same way this helper already does for pause/resume
// tests, rather than asserting only the weaker "piece 0 first" signal a
// genuinely racy completion order can still satisfy by chance.
func TestSetSequentialDownloadsPiecesInOrder(t *testing.T) {
	mi, content := buildTorrent(t, "sequential", 16384, []fileSpec{
		{path: []string{"data.bin"}, length: 16384 * 5},
	})
	seeder := newThrottledFakeSeeder(t, mi, content, 50*time.Millisecond)

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
	want := []int{0, 1, 2, 3, 4}
	if len(order) != len(want) {
		t.Fatalf("verified %d pieces, want %d (order so far: %v)", len(order), len(want), order)
	}
	for i, index := range order {
		if index != want[i] {
			t.Fatalf("verification order = %v, want %v (sequential order didn't take effect)", order, want)
		}
	}
}
