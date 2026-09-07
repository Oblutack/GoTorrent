package torrent

import (
	"testing"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

// buildPriorityTestTorrent lays out three files against a 16384-byte piece
// length so file B straddles a piece boundary with file A: piece 0 is all
// A, piece 1 is A's tail plus all of B, piece 2 is all of C. That straddle
// is deliberate — it's the only way to actually exercise "a piece touching
// two files with different priorities" at all.
func buildPriorityTestTorrent(t *testing.T) *metainfo.MetaInfo {
	t.Helper()
	mi, _ := buildTorrent(t, "priority-test", 16384, []fileSpec{
		{path: []string{"a.bin"}, length: 20000},
		{path: []string{"b.bin"}, length: 12768},
		{path: []string{"c.bin"}, length: 16384},
	})
	return mi
}

func TestPiecePrioritiesWithNoSelectionIsAllNormal(t *testing.T) {
	mi := buildPriorityTestTorrent(t)
	got := piecePriorities(mi, nil)
	want := []picker.Priority{picker.PriorityNormal, picker.PriorityNormal, picker.PriorityNormal}
	assertPriorities(t, got, want)
}

func TestPiecePrioritiesSkipsAWhollyOwnedPiece(t *testing.T) {
	mi := buildPriorityTestTorrent(t)
	// A and B normal, C skipped: piece 2 (wholly C) is the only skip piece.
	got := piecePriorities(mi, []picker.Priority{picker.PriorityNormal, picker.PriorityNormal, picker.PrioritySkip})
	want := []picker.Priority{picker.PriorityNormal, picker.PriorityNormal, picker.PrioritySkip}
	assertPriorities(t, got, want)
}

func TestPiecePrioritiesStraddlingPieceTakesTheHigherFilePriority(t *testing.T) {
	mi := buildPriorityTestTorrent(t)
	// A skipped, B high, C normal: piece 1 straddles A (skip) and B (high) -
	// it must still be downloaded, at B's priority, not silently dropped
	// just because A (part of the same piece) is skipped.
	got := piecePriorities(mi, []picker.Priority{picker.PrioritySkip, picker.PriorityHigh, picker.PriorityNormal})
	want := []picker.Priority{picker.PrioritySkip, picker.PriorityHigh, picker.PriorityNormal}
	assertPriorities(t, got, want)
}

func TestPiecePrioritiesAllSkipped(t *testing.T) {
	mi := buildPriorityTestTorrent(t)
	got := piecePriorities(mi, []picker.Priority{picker.PrioritySkip, picker.PrioritySkip, picker.PrioritySkip})
	want := []picker.Priority{picker.PrioritySkip, picker.PrioritySkip, picker.PrioritySkip}
	assertPriorities(t, got, want)
}

func TestNormalizedFilePrioritiesPadsShortSlices(t *testing.T) {
	mi := buildPriorityTestTorrent(t)
	got := normalizedFilePriorities(mi, []picker.Priority{picker.PriorityHigh})
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3 (one per file)", len(got))
	}
	if got[0] != picker.PriorityHigh {
		t.Fatalf("got[0] = %s, want high (from the caller's slice)", got[0])
	}
	if got[1] != picker.PriorityNormal || got[2] != picker.PriorityNormal {
		t.Fatalf("got %v, want the missing entries padded with normal", got)
	}
}

func TestPiecePrioritiesSingleFileTorrent(t *testing.T) {
	mi, _ := buildTorrent(t, "single", 16384, []fileSpec{{length: 16384 * 2}})
	got := piecePriorities(mi, []picker.Priority{picker.PriorityHigh})
	want := []picker.Priority{picker.PriorityHigh, picker.PriorityHigh}
	assertPriorities(t, got, want)
}

func assertPriorities(t *testing.T, got, want []picker.Priority) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d priorities, want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("piece %d: got %s, want %s (full: got=%v want=%v)", i, got[i], want[i], got, want)
		}
	}
}
