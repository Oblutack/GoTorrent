package merkle

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// genBytes reproduces the exact same deterministic byte stream a Python
// driver script (using BEP 52's own reference implementation,
// bep_0052_torrent_creator.py, fetched from bittorrent.org) generated to
// build this test's fixtures — a Knuth multiplicative hash of the byte
// index, taking the top byte, which (unlike a plain LCG's low bits, tried
// first and rejected once it produced visibly periodic output) has no
// short-period artifact across the piece sizes these fixtures use.
func genBytes(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		x := uint32(i) * 2654435761
		out[i] = byte(x >> 24)
	}
	return out
}

func mustHash(t *testing.T, hexStr string) Hash {
	t.Helper()
	b, err := hex.DecodeString(hexStr)
	if err != nil || len(b) != 32 {
		t.Fatalf("bad fixture hex %q: %v", hexStr, err)
	}
	var h Hash
	copy(h[:], b)
	return h
}

// pieceLeaves splits piece bytes into BlockSize leaves and hashes each.
func pieceLeaves(piece []byte) []Hash {
	var leaves []Hash
	for off := 0; off < len(piece); off += BlockSize {
		end := off + BlockSize
		if end > len(piece) {
			end = len(piece)
		}
		leaves = append(leaves, Leaf(piece[off:end]))
	}
	return leaves
}

// TestLeafMatchesPlainSHA256 confirms Leaf really is just SHA-256 of the
// block, independently of any tree logic.
func TestLeafMatchesPlainSHA256(t *testing.T) {
	block := genBytes(BlockSize)
	got := Leaf(block)
	want := sha256.Sum256(block)
	if got != Hash(want) {
		t.Fatalf("Leaf = %x, want %x", got, want)
	}
}

// TestCombineMatchesPlainSHA256 confirms combine really is SHA-256 of the
// two children concatenated, computed independently of the production
// combine function.
func TestCombineMatchesPlainSHA256(t *testing.T) {
	l := Leaf([]byte("left"))
	r := Leaf([]byte("right"))
	got := combine(l, r)
	want := sha256.Sum256(append(append([]byte{}, l[:]...), r[:]...))
	if got != Hash(want) {
		t.Fatalf("combine = %x, want %x", got, want)
	}
}

func TestNextPow2(t *testing.T) {
	cases := map[int]int{0: 1, 1: 1, 2: 2, 3: 4, 4: 4, 5: 8, 8: 8, 9: 16, 512: 512, 513: 1024}
	for n, want := range cases {
		if got := NextPow2(n); got != want {
			t.Errorf("NextPow2(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestPadLeavesFillsWithRawZeroHash(t *testing.T) {
	leaves := []Hash{Leaf([]byte("a")), Leaf([]byte("b"))}
	padded := PadLeaves(leaves, 4)
	if len(padded) != 4 {
		t.Fatalf("len(padded) = %d, want 4", len(padded))
	}
	if padded[0] != leaves[0] || padded[1] != leaves[1] {
		t.Fatal("PadLeaves must not alter the original leaves")
	}
	var zero Hash
	if padded[2] != zero || padded[3] != zero {
		t.Fatalf("padding slots = %x, %x, want raw all-zero hashes", padded[2], padded[3])
	}
}

func TestRootPanicsOnNonPowerOfTwo(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Root did not panic on a non-power-of-two leaf count")
		}
	}()
	Root([]Hash{{}, {}, {}})
}

// --- Real cross-checks against BEP 52's own reference implementation ---
//
// Every expected hash below was produced by running
// bep_0052_torrent_creator.py's FileHasher (fetched from
// bittorrent.org/beps/bep_0052_torrent_creator.py) against genBytes'
// output for the exact byte lengths and piece lengths named — a real,
// independent second implementation's output, the same discipline this
// project's other from-scratch protocol packages already follow (BEP 40/42
// against their own published test vectors, MSE against a second
// implementation's source).

// TestFixtureA_100KiB_LastPiecePartial: 102400 bytes, piece length 32768
// (blocksPerPiece=2). Pieces 0-2 are two full 16KiB leaves each (no
// padding); piece 3 has one real 4096-byte leaf and pads to the full
// blocksPerPiece via the non-sole-piece rule (padding to blocksPerPiece,
// not just NextPow2 of the real leaf count). The piece count (4) is
// already a power of two, so FileRoot needs no padding of its own.
func TestFixtureA_100KiB_LastPiecePartial(t *testing.T) {
	const pieceLength = 32768
	const blocksPerPiece = pieceLength / BlockSize
	data := genBytes(102400)

	want := []Hash{
		mustHash(t, "84935094e4d5f78de913e7f353fe7327277ddfc313929c51cdeddeacb44f8f96"),
		mustHash(t, "93cda584539d1a2210e8ec3ddeeba70a313f883ae09fcb0be80686aa9f452c13"),
		mustHash(t, "e51f34de77a14712543f9455b47df4f101b8c8df6095bfff8921e22eafbed189"),
		mustHash(t, "f2bf4494f15ffdeb5d9aca719dc4bbee390df301b8dae281d1556c134b70de15"),
	}
	wantRoot := mustHash(t, "fe6f0ca8028092053b4c21a550d85c8727b274c805f423a741638daa72e5326b")

	var pieceRoots []Hash
	for off := 0; off < len(data); off += pieceLength {
		end := off + pieceLength
		if end > len(data) {
			end = len(data)
		}
		root := PieceRoot(pieceLeaves(data[off:end]), blocksPerPiece, false)
		pieceRoots = append(pieceRoots, root)
	}
	if len(pieceRoots) != len(want) {
		t.Fatalf("got %d pieces, want %d", len(pieceRoots), len(want))
	}
	for i, w := range want {
		if pieceRoots[i] != w {
			t.Errorf("piece %d = %x, want %x", i, pieceRoots[i], w)
		}
	}
	if got := FileRoot(pieceRoots, blocksPerPiece); got != wantRoot {
		t.Errorf("FileRoot = %x, want %x", got, wantRoot)
	}
}

// TestFixtureB_SolePiece: 40000 bytes, piece length 131072 (blocksPerPiece
// = 8) — a file smaller than one piece, so it has exactly one piece with
// 3 real leaves (16384, 16384, 7232 bytes). Padded via the sole-file-piece
// rule to NextPow2(3)=4 leaves, not the full blocksPerPiece=8 — the one
// rule this fixture exists specifically to distinguish from fixture A's
// (non-sole) padding. The file's own root equals that single piece's own
// root directly (no layer above it).
func TestFixtureB_SolePiece(t *testing.T) {
	const pieceLength = 131072
	const blocksPerPiece = pieceLength / BlockSize
	data := genBytes(40000)

	want := mustHash(t, "4a81d3b69cf757771805b618dd65bbd7f5bfbc6d9d67599d8390bdd9da7991be")

	leaves := pieceLeaves(data)
	if len(leaves) != 3 {
		t.Fatalf("fixture setup: got %d real leaves, want 3", len(leaves))
	}
	pieceRoot := PieceRoot(leaves, blocksPerPiece, true)
	if pieceRoot != want {
		t.Errorf("sole-piece PieceRoot = %x, want %x", pieceRoot, want)
	}
	if got := FileRoot([]Hash{pieceRoot}, blocksPerPiece); got != want {
		t.Errorf("FileRoot of a single piece = %x, want %x (should pass through unchanged)", got, want)
	}
}

// TestFixtureC_ThreePiecesSingleLeafEach: 49152 bytes, piece length 16384
// (blocksPerPiece=1) — every piece is exactly one full leaf, so each
// piece's own root reduces to that leaf's own hash directly (no combine
// happens for a 1-leaf tree). This cross-checks Leaf() and PieceRoot()'s
// trivial-passthrough case against real content, and FileRoot's padding
// of a non-power-of-two (3) piece count up to 4 — though with
// blocksPerPiece=1 the padding filler itself degenerates to a raw zero
// hash (see fixture D for the case where it doesn't).
func TestFixtureC_ThreePiecesSingleLeafEach(t *testing.T) {
	const pieceLength = 16384
	const blocksPerPiece = pieceLength / BlockSize
	data := genBytes(49152)

	want := []Hash{
		mustHash(t, "8d5a927da22402130e8b3197f1be29eba10ca80071426f10eed00cb5fa4c4cbb"),
		mustHash(t, "3ea51041662ba8a4de4b1e35e52aa5ff43ff13786f14a011b71824a78dea3bf6"),
		mustHash(t, "fc05a9259ea59316aab8f4ca181b4edfdc4776bc825fc528cc90211fed2e2a9a"),
	}
	wantRoot := mustHash(t, "e54121e7f402b163dccab57623791ad1a37e8b8ca3a80931d9b870267d7eb193")

	var pieceRoots []Hash
	for off := 0; off < len(data); off += pieceLength {
		leaves := pieceLeaves(data[off : off+pieceLength])
		if len(leaves) != 1 {
			t.Fatalf("fixture setup: piece at %d has %d leaves, want 1", off, len(leaves))
		}
		root := PieceRoot(leaves, blocksPerPiece, false)
		if root != leaves[0] {
			t.Errorf("single-leaf PieceRoot must equal the leaf itself: got %x, leaf %x", root, leaves[0])
		}
		pieceRoots = append(pieceRoots, root)
	}
	for i, w := range want {
		if pieceRoots[i] != w {
			t.Errorf("piece %d = %x, want %x", i, pieceRoots[i], w)
		}
	}
	if got := FileRoot(pieceRoots, blocksPerPiece); got != wantRoot {
		t.Errorf("FileRoot = %x, want %x", got, wantRoot)
	}
}

// TestFixtureD_ThreeFullPiecesTwoLeavesEach is the fixture that actually
// distinguishes FileRoot's padding filler (the root hash of an entirely
// zero piece) from a raw zero hash: 98304 bytes, piece length 32768
// (blocksPerPiece=2), so every piece is a real 2-leaf combine and the
// zero-piece-root filler used to pad the 3-piece count up to 4 is itself
// a real SHA-256 combine of two zero leaves, not [32]byte{}.
func TestFixtureD_ThreeFullPiecesTwoLeavesEach(t *testing.T) {
	const pieceLength = 32768
	const blocksPerPiece = pieceLength / BlockSize
	data := genBytes(98304)

	want := []Hash{
		mustHash(t, "84935094e4d5f78de913e7f353fe7327277ddfc313929c51cdeddeacb44f8f96"),
		mustHash(t, "93cda584539d1a2210e8ec3ddeeba70a313f883ae09fcb0be80686aa9f452c13"),
		mustHash(t, "e51f34de77a14712543f9455b47df4f101b8c8df6095bfff8921e22eafbed189"),
	}
	wantRoot := mustHash(t, "05a8cef55194e8139f97e74c0389867a9818e7572e57a607187a3a3a13e38a21")
	wantZeroPieceRoot := mustHash(t, "f5a5fd42d16a20302798ef6ed309979b43003d2320d9f0e8ea9831a92759fb4b")

	if got := PieceRoot(nil, blocksPerPiece, false); got != wantZeroPieceRoot {
		t.Fatalf("zero-piece root = %x, want %x (a real 2-leaf combine, not a raw zero hash)", got, wantZeroPieceRoot)
	}
	if (wantZeroPieceRoot == Hash{}) {
		t.Fatal("fixture bug: the pinned zero-piece root equals the raw zero hash, so this test would not distinguish the two padding rules")
	}

	var pieceRoots []Hash
	for off := 0; off < len(data); off += pieceLength {
		root := PieceRoot(pieceLeaves(data[off:off+pieceLength]), blocksPerPiece, false)
		pieceRoots = append(pieceRoots, root)
	}
	for i, w := range want {
		if pieceRoots[i] != w {
			t.Errorf("piece %d = %x, want %x", i, pieceRoots[i], w)
		}
	}
	if got := FileRoot(pieceRoots, blocksPerPiece); got != wantRoot {
		t.Errorf("FileRoot = %x, want %x", got, wantRoot)
	}
}
