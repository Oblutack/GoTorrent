package torrent

import (
	"testing"
	"time"
)

// TestApplyExternalPieceWritesAndVerifies proves a deduped piece goes
// through the exact same write-then-hash-verify path a real network
// download does: after ApplyExternalPiece, the piece is have, ReadAt
// returns the correct bytes, and Stats.Downloaded reflects it even though
// no peer was ever involved.
func TestApplyExternalPieceWritesAndVerifies(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "deduped.bin", pieceLength, []fileSpec{
		{length: pieceLength * 4},
	})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	const index = 2
	piece := content[index*pieceLength : (index+1)*pieceLength]
	if err := tr.ApplyExternalPiece(index, piece); err != nil {
		t.Fatalf("ApplyExternalPiece: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !tr.HaveBitfield().Has(index) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !tr.HaveBitfield().Has(index) {
		t.Fatal("piece never became have after ApplyExternalPiece")
	}

	got := make([]byte, pieceLength)
	n, err := tr.ReadAt(got, int64(index)*pieceLength)
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if n != len(piece) || string(got[:n]) != string(piece) {
		t.Error("ReadAt after ApplyExternalPiece returned bytes that don't match the source content")
	}

	if got := tr.Stats().Downloaded; got != pieceLength {
		t.Errorf("Stats().Downloaded = %d, want %d (the deduped piece should still count as real progress)", got, pieceLength)
	}
}

// TestApplyExternalPieceWrongLengthIsRejected proves a length mismatch (the
// one cheap sanity check available before trusting a caller-supplied byte
// slice) is refused with a clear error rather than corrupting storage.
func TestApplyExternalPieceWrongLengthIsRejected(t *testing.T) {
	const pieceLength = 16384
	mi, _ := buildTorrent(t, "wronglen.bin", pieceLength, []fileSpec{
		{length: pieceLength * 2},
	})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	if err := tr.ApplyExternalPiece(0, make([]byte, pieceLength-1)); err == nil {
		t.Fatal("ApplyExternalPiece with a short buffer returned nil, want an error")
	}
	if tr.HaveBitfield().Has(0) {
		t.Fatal("a rejected deduped piece must not be marked have")
	}
}

// TestApplyExternalPieceOnAnAlreadyHavePieceIsANoOp covers the real,
// harmless race this method's own doc comment describes: a peer download
// and a dedupe copy both completing the same piece. The second call must
// not error or re-verify — just do nothing.
func TestApplyExternalPieceOnAnAlreadyHavePieceIsANoOp(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "raced.bin", pieceLength, []fileSpec{
		{length: pieceLength * 2},
	})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	piece := content[:pieceLength]
	if err := tr.ApplyExternalPiece(0, piece); err != nil {
		t.Fatalf("first ApplyExternalPiece: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !tr.HaveBitfield().Has(0) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	before := tr.Stats().Downloaded

	if err := tr.ApplyExternalPiece(0, piece); err != nil {
		t.Fatalf("second ApplyExternalPiece on an already-have piece: %v", err)
	}
	if got := tr.Stats().Downloaded; got != before {
		t.Errorf("Downloaded changed from %d to %d on a no-op re-apply — double-counted", before, got)
	}
}

// TestApplyExternalPieceBeforeMetadataReturnsAnError is the same
// no-metadata-yet guard every other t.pick-touching control method in this
// package already has.
func TestApplyExternalPieceBeforeMetadataReturnsAnError(t *testing.T) {
	mi, _ := buildTorrent(t, "magnet-dedupe.bin", 16384, []fileSpec{{length: 16384 * 4}})

	tr, err := NewFromInfoHash(mi.InfoHash, newTestConfig(t))
	if err != nil {
		t.Fatalf("NewFromInfoHash: %v", err)
	}
	_, _ = runInBackground(t, tr)
	waitForState(t, tr, StateFetchingMetadata, 5*time.Second)

	if err := tr.ApplyExternalPiece(0, make([]byte, 16384)); err == nil {
		t.Fatal("ApplyExternalPiece before metadata is known: want an error, got nil")
	}
}
