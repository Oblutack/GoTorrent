// Package picker decides which blocks to request next.
//
// The strategy is pluggable so it can be swapped and tested on its own, and
// availability is tracked incrementally: the previous implementation rescanned
// every piece against every connected peer twenty times a second while holding
// the session lock, which is O(pieces x peers) of pure overhead per tick.
package picker

import "github.com/Oblutack/GoTorrent/internal/bitfield"

// Availability counts how many connected peers hold each piece.
//
// It is maintained by events — a peer connecting, a peer disconnecting, a Have
// arriving — rather than recomputed, so a rarest-first decision costs a lookup
// instead of a scan.
type Availability struct {
	counts []int32
}

// NewAvailability returns an index over n pieces.
func NewAvailability(n int) *Availability {
	if n < 0 {
		n = 0
	}
	return &Availability{counts: make([]int32, n)}
}

// Len is the number of pieces covered.
func (a *Availability) Len() int { return len(a.counts) }

// AddPeer folds a newly connected peer's bitfield into the index.
func (a *Availability) AddPeer(have *bitfield.Bitfield) {
	if have == nil {
		return
	}
	have.Each(func(i int) bool {
		if i < len(a.counts) {
			a.counts[i]++
		}
		return true
	})
}

// RemovePeer takes a disconnected peer's bitfield back out.
func (a *Availability) RemovePeer(have *bitfield.Bitfield) {
	if have == nil {
		return
	}
	have.Each(func(i int) bool {
		if i < len(a.counts) && a.counts[i] > 0 {
			a.counts[i]--
		}
		return true
	})
}

// Reset clears every count, for when every peer has disconnected at once
// (a torrent pause) and decrementing each one individually would just waste
// cycles reaching the same all-zero result.
func (a *Availability) Reset() {
	for i := range a.counts {
		a.counts[i] = 0
	}
}

// Add records a single Have.
func (a *Availability) Add(index int) {
	if index >= 0 && index < len(a.counts) {
		a.counts[index]++
	}
}

// Count returns how many peers hold a piece.
func (a *Availability) Count(index int) int {
	if index < 0 || index >= len(a.counts) {
		return 0
	}
	return int(a.counts[index])
}

// MinCount returns the lowest copy count across every piece — the swarm's
// weakest link, in the sense that no piece is scarcer than this. Skips
// pieces with a zero count (unobtainable, same "not rare, unobtainable"
// distinction Rarest already draws) so a torrent with even one piece
// nobody has yet doesn't always report 0 regardless of how well-seeded
// everything else is. Returns 0 for an empty index or when every piece
// is unobtainable.
func (a *Availability) MinCount() int {
	min := int32(-1)
	for _, c := range a.counts {
		if c == 0 {
			continue
		}
		if min < 0 || c < min {
			min = c
		}
	}
	if min < 0 {
		return 0
	}
	return int(min)
}

// Rarest returns the index of the least available piece for which want
// reports true, or -1 if there is none. Pieces nobody has are skipped: they
// are not rare, they are unobtainable.
func (a *Availability) Rarest(want func(index int) bool) int {
	best, bestCount := -1, int32(0)
	for i, c := range a.counts {
		if c == 0 || !want(i) {
			continue
		}
		if best < 0 || c < bestCount {
			best, bestCount = i, c
		}
	}
	return best
}
