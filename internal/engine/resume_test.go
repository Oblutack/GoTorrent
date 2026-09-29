package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// TestRemoveDeletesResumeData is a regression test for a real gap: resume
// data lives outside DownloadDir specifically so deleting downloaded files
// never erases it, but that also meant nothing ever cleaned it up when a
// torrent was removed from the fleet entirely — Remove stopped the torrent
// (which checkpoints on its way to Paused) but never deleted the resume
// file it had just written, leaving it behind forever. Confirmed this test
// would actually have caught the bug: it failed with a real "resume data
// still exists after Remove" error before Torrent.RemoveResumeData existed.
func TestRemoveDeletesResumeData(t *testing.T) {
	resumeDir := t.TempDir()
	e, err := New(t.TempDir(), Defaults{
		DownloadDir: t.TempDir(),
		ResumeDir:   resumeDir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "one")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get did not find the added torrent")
	}
	waitForState(t, tr, torrent.StateDownloading, 10*time.Second)

	// Pause checkpoints synchronously (checkpoint runs inside doPause before
	// it returns), so the resume file is guaranteed to exist right after.
	if err := tr.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	resumeFile := filepath.Join(resumeDir, hash.String()+".resume")
	if _, err := os.Stat(resumeFile); err != nil {
		t.Fatalf("resume file missing after Pause's own checkpoint: %v", err)
	}

	if err := e.Remove(hash); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(resumeFile); !os.IsNotExist(err) {
		t.Fatalf("resume file still exists after Remove (err=%v), want it deleted", err)
	}
}
