package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/torrent"
)

func TestExpandCompletionVars(t *testing.T) {
	got := expandCompletionVars(`echo %N at %F in %D, 100%% done`, "My Movie", "/data/movie.mkv", "/data")
	want := `echo My Movie at /data/movie.mkv in /data, 100% done`
	if got != want {
		t.Fatalf("expandCompletionVars = %q, want %q", got, want)
	}
}

// TestOnCompleteRunsExactlyOnceOnRealCompletion drives a real Engine +
// torrent (pre-seeded, so it reaches StateSeeding on its own with no peer
// needed) with Defaults.OnComplete writing a marker file, and proves it
// fires exactly once even though the torrent will pass through Seeding
// again after a Pause/Resume cycle.
func TestOnCompleteRunsExactlyOnceOnRealCompletion(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran.txt")
	// %N as the file content lets the test also verify substitution reached
	// the real command, not just that *something* ran.
	var onComplete string
	if os.PathSeparator == '\\' {
		onComplete = `echo %N> "` + marker + `"`
	} else {
		onComplete = `echo %N > '` + marker + `'`
	}

	e, err := New(t.TempDir(), Defaults{
		DownloadDir: t.TempDir(),
		ResumeDir:   t.TempDir(),
		OnComplete:  onComplete,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "complete-me")
	// writeTorrentFile's content is random bytes with no matching data on
	// disk, so this torrent will sit in StateDownloading forever (dead
	// tracker, no peer) rather than reaching Seeding on its own — fine,
	// this test drives completion by hand below instead of waiting on a
	// real transfer.
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	e.dispatchCompletionHook(hash, torrent.StateSeeding)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	content, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("on-complete command never ran (marker file missing): %v", err)
	}
	if got := string(content); got != "complete-me\r\n" && got != "complete-me\n" {
		t.Fatalf("marker file content = %q, want the torrent's name (from %%N)", got)
	}

	// A second dispatch (simulating another Seeding transition later, e.g.
	// after a Pause/Resume) must not run the command again.
	if err := os.Remove(marker); err != nil {
		t.Fatalf("removing marker: %v", err)
	}
	e.dispatchCompletionHook(hash, torrent.StateSeeding)
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("on-complete command ran a second time for the same torrent")
	}
}

func TestOnCompleteNeverRunsForNonSeedingTransitions(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "should-not-exist.txt")
	e, err := New(t.TempDir(), Defaults{
		DownloadDir: t.TempDir(),
		ResumeDir:   t.TempDir(),
		OnComplete:  `echo hi > "` + marker + `"`,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "not-done")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	for _, s := range []torrent.State{torrent.StateDownloading, torrent.StateCheckingFiles, torrent.StatePaused} {
		e.dispatchCompletionHook(hash, s)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("on-complete command ran for a non-Seeding transition")
	}
}
