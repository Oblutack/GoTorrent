package torrent

import (
	"context"
	"testing"
	"time"
)

// TestSetStreamPositionPrioritizesTheRequestedPiece is streaming mode's
// core claim proven directly against the picker, without any HTTP layer in
// the way: a torrent big enough that a single connection can't fetch it
// all in one pipelined burst (40 pieces, a throttled seeder serving one
// request at a time with a real delay) still resolves a WaitForOffset for
// the very last piece quickly once SetStreamPosition points at it before
// any piece has ever been requested — proving the boosted window actually
// changes fetch order, not just that data eventually arrives. Without
// prioritization this would take roughly numPieces*delay (~600ms); with
// it, only the boosted piece has to arrive first.
func TestSetStreamPositionPrioritizesTheRequestedPiece(t *testing.T) {
	const pieceLength = 16384
	const numPieces = 40
	mi, content := buildTorrent(t, "stream.bin", pieceLength, []fileSpec{
		{length: pieceLength * numPieces},
	})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	if err := tr.SetSequential(true); err != nil {
		t.Fatalf("SetSequential: %v", err)
	}
	lastPieceOffset := int64(numPieces-1) * pieceLength
	if err := tr.SetStreamPosition(lastPieceOffset); err != nil {
		t.Fatalf("SetStreamPosition: %v", err)
	}

	seeder := newThrottledFakeSeeder(t, mi, content, 15*time.Millisecond)
	tr.DialPeer(seeder.peerInfo())

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tr.WaitForOffset(ctx, lastPieceOffset); err != nil {
		t.Fatalf("WaitForOffset for the boosted last piece: %v", err)
	}
	elapsed := time.Since(start)

	const unprioritizedFloor = numPieces * 15 * time.Millisecond // ~600ms if fetched dead last
	if elapsed >= unprioritizedFloor {
		t.Errorf("last piece took %s to arrive, want well under %s (the un-prioritized worst case) — SetStreamPosition does not appear to have reordered fetching", elapsed, unprioritizedFloor)
	}

	got := make([]byte, pieceLength)
	n, err := tr.ReadAt(got, lastPieceOffset)
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	want := content[lastPieceOffset : lastPieceOffset+pieceLength]
	if n != len(want) || string(got[:n]) != string(want) {
		t.Error("ReadAt returned bytes that don't match the source content")
	}
}

// TestWaitForOffsetRespectsContextCancellation proves a caller waiting on a
// piece that is never going to arrive (no peer dialed at all) unblocks
// promptly on ctx expiry rather than hanging — the failure mode a real HTTP
// handler's client-disconnect cancellation depends on.
func TestWaitForOffsetRespectsContextCancellation(t *testing.T) {
	const pieceLength = 16384
	mi, _ := buildTorrent(t, "nodata.bin", pieceLength, []fileSpec{
		{length: pieceLength * 4},
	})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = tr.WaitForOffset(ctx, 0)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("WaitForOffset returned nil for a piece with no peer ever dialed")
	}
	if elapsed > time.Second {
		t.Errorf("WaitForOffset took %s to respect a 200ms context deadline", elapsed)
	}
}

// TestSetStreamPositionBeforeMetadataReturnsAnError proves the same
// no-metadata-yet guard every other t.pick.SetPriorities caller in this
// package already has — a magnet still fetching metadata must not panic
// the actor, the same class of bug TestSetSequentialBeforeMetadataReturns
// AnErrorRatherThanPanicking already pinned down for SetSequential.
func TestSetStreamPositionBeforeMetadataReturnsAnError(t *testing.T) {
	mi, _ := buildTorrent(t, "magnet-stream.bin", 16384, []fileSpec{{length: 16384 * 4}})

	tr, err := NewFromInfoHash(mi.InfoHash, newTestConfig(t))
	if err != nil {
		t.Fatalf("NewFromInfoHash: %v", err)
	}
	_, _ = runInBackground(t, tr)
	waitForState(t, tr, StateFetchingMetadata, 5*time.Second)

	if err := tr.SetStreamPosition(0); err == nil {
		t.Fatal("SetStreamPosition on a metadata-less torrent returned nil, want an error")
	}
}
