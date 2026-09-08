package metainfo

import (
	"crypto/sha1"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func writeTempFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func TestBuildSingleFileRoundTrips(t *testing.T) {
	dir := t.TempDir()
	content := make([]byte, 40000)
	rand.New(rand.NewSource(1)).Read(content)
	src := writeTempFile(t, dir, "content.bin", content)

	raw, mi, err := Build(CreateOptions{
		Name:        "content.bin",
		PieceLength: 16384,
		Files:       []CreateFile{{SourcePath: src, Length: int64(len(content))}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	reparsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(Build's own output): %v", err)
	}
	if reparsed.InfoHash != mi.InfoHash {
		t.Fatal("Build's returned MetaInfo and a fresh Parse of its raw bytes disagree on InfoHash")
	}
	if mi.Info.Length != int64(len(content)) {
		t.Fatalf("Info.Length = %d, want %d", mi.Info.Length, len(content))
	}
	if mi.Info.IsMultiFile() {
		t.Fatal("single file should not produce a multi-file torrent")
	}

	// The real point: hashing every piece of the source content by hand
	// must match what Build computed, proving the piece boundaries are
	// exactly BEP 3's (a flat byte stream, not per-file).
	wantHashes := manualPieceHashes(content, 16384)
	if len(mi.PieceHashes) != len(wantHashes) {
		t.Fatalf("got %d piece hashes, want %d", len(mi.PieceHashes), len(wantHashes))
	}
	for i := range wantHashes {
		if mi.PieceHashes[i] != wantHashes[i] {
			t.Fatalf("piece %d hash mismatch", i)
		}
	}
}

func TestBuildMultiFileHashesAcrossFileBoundaries(t *testing.T) {
	dir := t.TempDir()
	a := make([]byte, 10000)
	b := make([]byte, 10000)
	rand.New(rand.NewSource(2)).Read(a)
	rand.New(rand.NewSource(3)).Read(b)
	srcA := writeTempFile(t, dir, "a.bin", a)
	srcB := writeTempFile(t, dir, "b.bin", b)

	_, mi, err := Build(CreateOptions{
		Name:        "Bundle",
		PieceLength: 16384,
		Files: []CreateFile{
			{Path: []string{"a.bin"}, SourcePath: srcA, Length: int64(len(a))},
			{Path: []string{"b.bin"}, SourcePath: srcB, Length: int64(len(b))},
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !mi.Info.IsMultiFile() {
		t.Fatal("two files should produce a multi-file torrent")
	}
	if mi.TotalLength != int64(len(a)+len(b)) {
		t.Fatalf("TotalLength = %d, want %d", mi.TotalLength, len(a)+len(b))
	}

	// Piece 1 (index 16384..32767) straddles a.bin's tail and b.bin's head -
	// concatenate the real bytes the same way BEP 3 does and confirm the
	// hash matches, proving file boundaries don't reset piece hashing.
	concatenated := append(append([]byte(nil), a...), b...)
	want := manualPieceHashes(concatenated, 16384)
	for i := range want {
		if mi.PieceHashes[i] != want[i] {
			t.Fatalf("piece %d hash mismatch across the file boundary", i)
		}
	}
}

func TestBuildChoosesAPieceLengthWhenNoneGiven(t *testing.T) {
	dir := t.TempDir()
	content := make([]byte, 1000)
	src := writeTempFile(t, dir, "x.bin", content)

	_, mi, err := Build(CreateOptions{
		Name:  "x.bin",
		Files: []CreateFile{{SourcePath: src, Length: int64(len(content))}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if mi.Info.PieceLength < MinPieceLength || mi.Info.PieceLength > MaxPieceLength {
		t.Fatalf("chosen piece length %d out of bounds", mi.Info.PieceLength)
	}
}

func TestBuildSetsPrivateFlag(t *testing.T) {
	dir := t.TempDir()
	src := writeTempFile(t, dir, "p.bin", []byte("hello"))

	_, mi, err := Build(CreateOptions{
		Name:        "p.bin",
		PieceLength: MinPieceLength,
		Private:     true,
		Files:       []CreateFile{{SourcePath: src, Length: 5}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !mi.Info.Private {
		t.Fatal("Info.Private = false, want true")
	}
}

func TestBuildRejectsAFileShorterThanDeclared(t *testing.T) {
	dir := t.TempDir()
	src := writeTempFile(t, dir, "short.bin", []byte("only ten b"))

	_, _, err := Build(CreateOptions{
		Name:        "short.bin",
		PieceLength: MinPieceLength,
		Files:       []CreateFile{{SourcePath: src, Length: 1000}}, // lies about the length
	})
	if err == nil {
		t.Fatal("Build accepted a file shorter than its declared length")
	}
}

func TestBuildRejectsNoFiles(t *testing.T) {
	if _, _, err := Build(CreateOptions{Name: "x"}); err == nil {
		t.Fatal("Build accepted an empty file list")
	}
}

func TestBuildWritesTrackersAndWebSeeds(t *testing.T) {
	dir := t.TempDir()
	src := writeTempFile(t, dir, "t.bin", []byte("hello"))

	_, mi, err := Build(CreateOptions{
		Name:         "t.bin",
		PieceLength:  MinPieceLength,
		Files:        []CreateFile{{SourcePath: src, Length: 5}},
		Announce:     "http://tracker.example/announce",
		AnnounceList: [][]string{{"http://tracker.example/announce"}, {"udp://tracker2.example:80"}},
		UrlList:      []string{"http://webseed.example/t.bin"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if mi.Announce != "http://tracker.example/announce" {
		t.Fatalf("Announce = %q", mi.Announce)
	}
	if len(mi.AnnounceList) != 2 {
		t.Fatalf("AnnounceList = %v, want 2 tiers", mi.AnnounceList)
	}
	if len(mi.UrlList) != 1 || mi.UrlList[0] != "http://webseed.example/t.bin" {
		t.Fatalf("UrlList = %v", mi.UrlList)
	}
}

func manualPieceHashes(content []byte, pieceLength int) []Hash {
	var out []Hash
	for off := 0; off < len(content); off += pieceLength {
		end := off + pieceLength
		if end > len(content) {
			end = len(content)
		}
		out = append(out, sha1.Sum(content[off:end]))
	}
	return out
}
