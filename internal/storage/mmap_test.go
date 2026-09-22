package storage

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"
)

// Every test in this file exists specifically to exercise the real,
// platform-specific mmap implementation this process is actually running
// on (mmap_unix.go's real syscall.Mmap, or mmap_windows.go's real
// CreateFileMappingW/MapViewOfFile — whichever GOOS built this binary),
// not a mock of either, via WithMmap(true).

// TestMmapWriteThenReadRoundTrip proves the most basic real claim this
// backend makes: bytes written through a real memory-mapped view come
// back correctly on a real read through the same mapping.
func TestMmapWriteThenReadRoundTrip(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "mmap-roundtrip", pieceLength, []fileSpec{
		{length: pieceLength*3 + 500}, // a short final piece
	})
	dir := t.TempDir()
	s, err := New(dir, mi, WithMmap(true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	if err := s.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	if _, err := s.WriteAt(content, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	got := make([]byte, len(content))
	if _, err := s.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("mmap-backed read does not match what was written")
	}
}

// TestMmapCrossesFileBoundaries proves a write/read spanning two mapped
// files (two independent mmapRegions) works correctly, the mmap-backed
// counterpart to TestWriteAcrossFileBoundaries.
func TestMmapCrossesFileBoundaries(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "mmap-boundary", pieceLength, []fileSpec{
		{path: []string{"a.bin"}, length: 20000},
		{path: []string{"b.bin"}, length: 12768},
	})
	dir := t.TempDir()
	s, err := New(dir, mi, WithMmap(true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	if err := s.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	// A single write straddling the a.bin/b.bin boundary.
	const off = 19000
	chunk := content[off : off+2000]
	if _, err := s.WriteAt(chunk, off); err != nil {
		t.Fatalf("WriteAt across boundary: %v", err)
	}
	got := make([]byte, len(chunk))
	if _, err := s.ReadAt(got, off); err != nil {
		t.Fatalf("ReadAt across boundary: %v", err)
	}
	if !bytes.Equal(got, chunk) {
		t.Fatal("cross-file-boundary mmap read does not match what was written")
	}

	rawA, err := os.ReadFile(s.files[0].Path)
	if err != nil {
		t.Fatalf("reading a.bin directly: %v", err)
	}
	if !bytes.Equal(rawA[19000:20000], content[19000:20000]) {
		t.Fatal("a.bin's own tail is wrong on real disk after a cross-boundary mmap write")
	}
	rawB, err := os.ReadFile(s.files[1].Path)
	if err != nil {
		t.Fatalf("reading b.bin directly: %v", err)
	}
	if !bytes.Equal(rawB[:1000], content[20000:21000]) {
		t.Fatal("b.bin's own head is wrong on real disk after a cross-boundary mmap write")
	}
}

// TestMmapSyncThenCloseIsVisibleToAFreshRead writes through one Storage,
// Sync()s and Close()s it (which unmaps everything), then reads the raw
// file with a completely independent os.ReadFile call rather than
// checking this same process's own still-mapped memory — real proof the
// bytes actually left the mapping, not just that this process's own view
// of it looks right.
//
// Honest limitation, found empirically rather than assumed: this does
// NOT prove Sync's platform-specific FlushViewOfFile-before-
// FlushFileBuffers ordering (mmap.go's own doc comment) is load-bearing.
// Tested directly — temporarily skipping the FlushViewOfFile call and
// rerunning this test left it passing every time, on this real machine.
// That is not proof the ordering is unnecessary; it means a test built
// on "read the file back afterward" cannot observe the difference in
// the first place, since a fresh read goes through the OS's own file
// cache either way, which reflects the latest writes regardless of
// whether they have actually reached physical storage yet. Genuine
// crash-durability (do the bytes survive real power loss) needs a kind
// of test this suite doesn't attempt — the FlushViewOfFile call is kept
// because it is Microsoft's own documented requirement for durable
// memory-mapped writes, not because this test proves it matters.
func TestMmapSyncThenCloseIsVisibleToAFreshRead(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "mmap-sync", pieceLength, []fileSpec{{length: pieceLength * 4}})
	dir := t.TempDir()
	s, err := New(dir, mi, WithMmap(true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if _, err := s.WriteAt(content, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if err := s.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	raw, err := os.ReadFile(s.files[0].Path)
	if err != nil {
		t.Fatalf("reading the file directly after Sync+Close: %v", err)
	}
	if !bytes.Equal(raw, content) {
		t.Fatal("content on real disk does not match what was written through the mmap — Sync did not actually make it durable")
	}
}

// TestMmapConcurrentWrites is the mmap-backed counterpart to
// TestConcurrentWrites: many goroutines writing disjoint, piece-aligned
// ranges through the same mapped regions at once must never corrupt
// each other's bytes — the same "safe for concurrent use on
// non-overlapping ranges" contract io.WriterAt already promises, which
// a raw memory copy into shared mapped memory has to uphold just as
// much as a real WriteAt syscall does.
func TestMmapConcurrentWrites(t *testing.T) {
	const pieceLength = 16384
	const pieces = 32
	mi, content := buildTorrent(t, "mmap-concurrent", pieceLength, []fileSpec{
		{path: []string{"a.bin"}, length: pieceLength * 10},
		{path: []string{"b.bin"}, length: pieceLength * 22},
	})
	dir := t.TempDir()
	s, err := New(dir, mi, WithMmap(true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	if err := s.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, pieces)
	for i := 0; i < pieces; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			off := int64(i) * pieceLength
			_, errs[i] = s.WriteAt(content[off:off+pieceLength], off)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("piece %d: %v", i, err)
		}
	}

	got := make([]byte, len(content))
	if _, err := s.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("concurrent mmap writes produced corrupted content")
	}
}

// TestMmapVerify proves Storage.Verify/VerifyOne — the actual
// production consumer of ReadAt, via internal/torrent's own
// verifyPiece — work correctly against mmap-backed content: every
// written piece verifies, and the one piece deliberately left unwritten
// (still zeros) correctly fails.
func TestMmapVerify(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "mmap-verify", pieceLength, []fileSpec{
		{path: []string{"a.bin"}, length: pieceLength * 3},
		{path: []string{"b.bin"}, length: pieceLength + 77},
	})
	dir := t.TempDir()
	s, err := New(dir, mi, WithMmap(true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	if err := s.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	for i := 0; i < mi.NumPieces(); i++ {
		if i == 1 {
			continue // deliberately left unwritten
		}
		off := int64(i) * pieceLength
		if _, err := s.WriteAt(content[off:off+mi.PieceLen(i)], off); err != nil {
			t.Fatalf("WriteAt piece %d: %v", i, err)
		}
	}

	res, err := s.Verify(context.Background(), mi, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Complete != mi.NumPieces()-1 {
		t.Fatalf("Verify.Complete = %d, want %d (every piece but the unwritten one)", res.Complete, mi.NumPieces()-1)
	}

	ok, err := s.VerifyOne(context.Background(), mi, 1)
	if err != nil {
		t.Fatalf("VerifyOne(1): %v", err)
	}
	if ok {
		t.Fatal("VerifyOne(1) reported true for a piece that was never written")
	}
}

// TestMmapCacheBoundsOpenMappings is the mmap-backed counterpart to
// TestHandleCacheBoundsOpenFiles: WithMaxOpenFiles must bound how many
// memory mappings are held at once, and eviction must not lose data —
// a later read of an evicted-then-remapped file has to return the real
// content, not stale or zeroed memory.
func TestMmapCacheBoundsOpenMappings(t *testing.T) {
	const pieceLength = 16384
	var files []fileSpec
	for i := 0; i < 40; i++ {
		files = append(files, fileSpec{path: []string{string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".bin"}, length: 512})
	}
	mi, content := buildTorrent(t, "mmap-many", pieceLength, files)

	dir := t.TempDir()
	s, err := New(dir, mi, WithMmap(true), WithMaxOpenFiles(4))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	if err := s.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	if _, err := s.WriteAt(content, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if got := s.mmaps.openCount(); got > 4 {
		t.Fatalf("mmap cache holds %d mappings, cap is 4", got)
	}

	got := make([]byte, len(content))
	if _, err := s.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("data is wrong after mmap cache eviction")
	}
}
