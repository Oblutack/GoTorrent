package torrent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStartPausedNeverAnnouncesReachesPausedDirectly proves Config.
// StartPaused holds a real torrent in StatePaused from the very first
// state after metadata/verification, without ever passing through
// Downloading/Seeding first, and that a normal Resume() afterward still
// works correctly (re-deriving Downloading, since this torrent's content
// was never written to disk).
func TestStartPausedNeverAnnouncesReachesPausedDirectly(t *testing.T) {
	mi, _ := buildTorrent(t, "startpaused.bin", 16384, []fileSpec{{length: 16384 * 2}})

	cfg := newTestConfig(t)
	cfg.StartPaused = true
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	waitForState(t, tr, StatePaused, 2*time.Second)

	// Give it a real moment to prove it doesn't drift anywhere else on its
	// own (a real bug this exact check would catch: StartPaused wired to
	// only the very first tick, silently undone by the next one).
	time.Sleep(100 * time.Millisecond)
	if got := tr.State(); got != StatePaused {
		t.Fatalf("State() = %s a moment later, want it to stay Paused", got)
	}

	if err := tr.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitForState(t, tr, StateDownloading, 2*time.Second)
}

// TestSkipHashCheckTrustsDataWithoutVerifying proves Config.SkipHashCheck
// really does skip the real read-back-and-hash check: deliberately WRONG
// on-disk content (not just absent) is trusted anyway, reaching Seeding
// immediately - if this test used correct content instead, a real
// verify passing would look identical to a skipped one, proving nothing.
func TestSkipHashCheckTrustsDataWithoutVerifying(t *testing.T) {
	const pieceLength = 16384
	mi, _ := buildTorrent(t, "skiphash.bin", pieceLength, []fileSpec{{length: pieceLength * 2}})

	cfg := newTestConfig(t)
	cfg.SkipHashCheck = true
	wrongContent := make([]byte, pieceLength*2)
	for i := range wrongContent {
		wrongContent[i] = 0xFF
	}
	if err := os.WriteFile(filepath.Join(cfg.DownloadDir, "skiphash.bin"), wrongContent, 0o644); err != nil {
		t.Fatalf("writing deliberately-wrong content: %v", err)
	}

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	waitForState(t, tr, StateSeeding, 2*time.Second)
	if got := tr.Stats().HavePieces; got != mi.NumPieces() {
		t.Fatalf("HavePieces = %d, want %d (every piece trusted without verifying)", got, mi.NumPieces())
	}
}
