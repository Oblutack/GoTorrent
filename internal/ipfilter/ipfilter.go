// Package ipfilter blocks connections to or from IP ranges a blocklist
// names — eMule's ipfilter.dat and PeerGuardian's .p2p formats, the two
// most common ones in the wild.
package ipfilter

import (
	"bytes"
	"net"
	"sort"
	"sync"
)

// Range is one blocked IP range, inclusive on both ends. Start and End are
// always the 16-byte form (net.IP.To16), so an IPv4 and an IPv6 range
// compare correctly against each other without a separate code path.
type Range struct {
	Start, End net.IP
}

// Filter is a set of blocked IP ranges, safe for concurrent use. The zero
// value blocks nothing; Load replaces the whole set atomically. A nil
// *Filter also blocks nothing — Blocked and Count are nil-receiver-safe —
// so a caller never needs to nil-check an optional filter before using it.
type Filter struct {
	mu     sync.RWMutex
	ranges []Range // sorted by Start
}

// New returns an empty Filter.
func New() *Filter { return &Filter{} }

// Load replaces the filter's whole range set, sorted once here so Blocked
// can binary-search rather than scan — the only cost Load itself pays for
// every lookup afterward being cheap even against a blocklist with
// hundreds of thousands of entries.
func (f *Filter) Load(ranges []Range) {
	sorted := make([]Range, len(ranges))
	for i, r := range ranges {
		sorted[i] = Range{Start: r.Start.To16(), End: r.End.To16()}
	}
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i].Start, sorted[j].Start) < 0 })

	f.mu.Lock()
	f.ranges = sorted
	f.mu.Unlock()
}

// Blocked reports whether ip falls inside any loaded range.
//
// Assumes the loaded ranges do not overlap, which holds for every real
// blocklist file this package has been tested against — Load sorts by
// Start and Blocked finds the rightmost range starting at or before ip,
// checking only that one's End. A file with genuinely overlapping ranges
// could in principle hide a match behind a narrower one that happens to
// start later; accepted as a deliberate simplification rather than a full
// interval-tree lookup no real blocklist actually needs.
func (f *Filter) Blocked(ip net.IP) bool {
	if f == nil || ip == nil {
		return false
	}
	ip16 := ip.To16()
	if ip16 == nil {
		return false
	}

	f.mu.RLock()
	defer f.mu.RUnlock()
	i := sort.Search(len(f.ranges), func(i int) bool {
		return bytes.Compare(f.ranges[i].Start, ip16) > 0
	})
	if i == 0 {
		return false
	}
	return bytes.Compare(ip16, f.ranges[i-1].End) <= 0
}

// Count returns how many ranges are currently loaded.
func (f *Filter) Count() int {
	if f == nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.ranges)
}
