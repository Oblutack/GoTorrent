package torrent

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWriteCacheCompletesARealDownloadByteExact is the real, wire-level
// regression test for Config.WriteCacheBytes: a real fake seeder, a
// torrent with several pieces, and a cache deliberately sized to hold
// only about half the torrent at once — small enough that some pieces
// are genuinely buffered-then-flushed (storage.PieceCache.WriteBlock
// returns true) and others genuinely bypass straight to Storage.WriteAt
// (the cache has no room left when their first block arrives), within
// the very same real download. Either path has to produce byte-exact
// content; this is what actually proves onBlock/verifyPiece's dispatch
// between the two is correct, not just that each one works in isolation
// (already covered by internal/storage's own PieceCache unit tests).
func TestWriteCacheCompletesARealDownloadByteExact(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "cached.bin", pieceLength, []fileSpec{
		{length: pieceLength*9 + 1000}, // 9 whole pieces plus a short final one
	})

	cfg := newTestConfig(t)
	cfg.WriteCacheBytes = pieceLength * 4 // room for less than half the torrent

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 30*time.Second)

	got, err := os.ReadFile(filepath.Join(cfg.DownloadDir, "cached.bin"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded file differs from the source (%d bytes vs %d)", len(got), len(content))
	}

	stats := tr.Stats()
	if stats.HavePieces != mi.NumPieces() {
		t.Fatalf("Stats().HavePieces = %d, want %d", stats.HavePieces, mi.NumPieces())
	}
	if stats.Downloaded != mi.TotalLength {
		t.Fatalf("Stats().Downloaded = %d, want %d", stats.Downloaded, mi.TotalLength)
	}
}

// TestWriteCacheDisabledByDefaultMatchesUncachedBehavior proves
// WriteCacheBytes's zero value (what every other test in this package
// uses) takes the plain storage.WriteAt path with no behavior change —
// t.pieceCache must be nil, so onBlock/verifyPiece never even consult it.
func TestWriteCacheDisabledByDefaultMatchesUncachedBehavior(t *testing.T) {
	mi, _ := buildTorrent(t, "nocache", 16384, []fileSpec{{length: 16384}})
	cfg := newTestConfig(t)
	if cfg.WriteCacheBytes != 0 {
		t.Fatalf("newTestConfig's WriteCacheBytes = %d, want 0 (the default every other test in this package relies on)", cfg.WriteCacheBytes)
	}

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	if tr.pieceCache != nil {
		t.Fatal("pieceCache is non-nil despite WriteCacheBytes being 0")
	}
}
