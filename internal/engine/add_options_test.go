package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// TestAddWithOptionsStartPausedReachesPaused proves AddOptions.StartPaused
// reaches a real managed torrent end to end, through the engine's own
// public Add path, not just torrent.Config directly.
func TestAddWithOptionsStartPausedReachesPaused(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "addpaused")

	if _, err := e.AddWithOptions(path, "", AddOptions{StartPaused: true}); err != nil {
		t.Fatalf("AddWithOptions: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing right after Add")
	}
	waitForState(t, tr, torrent.StatePaused, 2*time.Second)
}

// TestAddWithOptionsSkipHashCheckReachesSeeding proves AddOptions.
// SkipHashCheck also reaches a real managed torrent end to end - same
// deliberately-wrong-content proof as the torrent-package-level test, so
// a real verify accidentally passing can't be mistaken for one correctly
// skipped.
func TestAddWithOptionsSkipHashCheckReachesSeeding(t *testing.T) {
	downloadDir := t.TempDir()
	torrentDir := t.TempDir()
	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "addskiphash")
	wrongContent := make([]byte, len(content))
	for i := range wrongContent {
		wrongContent[i] = 0xAA
	}
	if err := os.WriteFile(filepath.Join(downloadDir, "addskiphash"), wrongContent, 0o644); err != nil {
		t.Fatalf("writing deliberately-wrong content: %v", err)
	}

	e, err := New(t.TempDir(), Defaults{DownloadDir: downloadDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	if _, err := e.AddWithOptions(path, "", AddOptions{SkipHashCheck: true}); err != nil {
		t.Fatalf("AddWithOptions: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing right after Add")
	}
	waitForState(t, tr, torrent.StateSeeding, 2*time.Second)
}
