package metainfo

import (
	"crypto/sha1"
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func TestBuildV2PureMultiFile(t *testing.T) {
	dir := t.TempDir()
	const pieceLength = 32768
	contentA := genBytes(102400) // 4 pieces, last one short - needs piece_layers
	contentB := genBytes(4096)   // sole piece - no piece_layers entry

	pathA := writeTestFile(t, dir, "fileA.bin", contentA)
	pathB := writeTestFile(t, dir, "fileB.bin", contentB)

	opts := CreateOptions{
		Name:        "v2built",
		PieceLength: pieceLength,
		MetaVersion: 2,
		Files: []CreateFile{
			{Path: []string{"dirA", "fileA.bin"}, SourcePath: pathA, Length: int64(len(contentA))},
			{Path: []string{"fileB.bin"}, SourcePath: pathB, Length: int64(len(contentB))},
		},
	}

	_, mi, err := Build(opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if mi.MetaVersion != 2 {
		t.Fatalf("MetaVersion = %d, want 2", mi.MetaVersion)
	}
	if len(mi.PieceHashes) != 0 {
		t.Fatalf("PieceHashes should be empty for a pure-v2 build, got %d", len(mi.PieceHashes))
	}
	if len(mi.V2Files) != 2 {
		t.Fatalf("V2Files has %d entries, want 2", len(mi.V2Files))
	}

	wantRootA, _, wantPiecesA := hashFileV2(contentA, pieceLength)
	wantRootB, _, _ := hashFileV2(contentB, pieceLength)
	if mi.V2Files[0].PiecesRoot != Hash256(wantRootA) {
		t.Errorf("fileA PiecesRoot = %s, want %s", mi.V2Files[0].PiecesRoot, Hash256(wantRootA))
	}
	if mi.V2Files[1].PiecesRoot != Hash256(wantRootB) {
		t.Errorf("fileB PiecesRoot = %s, want %s", mi.V2Files[1].PiecesRoot, Hash256(wantRootB))
	}
	if wantPiecesA <= 1 {
		t.Fatal("test setup: fileA must need a real piece_layers entry")
	}
	if _, ok := mi.PieceLayers[Hash256(wantRootA)]; !ok {
		t.Error("PieceLayers is missing fileA's entry")
	}
	if _, ok := mi.PieceLayers[Hash256(wantRootB)]; ok {
		t.Error("PieceLayers has an entry for fileB, a sole-piece file that should have none")
	}

	wantTotal := int64(len(contentA) + len(contentB))
	if mi.TotalLength != wantTotal {
		t.Errorf("TotalLength = %d, want %d", mi.TotalLength, wantTotal)
	}
}

func TestBuildV2HybridSingleFile(t *testing.T) {
	dir := t.TempDir()
	const pieceLength = 32768
	content := genBytes(102400)
	path := writeTestFile(t, dir, "solo.bin", content)

	opts := CreateOptions{
		Name:        "solo.bin",
		PieceLength: pieceLength,
		MetaVersion: 2,
		Hybrid:      true,
		Files:       []CreateFile{{SourcePath: path, Length: int64(len(content))}},
	}

	_, mi, err := Build(opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(mi.PieceHashes) != 4 {
		t.Fatalf("PieceHashes has %d entries, want 4", len(mi.PieceHashes))
	}
	if len(mi.V2Files) != 1 {
		t.Fatalf("V2Files has %d entries, want 1", len(mi.V2Files))
	}
	if mi.Info.IsMultiFile() {
		t.Fatal("a single-file hybrid torrent should not be IsMultiFile()")
	}
	if mi.Info.Length != int64(len(content)) {
		t.Errorf("Info.Length = %d, want %d", mi.Info.Length, len(content))
	}

	// Independently recompute the v1 SHA-1 pieces directly from the real
	// file content and confirm they match exactly - single-file hybrid
	// needs no padding at all, so this is a plain flat-stream hash.
	for i := 0; i < 4; i++ {
		off := i * pieceLength
		end := off + pieceLength
		if end > len(content) {
			end = len(content)
		}
		want := sha1.Sum(content[off:end])
		if Hash(want) != mi.PieceHashes[i] {
			t.Errorf("piece %d = %x, want %x", i, mi.PieceHashes[i], want)
		}
	}
}

// TestBuildV2HybridMultiFileInsertsRealPadding is the real test of this
// feature's trickiest piece: a two-file hybrid torrent where the first
// file's content does not end on a piece boundary, forcing a real BEP 47
// padding file to be inserted so the second file starts piece-aligned in
// both the v1 and v2 descriptions.
func TestBuildV2HybridMultiFileInsertsRealPadding(t *testing.T) {
	dir := t.TempDir()
	const pieceLength = 32768 // 2 blocks/piece
	contentA := genBytes(50000)
	contentB := genBytes(20000)
	pathA := writeTestFile(t, dir, "fileA.bin", contentA)
	pathB := writeTestFile(t, dir, "fileB.bin", contentB)

	// fileA: 50000 bytes over 32768-byte pieces = 2 pieces, the second
	// (50000 - 32768 = 17232 bytes) short by 32768-17232 = 15536 bytes -
	// exactly the padding this test exists to confirm.
	const wantPadLen = pieceLength - (50000 - pieceLength)

	opts := CreateOptions{
		Name:        "hybridmulti",
		PieceLength: pieceLength,
		MetaVersion: 2,
		Hybrid:      true,
		Files: []CreateFile{
			{Path: []string{"fileA.bin"}, SourcePath: pathA, Length: int64(len(contentA))},
			{Path: []string{"fileB.bin"}, SourcePath: pathB, Length: int64(len(contentB))},
		},
	}

	_, mi, err := Build(opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if !mi.Info.IsMultiFile() {
		t.Fatal("expected a multi-file v1 description")
	}
	var padFile *FileInfo
	for i := range mi.Info.Files {
		if mi.Info.Files[i].IsPadding() {
			f := mi.Info.Files[i]
			padFile = &f
		}
	}
	if padFile == nil {
		t.Fatal("no BEP 47 padding file was inserted")
	}
	if padFile.Length != wantPadLen {
		t.Errorf("padding file length = %d, want %d", padFile.Length, wantPadLen)
	}

	// v1 piece count: fileA (2 pieces, second closed by padding) + fileB
	// (20000 bytes = 1 piece, since it starts fresh right after the
	// padding closes fileA's own piece exactly) = 3.
	if len(mi.PieceHashes) != 3 {
		t.Fatalf("PieceHashes has %d entries, want 3", len(mi.PieceHashes))
	}

	// Independently recompute all 3 v1 piece hashes by laying out the
	// real bytes exactly as a downloaded torrent's storage would: fileA,
	// then wantPadLen zero bytes, then fileB - flat SHA-1 over
	// pieceLength-sized chunks of that concatenated stream.
	flat := append(append([]byte{}, contentA...), make([]byte, wantPadLen)...)
	flat = append(flat, contentB...)
	for i := 0; i < 3; i++ {
		off := i * pieceLength
		end := off + pieceLength
		if end > len(flat) {
			end = len(flat)
		}
		want := sha1.Sum(flat[off:end])
		if Hash(want) != mi.PieceHashes[i] {
			t.Errorf("piece %d = %x, want %x (independently recomputed)", i, mi.PieceHashes[i], want)
		}
	}

	// v2 side: fileB must be unaffected by fileA's padding at all - its
	// own root is exactly what hashing its real bytes alone produces.
	wantRootB, _, _ := hashFileV2(contentB, pieceLength)
	if len(mi.V2Files) != 2 {
		t.Fatalf("V2Files has %d entries, want 2", len(mi.V2Files))
	}
	if mi.V2Files[1].PiecesRoot != Hash256(wantRootB) {
		t.Errorf("fileB v2 root = %s, want %s (padding must never leak into v2 hashing)", mi.V2Files[1].PiecesRoot, Hash256(wantRootB))
	}
}

func TestBuildV2RejectsNonPowerOfTwoPieceLength(t *testing.T) {
	dir := t.TempDir()
	content := genBytes(1000)
	path := writeTestFile(t, dir, "a.bin", content)
	opts := CreateOptions{
		Name:        "a.bin",
		PieceLength: MinPieceLength + 1, // not a power of two, still in range
		MetaVersion: 2,
		Files:       []CreateFile{{SourcePath: path, Length: int64(len(content))}},
	}
	if _, _, err := Build(opts); err == nil {
		t.Fatal("Build accepted a non-power-of-two piece length for MetaVersion 2")
	}
}

func TestBuildThenExportRoundTripsPieceLayers(t *testing.T) {
	dir := t.TempDir()
	const pieceLength = 32768
	content := genBytes(102400)
	path := writeTestFile(t, dir, "a.bin", content)
	opts := CreateOptions{
		Name: "a.bin", PieceLength: pieceLength, MetaVersion: 2,
		Files: []CreateFile{{SourcePath: path, Length: int64(len(content))}},
	}
	_, mi, err := Build(opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(mi.PieceLayers) == 0 {
		t.Fatal("test setup: expected a real PieceLayers entry")
	}

	exported, err := Export(mi, nil)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	reparsed, err := Parse(exported)
	if err != nil {
		t.Fatalf("re-parsing exported bytes: %v", err)
	}
	if len(reparsed.PieceLayers) != len(mi.PieceLayers) {
		t.Fatalf("re-parsed PieceLayers has %d entries, want %d (Export must not drop this top-level field)",
			len(reparsed.PieceLayers), len(mi.PieceLayers))
	}
	for k, v := range mi.PieceLayers {
		got, ok := reparsed.PieceLayers[k]
		if !ok {
			t.Fatalf("re-parsed PieceLayers is missing key %s", k)
		}
		if string(got) != string(v) {
			t.Fatalf("re-parsed PieceLayers[%s] differs from the original", k)
		}
	}
}
