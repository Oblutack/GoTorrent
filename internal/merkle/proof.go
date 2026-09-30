package merkle

// Proof returns the sibling hash needed at every tree level, from the
// leaf's own level up to (but not including) the root, to prove that
// leaves[index] is part of Root(leaves). len(leaves) must be a power of
// two (pad first via PadLeaves).
//
// This is the standard single-leaf inclusion proof, not BEP 52's own wire
// format for the 'hash request'/'hashes' messages — those exchange a
// *range* of same-layer hashes at once and omit whatever ancestor layers
// the requester can already compute unaided from that range, a real but
// separate wire-efficiency detail internal/peer/internal/torrent handle
// when they build on this primitive.
func Proof(leaves []Hash, index int) []Hash {
	if len(leaves)&(len(leaves)-1) != 0 {
		panic("merkle: Proof called with a non-power-of-two leaf count")
	}
	if index < 0 || index >= len(leaves) {
		panic("merkle: Proof index out of range")
	}

	proof := make([]Hash, 0, bitLen(len(leaves))-1)
	level := leaves
	idx := index
	for len(level) > 1 {
		proof = append(proof, level[idx^1])
		next := make([]Hash, len(level)/2)
		for i := range next {
			next[i] = combine(level[2*i], level[2*i+1])
		}
		level = next
		idx /= 2
	}
	return proof
}

// VerifyProof reports whether leaf, claimed to sit at index among
// totalLeaves (a power of two), combines up through proof (as returned by
// Proof) to root.
func VerifyProof(root Hash, leaf Hash, index, totalLeaves int, proof []Hash) bool {
	if totalLeaves&(totalLeaves-1) != 0 || index < 0 || index >= totalLeaves {
		return false
	}
	if len(proof) != bitLen(totalLeaves)-1 {
		return false
	}

	h := leaf
	idx := index
	for _, sib := range proof {
		if idx%2 == 0 {
			h = combine(h, sib)
		} else {
			h = combine(sib, h)
		}
		idx /= 2
	}
	return h == root
}

// bitLen returns floor(log2(n))+1 for n a power of two >= 1 — the number
// of levels (leaves included) in a tree of n leaves.
func bitLen(n int) int {
	l := 0
	for n > 1 {
		n >>= 1
		l++
	}
	return l + 1
}
