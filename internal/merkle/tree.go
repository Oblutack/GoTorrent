package merkle

import (
	"crypto/sha256"
	"fmt"
)

// BlockSize is BEP 52's fixed leaf block size: every merkle leaf is the
// SHA-256 of one 16KiB block of a file's raw bytes (the file's last block
// may be shorter).
const BlockSize = 16 * 1024

// DigestSize is the byte length of one Hash — callers slicing a
// concatenated "piece layers" byte string into individual Hash values use
// this rather than a bare 32, so the relationship to Hash's own size stays
// explicit at the call site.
const DigestSize = 32

// Hash is one node's value in the tree — a raw 32-byte SHA-256 digest.
type Hash [DigestSize]byte

// Leaf hashes one raw block of file content into a tree leaf.
func Leaf(block []byte) Hash {
	return Hash(sha256.Sum256(block))
}

// combine is one branching-2 step: a parent node's hash is SHA-256 of its
// two children's hashes concatenated, left then right.
func combine(l, r Hash) Hash {
	var buf [64]byte
	copy(buf[:32], l[:])
	copy(buf[32:], r[:])
	return Hash(sha256.Sum256(buf[:]))
}

// NextPow2 returns the smallest power of two >= n. NextPow2(0) and
// NextPow2(1) both return 1, since a tree of zero or one leaf needs no
// padding.
func NextPow2(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// PadLeaves returns leaves extended to exactly n entries, the new slots
// left at their zero value — a raw all-zero 32-byte hash, exactly the
// padding value BEP 52's own leaf-padding rule calls for ("the remaining
// leaf hashes... required to construct upper layers... are set to zero").
// n must be >= len(leaves); callers choose n via NextPow2 or a fixed
// per-piece leaf count.
func PadLeaves(leaves []Hash, n int) []Hash {
	if len(leaves) >= n {
		return leaves[:n]
	}
	padded := make([]Hash, n)
	copy(padded, leaves)
	return padded
}

// Root computes the branching-2 merkle root over leaves, which must already
// be a power-of-two length — every real caller in this package pads first
// (PadLeaves), so a non-power-of-two length here means a caller bug, not a
// data problem, and panics rather than silently combining a truncated,
// wrong tree.
func Root(leaves []Hash) Hash {
	if len(leaves) == 0 {
		panic("merkle: Root called with no leaves")
	}
	if len(leaves)&(len(leaves)-1) != 0 {
		panic(fmt.Sprintf("merkle: Root called with %d leaves, not a power of two", len(leaves)))
	}
	level := leaves
	for len(level) > 1 {
		next := make([]Hash, len(level)/2)
		for i := range next {
			next[i] = combine(level[2*i], level[2*i+1])
		}
		level = next
	}
	return level[0]
}

// PieceRoot hashes one piece's own leaf blocks into that piece's own root —
// the value stored, one per piece, in a .torrent's "piece layers" field.
// blocksPerPiece is pieceLength/BlockSize; soleFilePiece is true only when
// this is a file's one and only piece (the file is no larger than one
// piece).
//
// BEP 52's own reference implementation (bep_0052_torrent_creator.py)
// settled the padding target, which the prose alone doesn't fully specify:
// a short piece normally pads up to the full blocksPerPiece leaves (raw
// zero-hash padding — see PadLeaves), but a file's sole piece pads only to
// the next power of two above its own real leaf count instead, since there
// is no larger nominal per-piece leaf count to pad up to in that case.
func PieceRoot(leaves []Hash, blocksPerPiece int, soleFilePiece bool) Hash {
	target := blocksPerPiece
	if soleFilePiece {
		target = NextPow2(len(leaves))
	}
	return Root(PadLeaves(leaves, target))
}

// FileRoot combines a file's own per-piece roots (PieceRoot, in piece
// order) into the file's overall "pieces root". Short (padding) pieces
// beyond the real piece count are filled with the root hash of an entirely
// zero piece — not a raw zero hash, the detail this package's own doc
// comment flags as the one most likely to be silently wrong if guessed
// rather than cross-checked against the reference implementation.
//
// For a file with exactly one piece, this returns that piece's own root
// unchanged (no padding needed one layer up when there is no layer above
// a single node) — the same behavior BEP 52's own reference implementation
// has, since its root_hash on a single-element list is a no-op.
func FileRoot(pieceRoots []Hash, blocksPerPiece int) Hash {
	target := NextPow2(len(pieceRoots))
	if target == len(pieceRoots) {
		return Root(pieceRoots)
	}
	zeroPiece := PieceRoot(nil, blocksPerPiece, false)
	padded := make([]Hash, target)
	copy(padded, pieceRoots)
	for i := len(pieceRoots); i < target; i++ {
		padded[i] = zeroPiece
	}
	return Root(padded)
}
