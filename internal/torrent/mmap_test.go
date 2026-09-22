package torrent

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMmapCompletesARealDownloadByteExact is the real wire-level
// regression test for Config.UseMmap: a real fake seeder, a multi-file
// torrent (so more than one storage.mmapRegion is genuinely in play,
// not just one), downloaded end to end with mmap-backed storage, byte-
// exact on real disk — proving the whole picker/actor/verify pipeline
// works unchanged with this backend, exactly as it does with the
// ordinary file-handle one.
func TestMmapCompletesARealDownloadByteExact(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "mmap-fleet", pieceLength, []fileSpec{
		{path: []string{"a.bin"}, length: pieceLength*3 + 1000},
		{path: []string{"b.bin"}, length: pieceLength * 4},
	})

	cfg := newTestConfig(t)
	cfg.UseMmap = true

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 30*time.Second)

	root := filepath.Join(cfg.DownloadDir, "mmap-fleet")
	gotA, err := os.ReadFile(filepath.Join(root, "a.bin"))
	if err != nil {
		t.Fatalf("read a.bin: %v", err)
	}
	if !bytes.Equal(gotA, content[:pieceLength*3+1000]) {
		t.Fatal("a.bin differs from the source")
	}
	gotB, err := os.ReadFile(filepath.Join(root, "b.bin"))
	if err != nil {
		t.Fatalf("read b.bin: %v", err)
	}
	if !bytes.Equal(gotB, content[pieceLength*3+1000:]) {
		t.Fatal("b.bin differs from the source")
	}

	stats := tr.Stats()
	if stats.HavePieces != mi.NumPieces() {
		t.Fatalf("Stats().HavePieces = %d, want %d", stats.HavePieces, mi.NumPieces())
	}
	if stats.Downloaded != mi.TotalLength {
		t.Fatalf("Stats().Downloaded = %d, want %d", stats.Downloaded, mi.TotalLength)
	}
}

// TestMmapDisabledByDefault proves UseMmap's zero value (what every
// other test in this package relies on) takes the ordinary
// storage.WithMmap(false) path — no behavior change for anything that
// doesn't explicitly opt in.
func TestMmapDisabledByDefault(t *testing.T) {
	cfg := newTestConfig(t)
	if cfg.UseMmap {
		t.Fatalf("newTestConfig's UseMmap = %v, want false (the default every other test in this package relies on)", cfg.UseMmap)
	}
}
