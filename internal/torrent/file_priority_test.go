package torrent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

// buildFilePriorityTorrent lays out three files against a 16384-byte piece
// length: piece 0 is all of A, piece 1 is A's tail plus all of B (the
// straddle piecePriorities has to get right), piece 2 is all of C — same
// geometry as buildPriorityTestTorrent in priority_test.go, but this one
// also returns real content so a fake seeder can actually serve it.
func buildFilePriorityTorrent(t *testing.T) (mi *metainfo.MetaInfo, content []byte) {
	t.Helper()
	m, c := buildTorrent(t, "file-priority", 16384, []fileSpec{
		{path: []string{"a.bin"}, length: 20000},
		{path: []string{"b.bin"}, length: 12768},
		{path: []string{"c.bin"}, length: 16384},
	})
	return m, c
}

// TestSkippedFileIsNeverAllocatedOrDownloaded proves the whole feature end
// to end: a torrent started with one file at PrioritySkip never creates
// that file on disk at all, and still reaches Seeding once everything it
// actually wants has arrived — even though a real peer has the skipped
// file's data too and would happily serve it.
func TestSkippedFileIsNeverAllocatedOrDownloaded(t *testing.T) {
	mi, content := buildFilePriorityTorrent(t)
	seeder := newFakeSeeder(t, mi, content)

	cfg := newTestConfig(t)
	cfg.FilePriorities = []picker.Priority{picker.PriorityNormal, picker.PriorityNormal, picker.PrioritySkip}

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 5*time.Second)

	root := filepath.Join(cfg.DownloadDir, "file-priority")
	if _, err := os.Stat(filepath.Join(root, "a.bin")); err != nil {
		t.Fatalf("a.bin was not downloaded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "b.bin")); err != nil {
		t.Fatalf("b.bin was not downloaded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "c.bin")); !os.IsNotExist(err) {
		t.Fatalf("c.bin (skipped) exists on disk (stat err = %v), want it never allocated", err)
	}
}

// TestSetFilePriorityUnskipsAndDownloadsTheRestLater proves the runtime
// half: raising a skipped file's priority after the torrent has already
// reached Seeding allocates it, requests its pieces, and the torrent ends
// up fully Seeding again with the previously-skipped file's real content on
// disk.
func TestSetFilePriorityUnskipsAndDownloadsTheRestLater(t *testing.T) {
	mi, content := buildFilePriorityTorrent(t)
	seeder := newFakeSeeder(t, mi, content)

	cfg := newTestConfig(t)
	cfg.FilePriorities = []picker.Priority{picker.PriorityNormal, picker.PriorityNormal, picker.PrioritySkip}

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())
	waitForState(t, tr, StateSeeding, 5*time.Second)

	root := filepath.Join(cfg.DownloadDir, "file-priority")
	if _, err := os.Stat(filepath.Join(root, "c.bin")); !os.IsNotExist(err) {
		t.Fatalf("c.bin exists before ever being un-skipped: %v", err)
	}

	if err := tr.SetFilePriority(2, picker.PriorityNormal); err != nil {
		t.Fatalf("SetFilePriority: %v", err)
	}

	// c.bin must exist immediately: SetFilePriority blocks until
	// EnsureFileAllocated has actually run, not just until the priority
	// change is queued.
	if _, err := os.Stat(filepath.Join(root, "c.bin")); err != nil {
		t.Fatalf("c.bin was not allocated synchronously by SetFilePriority: %v", err)
	}

	waitForState(t, tr, StateSeeding, 5*time.Second)

	got, err := os.ReadFile(filepath.Join(root, "c.bin"))
	if err != nil {
		t.Fatalf("reading c.bin: %v", err)
	}
	want := content[len(content)-16384:]
	if string(got) != string(want) {
		t.Fatal("c.bin's downloaded content does not match what the seeder actually had")
	}
}

// TestSetFilePriorityRejectsOutOfRangeIndex proves the control call itself
// is validated, not just silently ignored.
func TestSetFilePriorityRejectsOutOfRangeIndex(t *testing.T) {
	mi, _ := buildFilePriorityTorrent(t)
	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	if err := tr.SetFilePriority(99, picker.PriorityNormal); err == nil {
		t.Fatal("SetFilePriority accepted an out-of-range file index, want an error")
	}
}
