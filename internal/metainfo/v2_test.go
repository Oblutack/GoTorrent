package metainfo

import (
	"crypto/sha1"
	"crypto/sha256"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/merkle"
)

// genBytes reproduces internal/merkle's own test fixture generator (a
// Knuth multiplicative hash of the byte index) so file content built here
// is real and non-trivial without embedding binary blobs.
func genBytes(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		x := uint32(i) * 2654435761
		out[i] = byte(x >> 24)
	}
	return out
}

// hashFileV2 computes a file's v2 merkle data the same way internal/merkle's
// own primitives are meant to be driven: per-piece leaf hashing, then
// combining piece roots into the file's overall root.
func hashFileV2(content []byte, pieceLength int64) (root merkle.Hash, pieceLayer []byte, numPieces int) {
	blocksPerPiece := int(pieceLength / merkle.BlockSize)
	var pieceRoots []merkle.Hash
	for off := 0; off < len(content); off += int(pieceLength) {
		end := off + int(pieceLength)
		if end > len(content) {
			end = len(content)
		}
		var leaves []merkle.Hash
		for bo := off; bo < end; bo += merkle.BlockSize {
			be := bo + merkle.BlockSize
			if be > end {
				be = end
			}
			leaves = append(leaves, merkle.Leaf(content[bo:be]))
		}
		soleFilePiece := len(content) <= int(pieceLength)
		pr := merkle.PieceRoot(leaves, blocksPerPiece, soleFilePiece)
		pieceRoots = append(pieceRoots, pr)
	}
	numPieces = len(pieceRoots)
	root = merkle.FileRoot(pieceRoots, blocksPerPiece)
	for _, pr := range pieceRoots {
		pieceLayer = append(pieceLayer, pr[:]...)
	}
	return root, pieceLayer, numPieces
}

// buildV2TorrentBytes hand-assembles a real, valid v2-only .torrent's raw
// bencode bytes for a two-file torrent: fileA (in a subdirectory, larger
// than one piece, so it needs a real 'piece layers' entry) and fileB (at
// the tree root, no larger than one piece, so its 'pieces root' stands
// alone with no layer entry). This exercises real nested-directory file
// tree parsing, not just a single flat file.
func buildV2TorrentBytes(t *testing.T, pieceLength int64, contentA, contentB []byte) []byte {
	t.Helper()

	rootA, layerA, numPiecesA := hashFileV2(contentA, pieceLength)
	rootB, _, numPiecesB := hashFileV2(contentB, pieceLength)
	if numPiecesB != 1 {
		t.Fatalf("test setup: fileB must be a single-piece file, got %d pieces", numPiecesB)
	}

	fileTree := map[string]any{
		"dirA": map[string]any{
			"fileA.bin": map[string]any{
				"": map[string]any{
					"length":      int64(len(contentA)),
					"pieces root": string(rootA[:]),
				},
			},
		},
		"fileB.bin": map[string]any{
			"": map[string]any{
				"length":      int64(len(contentB)),
				"pieces root": string(rootB[:]),
			},
		},
	}

	info := map[string]any{
		"name":         "v2test",
		"piece length": pieceLength,
		"meta version": int64(2),
		"file tree":    fileTree,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}

	top := map[string]any{
		"info": bencode.RawMessage(infoBytes),
	}
	if numPiecesA > 1 {
		top["piece layers"] = map[string]any{
			string(rootA[:]): string(layerA),
		}
	}
	data, err := bencode.Marshal(top)
	if err != nil {
		t.Fatalf("marshal top level: %v", err)
	}
	return data
}

func TestParseV2TwoFileTorrent(t *testing.T) {
	const pieceLength = 32768 // 2 blocks/piece
	contentA := genBytes(102400)
	contentB := genBytes(4096)

	data := buildV2TorrentBytes(t, pieceLength, contentA, contentB)
	mi, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if mi.MetaVersion != 2 {
		t.Fatalf("MetaVersion = %d, want 2", mi.MetaVersion)
	}
	if len(mi.PieceHashes) != 0 {
		t.Fatalf("PieceHashes should be empty for a pure-v2 torrent, got %d", len(mi.PieceHashes))
	}
	if len(mi.V2Files) != 2 {
		t.Fatalf("V2Files has %d entries, want 2", len(mi.V2Files))
	}

	// File tree keys sort as "dirA" < "fileB.bin" (byte-wise), so fileA
	// comes first in path order - confirming the sorted-key walk.
	if got := mi.V2Files[0].Path; len(got) != 2 || got[0] != "dirA" || got[1] != "fileA.bin" {
		t.Fatalf("V2Files[0].Path = %v, want [dirA fileA.bin]", got)
	}
	if got := mi.V2Files[1].Path; len(got) != 1 || got[0] != "fileB.bin" {
		t.Fatalf("V2Files[1].Path = %v, want [fileB.bin]", got)
	}
	if mi.V2Files[0].Length != int64(len(contentA)) {
		t.Errorf("V2Files[0].Length = %d, want %d", mi.V2Files[0].Length, len(contentA))
	}
	if mi.V2Files[1].Length != int64(len(contentB)) {
		t.Errorf("V2Files[1].Length = %d, want %d", mi.V2Files[1].Length, len(contentB))
	}

	// InfoHashV2 must be the real SHA-256 of the exact info bytes, the
	// same substring InfoHash (SHA-1) is computed over.
	wantV2 := Hash256(sha256.Sum256(mi.InfoBytes))
	if mi.InfoHashV2 != wantV2 {
		t.Errorf("InfoHashV2 = %s, want %s", mi.InfoHashV2, wantV2)
	}
	wantV1 := Hash(sha1.Sum(mi.InfoBytes))
	if mi.InfoHash != wantV1 {
		t.Errorf("InfoHash = %s, want %s (must still be computed even for a v2 torrent)", mi.InfoHash, wantV1)
	}

	wantTotal := int64(len(contentA) + len(contentB))
	if mi.TotalLength != wantTotal {
		t.Errorf("TotalLength = %d, want %d", mi.TotalLength, wantTotal)
	}

	// Piece geometry: fileA occupies ceil(102400/32768)=4 pieces (indices
	// 0-3), fileB occupies its own 1 piece starting at index 4 - v2 never
	// packs a second file's data into a partially-used piece.
	wantNumPieces := 4 + 1
	if got := mi.NumPieces(); got != wantNumPieces {
		t.Fatalf("NumPieces() = %d, want %d", got, wantNumPieces)
	}
	for i := 0; i < 4; i++ {
		fi, off, ok := mi.PieceFile(i)
		if !ok || fi != 0 || off != int64(i)*pieceLength {
			t.Errorf("PieceFile(%d) = (%d, %d, %v), want (0, %d, true)", i, fi, off, ok, int64(i)*pieceLength)
		}
	}
	fi, off, ok := mi.PieceFile(4)
	if !ok || fi != 1 || off != 0 {
		t.Errorf("PieceFile(4) = (%d, %d, %v), want (1, 0, true)", fi, off, ok)
	}
	if _, _, ok := mi.PieceFile(5); ok {
		t.Error("PieceFile(5) succeeded, want false (out of range)")
	}

	// PieceLen: pieces 0-2 are full, piece 3 (fileA's last, 102400 -
	// 3*32768 = 4096 bytes) is short, piece 4 (fileB, the whole 4096-byte
	// file in one piece) is also short.
	for i := 0; i < 3; i++ {
		if got := mi.PieceLen(i); got != pieceLength {
			t.Errorf("PieceLen(%d) = %d, want %d", i, got, pieceLength)
		}
	}
	if got := mi.PieceLen(3); got != 4096 {
		t.Errorf("PieceLen(3) = %d, want 4096", got)
	}
	if got := mi.PieceLen(4); got != 4096 {
		t.Errorf("PieceLen(4) = %d, want 4096", got)
	}
}

func TestParseV2RejectsAMismatchedPieceLayersEntry(t *testing.T) {
	const pieceLength = 32768
	contentA := genBytes(102400)
	contentB := genBytes(4096)

	rootA, layerA, numPiecesA := hashFileV2(contentA, pieceLength)
	rootB, _, _ := hashFileV2(contentB, pieceLength)
	if numPiecesA <= 1 {
		t.Fatal("test setup: fileA must need a real 'piece layers' entry")
	}

	// Flip a byte inside fileA's own layer value - it no longer
	// reconstructs the pinned 'pieces root', independent of any bencode
	// framing byte, so this can only fail the merkle check, nothing else.
	corruptLayer := append([]byte(nil), layerA...)
	corruptLayer[0] ^= 0xFF

	fileTree := map[string]any{
		"dirA": map[string]any{
			"fileA.bin": map[string]any{
				"": map[string]any{"length": int64(len(contentA)), "pieces root": string(rootA[:])},
			},
		},
		"fileB.bin": map[string]any{
			"": map[string]any{"length": int64(len(contentB)), "pieces root": string(rootB[:])},
		},
	}
	info := map[string]any{
		"name": "v2test", "piece length": int64(pieceLength), "meta version": int64(2), "file tree": fileTree,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	top := map[string]any{
		"info":         bencode.RawMessage(infoBytes),
		"piece layers": map[string]any{string(rootA[:]): string(corruptLayer)},
	}
	data, err := bencode.Marshal(top)
	if err != nil {
		t.Fatalf("marshal top level: %v", err)
	}

	if _, err := Parse(data); err == nil {
		t.Fatal("Parse accepted a 'piece layers' entry that does not reconstruct its pinned 'pieces root'")
	}
}

func TestParseV2AcceptsAbsentPieceLayers(t *testing.T) {
	// The BEP 9 magnet path (ParseInfo) never has a top-level 'piece
	// layers' dict to give - a v2 MetaInfo must still parse successfully
	// from the info dictionary alone, with PieceLayers left empty.
	const pieceLength = 65536 // large enough that a 40KiB file is sole-piece
	content := genBytes(40000)
	root, _, numPieces := hashFileV2(content, pieceLength)
	if numPieces != 1 {
		t.Fatalf("test setup: want a sole-piece file, got %d pieces", numPieces)
	}

	fileTree := map[string]any{
		"solo.bin": map[string]any{
			"": map[string]any{
				"length":      int64(len(content)),
				"pieces root": string(root[:]),
			},
		},
	}
	info := map[string]any{
		"name":         "v2solo",
		"piece length": int64(pieceLength),
		"meta version": int64(2),
		"file tree":    fileTree,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}

	mi, err := ParseInfo(infoBytes)
	if err != nil {
		t.Fatalf("ParseInfo: %v", err)
	}
	if len(mi.PieceLayers) != 0 {
		t.Fatalf("PieceLayers should be empty (never delivered by BEP 9), got %d entries", len(mi.PieceLayers))
	}
	if mi.NumPieces() != 1 {
		t.Fatalf("NumPieces() = %d, want 1", mi.NumPieces())
	}
}

func TestParseV2RejectsUnsupportedMetaVersion(t *testing.T) {
	info := map[string]any{
		"name":         "bad",
		"piece length": int64(32768),
		"meta version": int64(3),
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := ParseInfo(infoBytes); err == nil {
		t.Fatal("ParseInfo accepted meta version 3, want a rejection (only version 2 is known)")
	}
}

func TestParseV2RejectsNonPowerOfTwoPieceLength(t *testing.T) {
	fileTree := map[string]any{
		"a.bin": map[string]any{"": map[string]any{"length": int64(100)}},
	}
	info := map[string]any{
		"name":         "bad",
		"piece length": int64(100000), // not a power of two
		"meta version": int64(2),
		"file tree":    fileTree,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := ParseInfo(infoBytes); err == nil {
		t.Fatal("ParseInfo accepted a non-power-of-two piece length for a v2 torrent")
	}
}

func TestParseV2RejectsAFileTreeRootThatIsItselfAFile(t *testing.T) {
	fileTree := map[string]any{
		"": map[string]any{"length": int64(100)},
	}
	info := map[string]any{
		"name":         "bad",
		"piece length": int64(16384),
		"meta version": int64(2),
		"file tree":    fileTree,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := ParseInfo(infoBytes); err == nil {
		t.Fatal("ParseInfo accepted a 'file tree' whose root is itself a file")
	}
}

// --- Hybrid ---

// buildHybridTorrentBytes hand-assembles a real, minimal hybrid (v1+v2)
// single-file torrent - the simplest real case, since a single file needs
// no BEP 47 padding to already be piece-aligned in both descriptions.
func buildHybridTorrentBytes(t *testing.T, pieceLength int64, content []byte) []byte {
	t.Helper()
	rootV2, layer, numPieces := hashFileV2(content, pieceLength)

	var v1Pieces []byte
	for off := 0; off < len(content); off += int(pieceLength) {
		end := off + int(pieceLength)
		if end > len(content) {
			end = len(content)
		}
		sum := sha1.Sum(content[off:end])
		v1Pieces = append(v1Pieces, sum[:]...)
	}

	fileTree := map[string]any{
		"hybrid.bin": map[string]any{
			"": map[string]any{
				"length":      int64(len(content)),
				"pieces root": string(rootV2[:]),
			},
		},
	}
	info := map[string]any{
		"name":         "hybrid.bin",
		"piece length": pieceLength,
		"meta version": int64(2),
		"file tree":    fileTree,
		"length":       int64(len(content)),
		"pieces":       string(v1Pieces),
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	top := map[string]any{"info": bencode.RawMessage(infoBytes)}
	if numPieces > 1 {
		top["piece layers"] = map[string]any{string(rootV2[:]): string(layer)}
	}
	data, err := bencode.Marshal(top)
	if err != nil {
		t.Fatalf("marshal top level: %v", err)
	}
	return data
}

func TestParseHybridSingleFileTorrent(t *testing.T) {
	const pieceLength = 32768
	content := genBytes(102400) // 4 pieces, same geometry as the v2 test above

	data := buildHybridTorrentBytes(t, pieceLength, content)
	mi, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if mi.MetaVersion != 2 {
		t.Fatalf("MetaVersion = %d, want 2", mi.MetaVersion)
	}
	if len(mi.PieceHashes) != 4 {
		t.Fatalf("PieceHashes has %d entries, want 4", len(mi.PieceHashes))
	}
	if len(mi.V2Files) != 1 {
		t.Fatalf("V2Files has %d entries, want 1", len(mi.V2Files))
	}
	if mi.TotalLength != int64(len(content)) {
		t.Errorf("TotalLength = %d, want %d", mi.TotalLength, len(content))
	}
	// Both v1 and v2 must agree on NumPieces (validateHybridConsistency
	// already checked this at parse time; confirm the accessor methods
	// actually reflect it too).
	if mi.NumPieces() != 4 {
		t.Fatalf("NumPieces() = %d, want 4", mi.NumPieces())
	}
}

func TestValidateHybridConsistencyRejectsPieceCountMismatch(t *testing.T) {
	mi := &MetaInfo{
		MetaVersion: 2,
		PieceHashes: make([]Hash, 3),
		Info:        InfoDict{Length: 300, PieceLength: 100},
		V2Files:     []V2FileInfo{{Path: []string{"a"}, Length: 300}},
		v2Pieces:    []v2PieceRun{{fileIndex: 0, startPiece: 0, numPieces: 4}}, // deliberately wrong
	}
	if err := mi.validateHybridConsistency(); err == nil {
		t.Fatal("validateHybridConsistency accepted a v1/v2 piece-count mismatch")
	}
}

func TestValidateHybridConsistencyRejectsContentLengthMismatch(t *testing.T) {
	mi := &MetaInfo{
		MetaVersion: 2,
		PieceHashes: make([]Hash, 3),
		Info:        InfoDict{Length: 300, PieceLength: 100},
		V2Files:     []V2FileInfo{{Path: []string{"a"}, Length: 999}}, // disagrees with Info.Length
		v2Pieces:    []v2PieceRun{{fileIndex: 0, startPiece: 0, numPieces: 3}},
	}
	if err := mi.validateHybridConsistency(); err == nil {
		t.Fatal("validateHybridConsistency accepted a v1/v2 content-length mismatch")
	}
}

func TestValidateHybridConsistencyAcceptsPaddingFilesExcludedFromV1Real(t *testing.T) {
	mi := &MetaInfo{
		MetaVersion: 2,
		PieceHashes: make([]Hash, 3),
		Info: InfoDict{
			PieceLength: 100,
			Files: []FileInfo{
				{Length: 250, Path: []string{"a"}},
				{Length: 50, Path: []string{".pad", "50"}, Attr: "p"},
			},
		},
		V2Files:  []V2FileInfo{{Path: []string{"a"}, Length: 250}},
		v2Pieces: []v2PieceRun{{fileIndex: 0, startPiece: 0, numPieces: 3}},
	}
	if err := mi.validateHybridConsistency(); err != nil {
		t.Fatalf("validateHybridConsistency rejected a real, correct hybrid torrent: %v", err)
	}
}

func TestHash256RoundTrip(t *testing.T) {
	var h Hash256
	for i := range h {
		h[i] = byte(i)
	}
	s := h.String()
	if len(s) != 64 {
		t.Fatalf("String() length = %d, want 64", len(s))
	}
	parsed, err := ParseHash256(s)
	if err != nil {
		t.Fatalf("ParseHash256: %v", err)
	}
	if parsed != h {
		t.Fatalf("round trip mismatch: got %s, want %s", parsed, h)
	}

	text, err := h.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	var h2 Hash256
	if err := h2.UnmarshalText(text); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	if h2 != h {
		t.Fatalf("MarshalText/UnmarshalText round trip mismatch")
	}
}

func TestHash256Truncated20(t *testing.T) {
	var h Hash256
	for i := range h {
		h[i] = byte(i + 1)
	}
	got := h.Truncated20()
	for i := 0; i < HashSize; i++ {
		if got[i] != h[i] {
			t.Fatalf("Truncated20()[%d] = %d, want %d", i, got[i], h[i])
		}
	}
}
