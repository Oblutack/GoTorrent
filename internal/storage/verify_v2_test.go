package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// genBytesV2 mirrors internal/metainfo's own test fixture generator (a
// Knuth multiplicative hash of the byte index), so content built here is
// real and non-trivial without embedding binary blobs.
func genBytesV2(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		x := uint32(i) * 2654435761
		out[i] = byte(x >> 24)
	}
	return out
}

// buildRealV2Torrent writes real file content to a source directory and
// builds a real v2 or hybrid MetaInfo from it via metainfo.Build — the
// same real fixture-over-mock discipline the rest of this project follows,
// reusing the already-tested BEP 52 write side rather than hand-encoding
// bencode a second time. Every file always gets a real (non-nil) path,
// even for a single file, so every fixture here is consistently
// multi-file-shaped — simpler than also covering the true single-file
// layout case, which internal/metainfo's own tests already cover.
func buildRealV2Torrent(t *testing.T, pieceLength int64, hybrid bool, files map[string][]byte) *metainfo.MetaInfo {
	t.Helper()
	srcDir := t.TempDir()

	var names []string
	for name := range files {
		names = append(names, name)
	}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}

	var createFiles []metainfo.CreateFile
	for _, name := range names {
		content := files[name]
		path := filepath.Join(srcDir, name)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		createFiles = append(createFiles, metainfo.CreateFile{
			Path: []string{name}, SourcePath: path, Length: int64(len(content)),
		})
	}

	opts := metainfo.CreateOptions{
		Name: "v2storagetest", PieceLength: pieceLength, MetaVersion: 2, Hybrid: hybrid, Files: createFiles,
	}
	_, mi, err := metainfo.Build(opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return mi
}

// writeContentForTorrent allocates s's real files (which, via Sparse
// allocation, already reads back as all-zero for anything never written —
// exactly what a BEP 47 padding file's content is defined to be) and
// writes each real file's content at its own real flat offset via
// s.WriteAt/s.Files() — the same path the real torrent actor uses, rather
// than this test independently reconstructing where Layout would have put
// each file (a real, easy way for a test fixture to silently drift from
// what Storage actually does).
func writeContentForTorrent(t *testing.T, s *Storage, mi *metainfo.MetaInfo, files map[string][]byte) {
	t.Helper()
	if err := s.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	for i, fr := range s.Files() {
		var name string
		switch {
		case mi.IsPureV2():
			p := mi.V2Files[i].Path
			name = p[len(p)-1]
		case mi.Info.IsMultiFile():
			f := mi.Info.Files[i]
			if f.IsPadding() {
				continue // already all-zero via Sparse allocation
			}
			name = f.Path[len(f.Path)-1]
		default:
			name = mi.Info.Name
		}
		content, ok := files[name]
		if !ok {
			t.Fatalf("no fixture content registered for file %q", name)
		}
		if _, err := s.WriteAt(content, fr.Offset); err != nil {
			t.Fatalf("WriteAt(%s, %d): %v", name, fr.Offset, err)
		}
	}
}

func TestVerifyPureV2MultiFile(t *testing.T) {
	const pieceLength = 32768
	files := map[string][]byte{
		"fileA.bin": genBytesV2(102400), // needs a real 'piece layers' entry
		"fileB.bin": genBytesV2(4096),   // sole-piece file
	}
	mi := buildRealV2Torrent(t, pieceLength, false, files)

	s, err := New(t.TempDir(), mi)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	writeContentForTorrent(t, s, mi, files)

	result, err := s.Verify(context.Background(), mi, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Complete != mi.NumPieces() {
		t.Fatalf("Verify: %d/%d pieces verified, want all %d", result.Complete, result.Total, mi.NumPieces())
	}
}

func TestVerifyPureV2DetectsCorruption(t *testing.T) {
	const pieceLength = 32768
	files := map[string][]byte{
		"fileA.bin": genBytesV2(102400),
		"fileB.bin": genBytesV2(4096),
	}
	mi := buildRealV2Torrent(t, pieceLength, false, files)

	s, err := New(t.TempDir(), mi)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	writeContentForTorrent(t, s, mi, files)

	// Corrupt one byte inside piece 0, via Storage's own real resolved
	// path for file 0 - never guessed independently.
	path := s.Files()[0].Path
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back %s: %v", path, err)
	}
	data[0] ^= 0xFF
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("rewriting %s: %v", path, err)
	}

	ok0, err := s.VerifyOne(context.Background(), mi, 0)
	if err != nil {
		t.Fatalf("VerifyOne(0): %v", err)
	}
	if ok0 {
		t.Error("VerifyOne(0) = true for corrupted content, want false")
	}
	ok1, err := s.VerifyOne(context.Background(), mi, 1)
	if err != nil {
		t.Fatalf("VerifyOne(1): %v", err)
	}
	if !ok1 {
		t.Error("VerifyOne(1) = false for untouched content, want true")
	}
}

func TestVerifyHybridMultiFileWithPadding(t *testing.T) {
	const pieceLength = 32768
	files := map[string][]byte{
		"fileA.bin": genBytesV2(50000), // forces a real BEP 47 padding file
		"fileB.bin": genBytesV2(20000),
	}
	mi := buildRealV2Torrent(t, pieceLength, true, files)
	if !mi.Info.IsMultiFile() {
		t.Fatal("test setup: expected a real multi-file hybrid torrent")
	}
	hasPadding := false
	for _, f := range mi.Info.Files {
		if f.IsPadding() {
			hasPadding = true
		}
	}
	if !hasPadding {
		t.Fatal("test setup: expected a real padding file to have been inserted")
	}

	s, err := New(t.TempDir(), mi)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	writeContentForTorrent(t, s, mi, files)

	result, err := s.Verify(context.Background(), mi, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Complete != mi.NumPieces() {
		t.Fatalf("Verify: %d/%d pieces verified, want all %d (real content + real padding on disk)", result.Complete, result.Total, mi.NumPieces())
	}
}

// TestVerifyHybridDetectsV1V2Disagreement constructs a hybrid torrent
// whose v1 and v2 descriptions structurally agree (same piece count, same
// total length — everything Parse's own validateHybridConsistency checks)
// but whose piece 0 hash values genuinely disagree, simulating a corrupt
// or maliciously-constructed .torrent file that passed parsing. This must
// be caught as a real error, never silently resolved either way.
func TestVerifyHybridDetectsV1V2Disagreement(t *testing.T) {
	const pieceLength = 32768
	files := map[string][]byte{"solo.bin": genBytesV2(102400)}
	mi := buildRealV2Torrent(t, pieceLength, true, files)

	// Tamper the v1 piece 0 hash directly - the real on-disk content will
	// still match v2 but no longer match this now-wrong v1 hash.
	mi.PieceHashes[0][0] ^= 0xFF

	s, err := New(t.TempDir(), mi)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	writeContentForTorrent(t, s, mi, files)

	ok, err := s.VerifyOne(context.Background(), mi, 0)
	if err == nil {
		t.Fatal("VerifyOne(0) succeeded despite a real v1/v2 hash disagreement, want an error")
	}
	if ok {
		t.Fatal("VerifyOne(0) = true despite an error, want false")
	}
}
