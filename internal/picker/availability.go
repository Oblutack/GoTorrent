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
