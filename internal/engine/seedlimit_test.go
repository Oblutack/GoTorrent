package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// TestSeedLimitActionRemoveDropsFromFleet proves SeedLimitActionRemove
// actually reaches Engine.applySeedLimitAction end to end: a real
// pre-seeded torrent, a real (short) seed-time limit, and the torrent
// disappearing from the fleet once it trips — with its data left alone.
func TestSeedLimitActionRemoveDropsFromFleet(t *testing.T) {
	downloadDir := t.TempDir()
	torrentDir := t.TempDir()
	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "removeme")
	if err := os.WriteFile(filepath.Join(downloadDir, "removeme"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}

	e, err := New(t.TempDir(), Defaults{
		DownloadDir:     downloadDir,
		ResumeDir:       t.TempDir(),
		SeedTimeLimit:   100 * time.Millisecond,
		SeedLimitAction: SeedLimitActionRemove,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing right after Add")
	}
	waitForState(t, tr, torrent.StateSeeding, 5*time.Second)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := e.Get(hash); !ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := e.Get(hash); ok {
		t.Fatal("torrent was never removed after its seed time limit was reached")
	}

	if _, err := os.Stat(filepath.Join(downloadDir, "removeme")); err != nil {
		t.Fatalf("SeedLimitActionRemove should leave data on disk, but stat failed: %v", err)
	}
}

// TestSeedLimitActionRemoveDeleteDataDeletesTheFile is the same scenario
// with SeedLimitActionRemoveDeleteData, proving the data is actually gone
// too, not just the fleet entry.
func TestSeedLimitActionRemoveDeleteDataDeletesTheFile(t *testing.T) {
	downloadDir := t.TempDir()
	torrentDir := t.TempDir()
	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "deleteme")
	if err := os.WriteFile(filepath.Join(downloadDir, "deleteme"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}

	e, err := New(t.TempDir(), Defaults{
		DownloadDir:     downloadDir,
		ResumeDir:       t.TempDir(),
		SeedTimeLimit:   100 * time.Millisecond,
		SeedLimitAction: SeedLimitActionRemoveDeleteData,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing right after Add")
	}
	waitForState(t, tr, torrent.StateSeeding, 5*time.Second)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(downloadDir, "deleteme")); os.IsNotExist(err) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(downloadDir, "deleteme")); !os.IsNotExist(err) {
		t.Fatalf("SeedLimitActionRemoveDeleteData did not delete the file (stat err = %v)", err)
	}
	if _, ok := e.Get(hash); ok {
		t.Fatal("torrent still managed after SeedLimitActionRemoveDeleteData")
	}
}

func TestSeedLimitActionPauseNeverRemoves(t *testing.T) {
	downloadDir := t.TempDir()
	torrentDir := t.TempDir()
	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "stays")
	if err := os.WriteFile(filepath.Join(downloadDir, "stays"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}

	e, err := New(t.TempDir(), Defaults{
		DownloadDir:   downloadDir,
		ResumeDir:     t.TempDir(),
		SeedTimeLimit: 100 * time.Millisecond,
		// SeedLimitAction left at its zero value (SeedLimitActionPause).
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing right after Add")
	}
	waitForState(t, tr, torrent.StateSeeding, 5*time.Second)
	waitForState(t, tr, torrent.StatePaused, 5*time.Second)

	time.Sleep(200 * time.Millisecond)
	if _, ok := e.Get(hash); !ok {
		t.Fatal("torrent was removed even though SeedLimitAction was the default (pause only)")
	}
}
