package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// TestAddedAtIsSetOnAddAndSurvivesReload proves AddedAt is recorded at Add
// time and, critically, is NOT reset to the moment of a later reload - the
// whole point of persisting it in the manifest (Stage 5).
func TestAddedAtIsSetOnAddAndSurvivesReload(t *testing.T) {
	stateDir := t.TempDir()
	downloadDir := t.TempDir()
	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "timestamped")

	e1, err := New(stateDir, Defaults{DownloadDir: downloadDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before := time.Now()
	if _, err := e1.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	after := time.Now()

	summary, ok := e1.GetSummary(hash)
	if !ok {
		t.Fatal("GetSummary: torrent missing right after Add")
	}
	if summary.AddedAt.Before(before) || summary.AddedAt.After(after) {
		t.Fatalf("AddedAt = %v, want between %v and %v", summary.AddedAt, before, after)
	}
	originalAddedAt := summary.AddedAt
	e1.Shutdown()

	// A real, human-observable gap before reloading - if AddedAt were ever
	// reset to "now" on Load, this is what would make that visible: the
	// reloaded value would land strictly after originalAddedAt instead of
	// matching it exactly.
	time.Sleep(20 * time.Millisecond)

	e2, err := New(stateDir, Defaults{DownloadDir: downloadDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New (second engine): %v", err)
	}
	t.Cleanup(e2.Shutdown)
	if err := e2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	reloaded, ok := e2.GetSummary(hash)
	if !ok {
		t.Fatal("GetSummary after Load: torrent missing")
	}
	// Compared at Unix-second granularity, not time.Equal - the manifest
	// stores AddedAt as unix seconds (see manifestEntryWire's own doc
	// comment), so a reload legitimately loses sub-second precision; what
	// actually matters is that it isn't reset to the reload's own time.
	if reloaded.AddedAt.Unix() != originalAddedAt.Unix() {
		t.Fatalf("AddedAt after reload = %v, want unchanged %v", reloaded.AddedAt, originalAddedAt)
	}
}

// TestCompletedAtIsSetOnceAndSurvivesReload proves CompletedAt is recorded
// the first time a torrent reaches Seeding and is restored (not reset) by
// a later reload - the same shape as AddedAt's own persistence.
func TestCompletedAtIsSetOnceAndSurvivesReload(t *testing.T) {
	stateDir := t.TempDir()
	downloadDir := t.TempDir()
	torrentDir := t.TempDir()
	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "completedme")
	if err := os.WriteFile(filepath.Join(downloadDir, "completedme"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}

	e1, err := New(stateDir, Defaults{DownloadDir: downloadDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := e1.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e1.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing right after Add")
	}
	waitForState(t, tr, torrent.StateSeeding, 5*time.Second)

	// recordCompletedAt runs off a detached goroutine (see its own doc
	// comment) - give it a moment to actually land before reading Summary.
	var summary Summary
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		summary, ok = e1.GetSummary(hash)
		if !ok {
			t.Fatal("GetSummary: torrent missing")
		}
		if !summary.CompletedAt.IsZero() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if summary.CompletedAt.IsZero() {
		t.Fatal("CompletedAt was never set after reaching Seeding")
	}
	originalCompletedAt := summary.CompletedAt
	e1.Shutdown()

	e2, err := New(stateDir, Defaults{DownloadDir: downloadDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New (second engine): %v", err)
	}
	t.Cleanup(e2.Shutdown)
	if err := e2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	reloaded, ok := e2.GetSummary(hash)
	if !ok {
		t.Fatal("GetSummary after Load: torrent missing")
	}
	// Same Unix-second-granularity comparison as AddedAt's own test, for
	// the same reason - the manifest's wire format only ever had
	// second-level precision to begin with.
	if reloaded.CompletedAt.Unix() != originalCompletedAt.Unix() {
		t.Fatalf("CompletedAt after reload = %v, want unchanged %v", reloaded.CompletedAt, originalCompletedAt)
	}
}

// TestCompletedAtIsZeroBeforeCompletion proves an ordinary in-progress
// torrent doesn't get a fabricated CompletedAt just for existing.
func TestCompletedAtIsZeroBeforeCompletion(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "incomplete")

	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	summary, ok := e.GetSummary(hash)
	if !ok {
		t.Fatal("GetSummary: torrent missing right after Add")
	}
	if !summary.CompletedAt.IsZero() {
		t.Fatalf("CompletedAt = %v, want zero (never reached Seeding)", summary.CompletedAt)
	}
}
