package storage

import (
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

type fileSpec struct {
	path   []string
	length int64
}

// buildTorrent produces a MetaInfo plus the content it describes, going
// through the real encoder and parser so the fixture cannot drift from what
// the rest of the client sees.
func buildTorrent(t *testing.T, name string, pieceLength int64, files []fileSpec) (*metainfo.MetaInfo, []byte) {
	t.Helper()

	var total int64
	for _, f := range files {
		total += f.length
	}
	content := make([]byte, total)
	rand.New(rand.NewSource(7)).Read(content)

	var hashes []byte
	for off := int64(0); off < total; off += pieceLength {
		end := min(off+pieceLength, total)
		sum := sha1.Sum(content[off:end])
		hashes = append(hashes, sum[:]...)
	}

	type fileWire struct {
		Length int64    `bencode:"length"`
		Path   []string `bencode:"path"`
	}
	type infoWire struct {
		Files       []fileWire `bencode:"files,omitempty"`
		Length      int64      `bencode:"length,omitempty"`
		Name        string     `bencode:"name"`
		PieceLength int64      `bencode:"piece length"`
		Pieces      []byte     `bencode:"pieces"`
	}

	info := infoWire{Name: name, PieceLength: pieceLength, Pieces: hashes}
	if len(files) == 1 && len(files[0].path) == 0 {
		info.Length = files[0].length
	} else {
		for _, f := range files {
			info.Files = append(info.Files, fileWire{Length: f.length, Path: f.path})
		}
	}

	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	torrentBytes, err := bencode.Marshal(struct {
		Announce string             `bencode:"announce"`
		Info     bencode.RawMessage `bencode:"info"`
	}{Announce: "http://127.0.0.1:1/announce", Info: infoBytes})
	if err != nil {
		t.Fatalf("marshal torrent: %v", err)
	}

	mi, err := metainfo.Parse(torrentBytes)
	if err != nil {
		t.Fatalf("parse torrent: %v", err)
	}
	return mi, content
}

func newStorage(t *testing.T, mi *metainfo.MetaInfo, opts ...Option) (*Storage, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(dir, mi, opts...)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	return s, dir
}

// TestWriteAcrossFileBoundaries is the property the whole package exists for:
// callers address the torrent as one flat byte space and never see the file
// boundaries.
func TestWriteAcrossFileBoundaries(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "Bundle", pieceLength, []fileSpec{
		{path: []string{"first.bin"}, length: 1000},
		{path: []string{"second.bin"}, length: 500},
		{path: []string{"sub", "third.bin"}, length: pieceLength*2 - 1500},
	})

	s, dir := newStorage(t, mi)

	// One write spanning all three files in a single call.
	if n, err := s.WriteAt(content, 0); err != nil || n != len(content) {
		t.Fatalf("WriteAt = %d, %v; want %d, nil", n, err, len(content))
	}

	// The bytes must land in the right files at the right offsets.
	for _, tc := range []struct {
		rel  []string
		from int64
		size int64
	}{
		{[]string{"first.bin"}, 0, 1000},
		{[]string{"second.bin"}, 1000, 500},
		{[]string{"sub", "third.bin"}, 1500, int64(len(content)) - 1500},
	} {
		path := filepath.Join(append([]string{dir, "Bundle"}, tc.rel...)...)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %v: %v", tc.rel, err)
		}
		want := content[tc.from : tc.from+tc.size]
		if !bytes.Equal(got, want) {
			t.Fatalf("%v holds the wrong bytes (%d of %d)", tc.rel, len(got), len(want))
		}
	}

	// And reading it back through the same abstraction must round trip.
	readBack := make([]byte, len(content))
	if _, err := s.ReadAt(readBack, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(readBack, content) {
		t.Fatal("ReadAt did not return what WriteAt stored")
	}
}

func TestReadAtPartialRanges(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "Bundle", pieceLength, []fileSpec{
		{path: []string{"a"}, length: 100},
		{path: []string{"b"}, length: pieceLength - 100},
	})
	s, _ := newStorage(t, mi)
	if _, err := s.WriteAt(content, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	for _, tc := range []struct{ off, size int64 }{
		{0, 50},                      // inside the first file
		{50, 100},                    // straddling the boundary
		{100, 200},                   // start exactly on the boundary
		{99, 2},                      // one byte either side
		{int64(len(content)) - 1, 1}, // final byte
		{0, int64(len(content))},     // everything
	} {
		buf := make([]byte, tc.size)
		if _, err := s.ReadAt(buf, tc.off); err != nil {
			t.Fatalf("ReadAt(%d, %d): %v", tc.off, tc.size, err)
		}
		if !bytes.Equal(buf, content[tc.off:tc.off+tc.size]) {
			t.Fatalf("ReadAt(%d, %d) returned the wrong bytes", tc.off, tc.size)
		}
	}
}

func TestReadAtOutOfRange(t *testing.T) {
	mi, _ := buildTorrent(t, "x.bin", 16384, []fileSpec{{length: 16384}})
	s, _ := newStorage(t, mi)

	if _, err := s.ReadAt(make([]byte, 1), 16384); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt past the end = %v, want io.EOF", err)
	}
	if _, err := s.ReadAt(make([]byte, 1), -1); err == nil {
		t.Fatal("ReadAt at a negative offset returned no error")
	}
	// A read that starts inside the torrent but runs past the end returns the
	// bytes it could get plus io.EOF, per the io.ReaderAt contract.
	n, err := s.ReadAt(make([]byte, 100), 16330)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt overrunning the end = %v, want io.EOF", err)
	}
	if n != 54 {
		t.Fatalf("ReadAt overrunning the end read %d bytes, want 54", n)
	}
}

func TestWriteAtRefusesOverrun(t *testing.T) {
	mi, _ := buildTorrent(t, "x.bin", 16384, []fileSpec{{length: 16384}})
	s, _ := newStorage(t, mi)

	if _, err := s.WriteAt(make([]byte, 100), 16350); err == nil {
		t.Fatal("WriteAt past the end of the torrent returned no error")
	}
	if _, err := s.WriteAt(make([]byte, 1), -1); err == nil {
		t.Fatal("WriteAt at a negative offset returned no error")
	}
}

// TestConcurrentWrites is why the package uses WriteAt rather than Seek+Write:
// several goroutines writing different pieces share one handle.
func TestConcurrentWrites(t *testing.T) {
	const pieceLength = 16384
	const pieces = 32
	mi, content := buildTorrent(t, "Bundle", pieceLength, []fileSpec{
		{path: []string{"a.bin"}, length: pieceLength * 10},
		{path: []string{"b.bin"}, length: pieceLength * 22},
	})
	s, _ := newStorage(t, mi)

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
		t.Fatal("concurrent writes produced the wrong file contents")
	}
}

func TestVerify(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "Bundle", pieceLength, []fileSpec{
		{path: []string{"a.bin"}, length: pieceLength * 3},
		{path: []string{"b.bin"}, length: pieceLength + 77}, // short final piece
	})
	s, _ := newStorage(t, mi)

	// Nothing written yet: allocated files are all zeros, so no piece verifies.
	res, err := s.Verify(context.Background(), mi, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify on an empty download: %v", err)
	}
	if res.Complete != 0 || res.Total != mi.NumPieces() {
		t.Fatalf("Verify = %+v, want 0 of %d", res, mi.NumPieces())
	}

	// Write everything except the second piece.
	for i := 0; i < mi.NumPieces(); i++ {
		if i == 1 {
			continue
		}
		off := int64(i) * pieceLength
		if _, err := s.WriteAt(content[off:off+mi.PieceLen(i)], off); err != nil {
			t.Fatalf("WriteAt piece %d: %v", i, err)
		}
	}

	var mu sync.Mutex
	seen := make(map[int]bool)
	var lastDone, lastTotal int
	res, err = s.Verify(context.Background(), mi, VerifyOptions{
		Workers: 3,
		OnPiece: func(index int, ok bool) {
			mu.Lock()
			seen[index] = ok
			mu.Unlock()
		},
		OnProgress: func(done, total int) {
			mu.Lock()
			if done > lastDone {
				lastDone, lastTotal = done, total
			}
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	want := mi.NumPieces() - 1
	if res.Complete != want {
		t.Fatalf("Verify found %d complete pieces, want %d", res.Complete, want)
	}
	if len(seen) != mi.NumPieces() {
		t.Fatalf("OnPiece fired for %d pieces, want %d", len(seen), mi.NumPieces())
	}
	if seen[1] {
		t.Error("piece 1 was never written but verified anyway")
	}
	if !seen[0] || !seen[mi.NumPieces()-1] {
		t.Error("a written piece failed verification")
	}
	if lastDone != mi.NumPieces() || lastTotal != mi.NumPieces() {
		t.Errorf("progress ended at %d/%d, want %d/%d", lastDone, lastTotal, mi.NumPieces(), mi.NumPieces())
	}
}

func TestVerifyIsCancellable(t *testing.T) {
	const pieceLength = 16384
	mi, _ := buildTorrent(t, "big.bin", pieceLength, []fileSpec{{length: pieceLength * 500}})
	s, _ := newStorage(t, mi)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.Verify(ctx, mi, VerifyOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify with a cancelled context = %v, want context.Canceled", err)
	}
}

func TestAllocateIsIdempotent(t *testing.T) {
	mi, content := buildTorrent(t, "x.bin", 16384, []fileSpec{{length: 16384 * 2}})
	s, dir := newStorage(t, mi)

	if _, err := s.WriteAt(content, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	// A second Storage over the same directory must not clobber the data.
	s2, err := New(dir, mi)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s2.Close()
	if err := s2.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	got := make([]byte, len(content))
	if _, err := s2.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("re-allocating an existing download destroyed its data")
	}
}

func TestFullAllocation(t *testing.T) {
	mi, _ := buildTorrent(t, "x.bin", 16384, []fileSpec{{length: 16384 * 4}})
	dir := t.TempDir()
	s, err := New(dir, mi, WithAllocation(Full))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	if err := s.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "x.bin"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != mi.TotalLength {
		t.Fatalf("file is %d bytes, want %d", info.Size(), mi.TotalLength)
	}
}

// TestHandleCacheBoundsOpenFiles checks the LRU actually evicts. Without it a
// torrent with thousands of files exhausts the process file descriptor limit.
func TestHandleCacheBoundsOpenFiles(t *testing.T) {
	const pieceLength = 16384
	var files []fileSpec
	for i := 0; i < 40; i++ {
		files = append(files, fileSpec{path: []string{string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".bin"}, length: 512})
	}
	mi, content := buildTorrent(t, "Many", pieceLength, files)

	dir := t.TempDir()
	s, err := New(dir, mi, WithMaxOpenFiles(4))
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
	if got := s.cache.openCount(); got > 4 {
		t.Fatalf("cache holds %d handles, cap is 4", got)
	}

	got := make([]byte, len(content))
	if _, err := s.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("data is wrong after handle eviction")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := s.cache.openCount(); got != 0 {
		t.Fatalf("%d handles still open after Close", got)
	}
}

func TestZeroLengthFilesAreCreated(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "Bundle", pieceLength, []fileSpec{
		{path: []string{"empty.txt"}, length: 0},
		{path: []string{"data.bin"}, length: pieceLength},
	})
	s, dir := newStorage(t, mi)

	if _, err := s.WriteAt(content, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "Bundle", "empty.txt"))
	if err != nil {
		t.Fatalf("the zero-length file was not created: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("empty.txt is %d bytes", info.Size())
	}

	got := make([]byte, len(content))
	if _, err := s.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("a zero-length file broke the offset arithmetic")
	}
}

func TestSingleFileLayout(t *testing.T) {
	mi, content := buildTorrent(t, "solo.bin", 16384, []fileSpec{{length: 16384}})
	s, dir := newStorage(t, mi)

	if _, err := s.WriteAt(content, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	// A single-file torrent writes straight into the download directory, with
	// no wrapper directory.
	got, err := os.ReadFile(filepath.Join(dir, "solo.bin"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("single-file torrent landed in the wrong place")
	}
}

func TestAvailableSpaceReportsSomething(t *testing.T) {
	n, err := availableSpace(t.TempDir())
	if err != nil {
		t.Skipf("free space is not queryable here: %v", err)
	}
	if n <= 0 {
		t.Fatalf("availableSpace returned %d bytes", n)
	}
}
