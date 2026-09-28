package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/storage"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// TestIncompleteDirStagesThenMovesOnCompletion proves the whole feature end
// to end: a torrent Added with Defaults.IncompleteDir configured downloads
// (here, verifies instantly from pre-seeded content) into the incomplete
// directory, then — once it first reaches StateSeeding — is automatically
// relocated to its real, final download directory, byte-exact, with
// nothing left behind in the staging directory.
func TestIncompleteDirStagesThenMovesOnCompletion(t *testing.T) {
	incompleteDir := t.TempDir()
	finalDir := t.TempDir()
	torrentDir := t.TempDir()

	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "staged")
	// Pre-seed the content under the INCOMPLETE directory, not the final
	// one - this torrent should verify instantly there, exactly like
	// TestMoveDataRelocatesFilesAndResumesWithoutRedownloading pre-seeds
	// its old directory.
	if err := os.WriteFile(filepath.Join(incompleteDir, "staged"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}

	e, err := New(t.TempDir(), Defaults{DownloadDir: finalDir, ResumeDir: t.TempDir(), IncompleteDir: incompleteDir})
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

	// The move itself races the test goroutine reading e.List() right
	// after Seeding - poll for it rather than asserting once.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(finalDir, "staged")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	got, err := os.ReadFile(filepath.Join(finalDir, "staged"))
	if err != nil {
		t.Fatalf("reading moved content at the final directory: %v", err)
	}
	if string(got) != string(content) {
		t.Fatal("moved file's content does not match the original")
	}
	if _, err := os.Stat(filepath.Join(incompleteDir, "staged")); !os.IsNotExist(err) {
		t.Fatalf("content still exists in the incomplete directory (stat err = %v)", err)
	}

	tr2, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing after the move")
	}
	waitForState(t, tr2, torrent.StateSeeding, 5*time.Second)

	list := e.List()
	if len(list) != 1 || list[0].DownloadDir != finalDir {
		t.Fatalf("List()[0].DownloadDir = %q, want %q (the final directory, never the staging one)", list[0].DownloadDir, finalDir)
	}
}

// TestIncompleteDirCompletionHookSeesTheFinalPathNotTheStagingOne proves
// the ordering guarantee dispatchCompletionHook/recordCompletedAt's own
// incompleteDir check exists for: the on-complete command must never run
// against the soon-to-be-renamed-away staging path.
func TestIncompleteDirCompletionHookSeesTheFinalPathNotTheStagingOne(t *testing.T) {
	incompleteDir := t.TempDir()
	finalDir := t.TempDir()
	torrentDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran.txt")

	var onComplete string
	if os.PathSeparator == '\\' {
		onComplete = `echo %F> "` + marker + `"`
	} else {
		onComplete = `echo %F > '` + marker + `'`
	}

	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "hooked")
	if err := os.WriteFile(filepath.Join(incompleteDir, "hooked"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}

	e, err := New(t.TempDir(), Defaults{
		DownloadDir: finalDir, ResumeDir: t.TempDir(), IncompleteDir: incompleteDir, OnComplete: onComplete,
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
	var content2 []byte
	for time.Now().Before(deadline) {
		content2, err = os.ReadFile(marker)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("on-complete command never ran (marker file missing): %v", err)
	}

	gotPath := filepath.Clean(strings.TrimRight(string(content2), "\r\n")) // echo's own line ending, CRLF or LF depending on OS
	wantPath := filepath.Join(finalDir, "hooked")
	if gotPath != wantPath {
		t.Fatalf("on-complete's %%F = %q, want the final path %q - it must never see the staging directory", gotPath, wantPath)
	}
}

// TestIncompleteDirManifestResumesStagingAcrossARestart proves a
// still-downloading staged torrent keeps using the exact incomplete
// directory it was already using after a restart, even when the new
// process's own Defaults.IncompleteDir is different (or unset) - the
// manifest's own persisted incomplete_dir must win, not the engine's
// current configuration.
func TestIncompleteDirManifestResumesStagingAcrossARestart(t *testing.T) {
	incompleteDir := t.TempDir()
	finalDir := t.TempDir()
	stateDir := t.TempDir()
	torrentDir := t.TempDir()

	// writeTorrentFile's content is random with nothing matching on disk,
	// so this torrent never reaches Seeding on its own (dead tracker, no
	// peer) - it stays genuinely mid-stage the whole test, which is
	// exactly the case this test needs.
	path, hash := writeTorrentFile(t, torrentDir, "still-going")

	e1, err := New(stateDir, Defaults{DownloadDir: finalDir, ResumeDir: t.TempDir(), IncompleteDir: incompleteDir})
	if err != nil {
		t.Fatalf("New (first engine): %v", err)
	}
	if _, err := e1.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	e1.Shutdown()

	// A second engine, deliberately configured with a DIFFERENT (in fact
	// unset) IncompleteDir - if the reload wrongly re-decided staging from
	// this engine's own defaults instead of the manifest, it would use no
	// staging directory at all instead of resuming the real one.
	e2, err := New(stateDir, Defaults{DownloadDir: finalDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New (second engine): %v", err)
	}
	t.Cleanup(e2.Shutdown)
	if err := e2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	e2.mu.Lock()
	mt, ok := e2.torrents[hash]
	e2.mu.Unlock()
	if !ok {
		t.Fatal("torrent missing after reload")
	}
	if mt.incompleteDir != incompleteDir {
		t.Fatalf("reloaded incompleteDir = %q, want the original %q", mt.incompleteDir, incompleteDir)
	}
	if mt.downloadDir != finalDir {
		t.Fatalf("reloaded downloadDir = %q, want the final directory %q", mt.downloadDir, finalDir)
	}
}

// TestIncompleteDirDoesNotReapplyToAnAlreadyCompletedReloadedTorrent proves
// the other half of the same guarantee: a torrent that already finished
// and moved to its final directory must never be re-staged on a later
// reload, even with Defaults.IncompleteDir still (or newly) configured.
func TestIncompleteDirDoesNotReapplyToAnAlreadyCompletedReloadedTorrent(t *testing.T) {
	incompleteDir := t.TempDir()
	finalDir := t.TempDir()
	stateDir := t.TempDir()
	torrentDir := t.TempDir()

	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "finished")
	if err := os.WriteFile(filepath.Join(incompleteDir, "finished"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}

	e1, err := New(stateDir, Defaults{DownloadDir: finalDir, ResumeDir: t.TempDir(), IncompleteDir: incompleteDir})
	if err != nil {
		t.Fatalf("New (first engine): %v", err)
	}
	if _, err := e1.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e1.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing right after Add")
	}
	waitForState(t, tr, torrent.StateSeeding, 5*time.Second)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(finalDir, "finished")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(finalDir, "finished")); err != nil {
		t.Fatalf("content never reached the final directory before shutdown: %v", err)
	}
	e1.Shutdown()

	// Second engine, IncompleteDir still configured (the realistic case: a
	// user leaves the setting on) - the already-completed torrent must
	// come back pointed straight at its final directory, not re-staged.
	e2, err := New(stateDir, Defaults{DownloadDir: finalDir, ResumeDir: t.TempDir(), IncompleteDir: incompleteDir})
	if err != nil {
		t.Fatalf("New (second engine): %v", err)
	}
	t.Cleanup(e2.Shutdown)
	if err := e2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	e2.mu.Lock()
	mt, ok := e2.torrents[hash]
	e2.mu.Unlock()
	if !ok {
		t.Fatal("torrent missing after reload")
	}
	if mt.incompleteDir != "" {
		t.Fatalf("reloaded incompleteDir = %q, want empty - an already-completed torrent must never be re-staged", mt.incompleteDir)
	}

	tr2, ok := e2.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing after reload")
	}
	waitForState(t, tr2, torrent.StateSeeding, 5*time.Second)
}

// TestIncompleteDirLeavesANoSubfolderMultiFileTorrentInPlace mirrors
// TestMoveDataRefusesNoSubfolderMultiFileTorrent: such a torrent's content
// root is the whole staging directory, not something exclusively its own,
// so maybeMoveFromIncompleteDir must refuse to move it automatically
// rather than risk moving (or leaving behind) other torrents' data. The
// torrent keeps seeding from the staging directory; nothing appears in the
// final one.
func TestIncompleteDirLeavesANoSubfolderMultiFileTorrentInPlace(t *testing.T) {
	incompleteDir := t.TempDir()
	finalDir := t.TempDir()
	torrentDir := t.TempDir()

	e, err := New(t.TempDir(), Defaults{
		DownloadDir: finalDir, ResumeDir: t.TempDir(), IncompleteDir: incompleteDir,
		ContentLayout: storage.LayoutNoSubfolder,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	path, hash, content := writeMultiFileTorrentFile(t, torrentDir, "multi")
	// LayoutNoSubfolder means the file lands directly at
	// <incompleteDir>/a.bin, not <incompleteDir>/multi/a.bin.
	if err := os.WriteFile(filepath.Join(incompleteDir, "a.bin"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}

	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing right after Add")
	}
	waitForState(t, tr, torrent.StateSeeding, 5*time.Second)

	// Give maybeMoveFromIncompleteDir a real window to (wrongly) move
	// things, if the refusal guard didn't hold.
	time.Sleep(300 * time.Millisecond)

	if _, err := os.Stat(filepath.Join(finalDir, "a.bin")); !os.IsNotExist(err) {
		t.Fatalf("content was moved to the final directory despite the no-subfolder refusal guard (stat err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(incompleteDir, "a.bin")); err != nil {
		t.Fatalf("content is no longer at the staging directory either: %v", err)
	}

	e.mu.Lock()
	mt, ok := e.torrents[hash]
	e.mu.Unlock()
	if !ok {
		t.Fatal("torrent missing")
	}
	if mt.incompleteDir == "" {
		t.Fatal("incompleteDir was cleared despite the move being refused")
	}
}
