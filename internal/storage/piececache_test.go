package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// buildPieceCacheTorrent is a small two-piece single-file torrent: piece
// length 16384, two whole pieces, small enough that a real test can write
// every block by hand without a fake seeder.
func buildPieceCacheTorrent(t *testing.T) (mi *metainfo.MetaInfo, content []byte) {
	t.Helper()
	return buildTorrent(t, "piececache", 16384, []fileSpec{{length: 16384 * 2}})
}

// writeWholePiece feeds every block of piece index through cache.WriteBlock
// (mirroring internal/torrent's own onBlock), asserting every one was
// actually buffered — the common case this whole test file is about.
func writeWholePiece(t *testing.T, cache *PieceCache, mi *metainfo.MetaInfo, content []byte, index int) {
	t.Helper()
	pieceOffset := int64(index) * mi.Info.PieceLength
	pieceLength := mi.PieceLen(index)
	for begin := int64(0); begin < pieceLength; begin += 16384 {
		end := begin + 16384
		if end > pieceLength {
			end = pieceLength
		}
		block := content[pieceOffset+begin : pieceOffset+end]
		if !cache.WriteBlock(index, pieceOffset, pieceLength, int(begin), block) {
			t.Fatalf("WriteBlock(piece %d, begin %d) returned false, want buffered", index, begin)
		}
	}
}

// TestPieceCacheFlushesOnlyAfterSuccessfulVerify proves the whole point of
// this type: nothing reaches the underlying Storage until TryVerify
// confirms the hash, and then it lands in one write.
func TestPieceCacheFlushesOnlyAfterSuccessfulVerify(t *testing.T) {
	mi, content := buildPieceCacheTorrent(t)
	s, dir := newStorage(t, mi)
	cache := NewPieceCache(s, 1<<20)

	writeWholePiece(t, cache, mi, content, 0)

	// Before TryVerify, the file on disk must still be exactly what
	// Allocate left it as (sparse-allocated, i.e. all zero) — proof the
	// buffered blocks genuinely haven't touched disk yet.
	raw, err := os.ReadFile(filepath.Join(dir, "piececache"))
	if err != nil {
		t.Fatalf("reading the pre-verify file: %v", err)
	}
	firstPiece := raw[:mi.PieceLen(0)]
	if !bytes.Equal(firstPiece, make([]byte, len(firstPiece))) {
		t.Fatal("WriteBlock wrote to disk before TryVerify — the buffer isn't actually deferring anything")
	}

	ok, found, err := cache.TryVerify(0, mi.PieceHashes[0])
	if err != nil {
		t.Fatalf("TryVerify: %v", err)
	}
	if !found {
		t.Fatal("TryVerify reported found=false for a piece that was fully buffered")
	}
	if !ok {
		t.Fatal("TryVerify reported ok=false for genuinely correct content")
	}

	raw, err = os.ReadFile(filepath.Join(dir, "piececache"))
	if err != nil {
		t.Fatalf("reading the post-verify file: %v", err)
	}
	if !bytes.Equal(raw[:mi.PieceLen(0)], content[:mi.PieceLen(0)]) {
		t.Fatal("piece 0's real content never reached disk after a successful TryVerify")
	}
}

// TestPieceCacheNeverWritesOnHashMismatch proves a corrupt buffered piece
// is simply discarded — a real improvement over the old always-write-
// each-block-immediately behavior, where bad bytes land on disk before
// anyone finds out they're wrong.
func TestPieceCacheNeverWritesOnHashMismatch(t *testing.T) {
	mi, content := buildPieceCacheTorrent(t)
	s, dir := newStorage(t, mi)
	cache := NewPieceCache(s, 1<<20)

	pieceLength := mi.PieceLen(0)
	corrupt := make([]byte, pieceLength)
	copy(corrupt, content[:pieceLength])
	corrupt[0] ^= 0xFF // guaranteed to break the hash

	if !cache.WriteBlock(0, 0, pieceLength, 0, corrupt) {
		t.Fatal("WriteBlock returned false with ample room")
	}

	ok, found, err := cache.TryVerify(0, mi.PieceHashes[0])
	if err != nil {
		t.Fatalf("TryVerify: %v", err)
	}
	if !found {
		t.Fatal("TryVerify reported found=false for a buffered piece")
	}
	if ok {
		t.Fatal("TryVerify reported ok=true for deliberately corrupted content")
	}

	raw, err := os.ReadFile(filepath.Join(dir, "piececache"))
	if err != nil {
		t.Fatalf("reading the file: %v", err)
	}
	if !bytes.Equal(raw[:pieceLength], make([]byte, pieceLength)) {
		t.Fatal("a hash-mismatched piece was written to disk anyway")
	}
}

// TestPieceCacheFallsBackWhenFull proves the bound is real: once buffering
// a new piece would exceed maxBytes, WriteBlock reports false for every
// one of its blocks (not just the ones after the cache filled up), and
// TryVerify correctly reports found=false so the caller knows to read the
// piece back from disk instead.
func TestPieceCacheFallsBackWhenFull(t *testing.T) {
	mi, content := buildPieceCacheTorrent(t)
	s, _ := newStorage(t, mi)
	pieceLength := mi.PieceLen(0)
	cache := NewPieceCache(s, pieceLength) // room for exactly one piece

	writeWholePiece(t, cache, mi, content, 0) // fills the whole budget

	pieceOffset := int64(1) * mi.Info.PieceLength
	block := content[pieceOffset : pieceOffset+16384]
	if cache.WriteBlock(1, pieceOffset, mi.PieceLen(1), 0, block) {
		t.Fatal("WriteBlock accepted a second piece despite no room left")
	}

	// The caller falls back to writing straight to Storage itself, exactly
	// as internal/torrent's onBlock does.
	if _, err := s.WriteAt(block, pieceOffset); err != nil {
		t.Fatalf("direct WriteAt fallback: %v", err)
	}

	_, found, err := cache.TryVerify(1, mi.PieceHashes[1])
	if err != nil {
		t.Fatalf("TryVerify: %v", err)
	}
	if found {
		t.Fatal("TryVerify reported found=true for a piece that was never buffered")
	}
}

// TestPieceCacheUsedTracksBufferedBytes proves Used() reflects reality
// throughout a piece's lifetime, including after it's released.
func TestPieceCacheUsedTracksBufferedBytes(t *testing.T) {
	mi, content := buildPieceCacheTorrent(t)
	s, _ := newStorage(t, mi)
	cache := NewPieceCache(s, 1<<20)

	if got := cache.Used(); got != 0 {
		t.Fatalf("Used() = %d before anything buffered, want 0", got)
	}
	writeWholePiece(t, cache, mi, content, 0)
	if got, want := cache.Used(), mi.PieceLen(0); got != want {
		t.Fatalf("Used() = %d after buffering piece 0, want %d", got, want)
	}
	if _, _, err := cache.TryVerify(0, mi.PieceHashes[0]); err != nil {
		t.Fatalf("TryVerify: %v", err)
	}
	if got := cache.Used(); got != 0 {
		t.Fatalf("Used() = %d after TryVerify released the piece, want 0", got)
	}
}
