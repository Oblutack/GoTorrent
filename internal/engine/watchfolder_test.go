package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStartWatchFolderAddsExistingAndNewFiles(t *testing.T) {
	watchDir := t.TempDir()
	torrentDir := t.TempDir()

	// One file dropped before the watch starts (proves the initial scan,
	// not just the ticker).
	pathA, hashA := writeTorrentFile(t, torrentDir, "already-there")
	dataA, err := os.ReadFile(pathA)
	if err != nil {
		t.Fatalf("reading fixture a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(watchDir, "a.torrent"), dataA, 0o644); err != nil {
		t.Fatalf("seeding watch dir: %v", err)
	}

	original := watchFolderInterval
	watchFolderInterval = 50 * time.Millisecond
	t.Cleanup(func() { watchFolderInterval = original })

	e := newTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.StartWatchFolder(ctx, watchDir)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := e.Get(hashA); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := e.Get(hashA); !ok {
		t.Fatal("watch folder never added the file already present at startup")
	}

	// A second file dropped in later must also get picked up by the
	// ticker, not just the initial scan.
	pathB, hashB := writeTorrentFile(t, torrentDir, "dropped-later")
	dataB, err := os.ReadFile(pathB)
	if err != nil {
		t.Fatalf("reading fixture b: %v", err)
	}
	if err := os.WriteFile(filepath.Join(watchDir, "b.torrent"), dataB, 0o644); err != nil {
		t.Fatalf("dropping second file: %v", err)
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := e.Get(hashB); ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, ok := e.Get(hashB); !ok {
		t.Fatal("watch folder never picked up the file dropped in after startup")
	}
}

func TestWatchFolderIgnoresNonTorrentFiles(t *testing.T) {
	watchDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(watchDir, "readme.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("writing non-torrent file: %v", err)
	}

	e := newTestEngine(t)
	e.watchFolderScan(watchDir) // one synchronous pass, no need for the ticker
	if len(e.List()) != 0 {
		t.Fatalf("List() = %v, want nothing added from a non-.torrent file", e.List())
	}
}

func TestWatchFolderRescanDoesNotErrorOnAlreadyAdded(t *testing.T) {
	watchDir := t.TempDir()
	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "dup")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(watchDir, "dup.torrent"), data, 0o644); err != nil {
		t.Fatalf("seeding watch dir: %v", err)
	}

	e := newTestEngine(t)
	e.watchFolderScan(watchDir)
	if _, ok := e.Get(hash); !ok {
		t.Fatal("first scan did not add the file")
	}
	// A second scan must not remove it, duplicate it, or otherwise disturb
	// the fleet — it should just see ErrAlreadyAdded and move on.
	e.watchFolderScan(watchDir)
	if len(e.List()) != 1 {
		t.Fatalf("List() has %d entries after a re-scan, want 1", len(e.List()))
	}
}
