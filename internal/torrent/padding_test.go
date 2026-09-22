package torrent

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

// buildPaddingTestTorrent lays out five files against a 16384-byte piece
// length: a.bin (wanted, 12000 bytes) is followed by a small BEP 47 padding
// file that fills the rest of piece 0 (4384 bytes, exactly the tail of a
// piece a wanted file also occupies — the "must still be allocated" case);
// b.bin (wanted, 32768 bytes) occupies pieces 1-2 wholly; a second, larger
// padding file occupies piece 3 wholly on its own (no wanted file shares
// that piece at all — the "must never be allocated" case); c.bin (wanted,
// 16384 bytes) occupies piece 4 wholly.
func buildPaddingTestTorrent(t *testing.T) (mi *metainfo.MetaInfo, content []byte) {
	t.Helper()
	return buildTorrent(t, "padding-test", 16384, []fileSpec{
		{path: []string{"a.bin"}, length: 12000},
		{path: []string{".pad", "4384"}, length: 4384, attr: "p"},
		{path: []string{"b.bin"}, length: 32768},
		{path: []string{".pad", "16384"}, length: 16384, attr: "p"},
		{path: []string{"c.bin"}, length: 16384},
	})
}

func TestPaddingFlagsIdentifiesOnlyAttrPFiles(t *testing.T) {
	mi, _ := buildPaddingTestTorrent(t)
	got := paddingFlags(mi)
	want := []bool{false, true, false, true, false}
	if len(got) != len(want) {
		t.Fatalf("got %d flags, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("file %d: got padding=%v, want %v (full: got=%v want=%v)", i, got[i], want[i], got, want)
		}
	}
}

// TestNormalizedFilePrioritiesDefaultsPaddingFilesToSkip proves the "no
// selection at all" default (nil FilePriorities) skips padding files
// automatically while every real file stays Normal.
func TestNormalizedFilePrioritiesDefaultsPaddingFilesToSkip(t *testing.T) {
	mi, _ := buildPaddingTestTorrent(t)
	got := normalizedFilePriorities(mi, nil)
	want := []picker.Priority{
		picker.PriorityNormal, picker.PrioritySkip, picker.PriorityNormal,
		picker.PrioritySkip, picker.PriorityNormal,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d priorities, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("file %d: got %s, want %s (full: got=%v want=%v)", i, got[i], want[i], got, want)
		}
	}
}

// TestNormalizedFilePrioritiesCallerOverridesPaddingDefault proves a
// caller's own explicit priority for a padding file wins over the
// automatic skip default — same "caller's value wins" precedence as
// everything else this function normalizes.
func TestNormalizedFilePrioritiesCallerOverridesPaddingDefault(t *testing.T) {
	mi, _ := buildPaddingTestTorrent(t)
	explicit := []picker.Priority{
		picker.PriorityNormal, picker.PriorityHigh, picker.PriorityNormal,
		picker.PrioritySkip, picker.PriorityNormal,
	}
	got := normalizedFilePriorities(mi, explicit)
	if got[1] != picker.PriorityHigh {
		t.Fatalf("file 1 (padding, explicit High): got %s, want High — caller's value should win over the padding default", got[1])
	}
}

// TestFilesNeedingAllocation proves the general gap this closes: a skipped
// file sharing a piece with a wanted file must still be allocated (the
// piece is downloaded regardless — pieces are atomic), while a skipped
// file whose every piece is exclusively its own is correctly never
// allocated.
func TestFilesNeedingAllocation(t *testing.T) {
	mi, _ := buildPaddingTestTorrent(t)
	fp := normalizedFilePriorities(mi, nil) // padding defaults to skip
	pp := piecePriorities(mi, fp)

	got := filesNeedingAllocation(mi, fp, pp)
	want := []bool{true, true, true, false, true}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("file %d: got need=%v, want %v (full: got=%v want=%v)", i, got[i], want[i], got, want)
		}
	}
}

// TestBEP47PaddingFilesAreHandledCorrectlyEndToEnd is the real, wire-level
// regression test for the whole feature: a real download over the real
// protocol from a real fake seeder, proving three things at once that no
// unit test alone can — (1) the small padding file sharing a piece
// boundary with a.bin is allocated and receives the right bytes (the
// allocation-gap fix actually works, not just compiles), (2) the large
// padding file occupying piece 3 entirely on its own is never written to
// disk at all (the actual BEP 47 "don't download padding" behavior), and
// (3) every real file's content still arrives byte-exact despite the
// straddling write.
func TestBEP47PaddingFilesAreHandledCorrectlyEndToEnd(t *testing.T) {
	mi, content := buildPaddingTestTorrent(t)
	seeder := newFakeSeeder(t, mi, content)

	cfg := newTestConfig(t)
	// Deliberately no cfg.FilePriorities — proves the automatic padding
	// default, not an explicit caller selection.

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 5*time.Second)

	root := filepath.Join(cfg.DownloadDir, "padding-test")

	// The large, wholly-owned padding file must never exist on disk.
	if _, err := os.Stat(filepath.Join(root, ".pad", "16384")); !os.IsNotExist(err) {
		t.Fatalf("large padding file (piece 3, no wanted neighbor) exists on disk (stat err = %v), want it never allocated", err)
	}

	// Every real file must be byte-exact, including a.bin, whose tail
	// shares piece 0 with the small padding file — this is what proves
	// the straddling write actually succeeded rather than silently
	// failing or corrupting.
	checkFile := func(name string, want []byte) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s content mismatch: got %d bytes, want %d bytes", name, len(got), len(want))
		}
	}
	checkFile("a.bin", content[0:12000])
	checkFile("b.bin", content[16384:16384+32768])
	checkFile("c.bin", content[65536:65536+16384])

	// The small padding file, sharing piece 0 with a.bin, has to exist —
	// piece 0 was downloaded in full, and its tail landed somewhere.
	if _, err := os.Stat(filepath.Join(root, ".pad", "4384")); err != nil {
		t.Fatalf("small padding file (shares piece 0 with a.bin) was not allocated: %v", err)
	}
}

// TestSetFilePriorityAllocatesANeighboringPaddingFileToo proves the runtime
// half of the allocation-gap fix: doSetFilePriority must allocate not just
// the file whose priority actually changed, but any other skipped file
// (here, the small padding file) that a piece straddling it now needs —
// the old code only ever checked the changed file itself, which would have
// left the padding file unallocated and every write into it failing.
//
// Starts with a.bin explicitly skipped too, so piece 0 is wholly skip at
// construction (both files touching it are skip) and the padding file is
// never allocated — then un-skips a.bin at runtime, which pulls piece 0
// (and therefore the padding file sharing it) back in.
func TestSetFilePriorityAllocatesANeighboringPaddingFileToo(t *testing.T) {
	mi, content := buildPaddingTestTorrent(t)
	seeder := newFakeSeeder(t, mi, content)

	cfg := newTestConfig(t)
	cfg.FilePriorities = []picker.Priority{
		picker.PrioritySkip, picker.PrioritySkip, picker.PriorityNormal,
		picker.PrioritySkip, picker.PriorityNormal,
	}

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())
	waitForState(t, tr, StateSeeding, 5*time.Second)

	root := filepath.Join(cfg.DownloadDir, "padding-test")
	if _, err := os.Stat(filepath.Join(root, ".pad", "4384")); !os.IsNotExist(err) {
		t.Fatalf("padding file exists before a.bin was ever un-skipped: %v", err)
	}

	if err := tr.SetFilePriority(0, picker.PriorityNormal); err != nil {
		t.Fatalf("SetFilePriority: %v", err)
	}

	// Both a.bin and the padding file it shares piece 0 with must exist
	// immediately — SetFilePriority blocks until EnsureFileAllocated has
	// actually run for every file the newly-wanted piece touches, not just
	// file 0 itself.
	if _, err := os.Stat(filepath.Join(root, ".pad", "4384")); err != nil {
		t.Fatalf("padding file was not allocated when its neighboring piece became wanted: %v", err)
	}

	waitForState(t, tr, StateSeeding, 5*time.Second)

	got, err := os.ReadFile(filepath.Join(root, "a.bin"))
	if err != nil {
		t.Fatalf("reading a.bin: %v", err)
	}
	if !bytes.Equal(got, content[0:12000]) {
		t.Fatal("a.bin's downloaded content does not match what the seeder actually had")
	}
}
