// Package choker implements BitTorrent's tit-for-tat unchoking algorithm.
//
// The idea is simple and the old code never did it: unchoke the peers that
// are giving you the most data, plus one optimistic slot chosen at random so
// new and slow peers still get a chance to prove themselves. Reciprocity is
// what makes a swarm self-sustaining instead of every leecher choking every
// other leecher.
package choker

import (
	"math/rand"
	"sort"
	"time"
)

// DefaultSlots is how many peers are unchoked for measured performance,
// including the optimistic one. Real clients use 4; more doesn't help once
// upload bandwidth is the bottleneck.
const DefaultSlots = 4

// DefaultRotation is how often the optimistic slot moves to a new peer.
const DefaultRotation = 30 * time.Second

// Peer is what the choker needs from a connection. Implementations must be
// safe to call from the choker's goroutine; peer.Client's accessors already
// are.
type Peer interface {
	// ID distinguishes peers from each other. The remote address is a good
	// choice since, unlike the handshake peer ID, it isn't attacker-chosen.
	ID() string
	Interested() bool
	Choking() bool
	Choke() error
	Unchoke() error
	// BytesDownloaded is the cumulative bytes received from this peer. The
	// choker measures the delta between calls to Run, so it does not need to
	// be reset.
	BytesDownloaded() int64
}

// Choker decides which peers to unchoke.
//
// It is not safe for concurrent use — like Picker, it is meant to be owned by
// a single goroutine (the torrent actor) and driven by a ticker.
type Choker struct {
	slots        int
	rotation     time.Duration
	rng          *rand.Rand
	lastBytes    map[string]int64
	lastRotation time.Time
	optimistic   string
}

// Option configures a Choker.
type Option func(*Choker)

// WithSlots overrides DefaultSlots.
func WithSlots(n int) Option {
	return func(c *Choker) { c.slots = n }
}

// WithRotation overrides DefaultRotation.
func WithRotation(d time.Duration) Option {
	return func(c *Choker) { c.rotation = d }
}

// WithRand overrides the source used to pick the optimistic peer. Tests use
// this for a deterministic choice.
func WithRand(r *rand.Rand) Option {
	return func(c *Choker) { c.rng = r }
}

// New returns a Choker.
func New(opts ...Option) *Choker {
	c := &Choker{
		slots:     DefaultSlots,
		rotation:  DefaultRotation,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
		lastBytes: make(map[string]int64),
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.slots < 1 {
		c.slots = 1
	}
	return c
}

type rankedPeer struct {
	peer Peer
	rate int64
}

// Run re-evaluates who should be unchoked and sends the necessary Choke/
// Unchoke messages. Peers not in peers are forgotten from the rate history on
// the next call that omits them for good — Go maps don't need explicit
// pruning here since stale entries cost only a few bytes each and Reap
// handles the rare long-lived-swarm case.
func (c *Choker) Run(peers []Peer, now time.Time) {
	ranked := make([]rankedPeer, 0, len(peers))
	for _, p := range peers {
		if !p.Interested() {
			// Not interested means choking them costs nothing and unchoking
			// them helps nobody; leave them choked and out of the ranking.
			c.lastBytes[p.ID()] = p.BytesDownloaded()
			if !p.Choking() {
				p.Choke()
			}
			continue
		}
		current := p.BytesDownloaded()
		rate := current - c.lastBytes[p.ID()]
		if rate < 0 {
			rate = 0 // a counter reset (reconnect) should not look negative
		}
		c.lastBytes[p.ID()] = current
		ranked = append(ranked, rankedPeer{peer: p, rate: rate})
	}

	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].rate > ranked[j].rate })

	regularSlots := c.slots
	if len(ranked) > 0 {
		regularSlots-- // reserve one slot for the optimistic pick
	}
	if regularSlots < 0 {
		regularSlots = 0
	}

	unchoke := make(map[string]bool, c.slots)
	for i := 0; i < regularSlots && i < len(ranked); i++ {
		unchoke[ranked[i].peer.ID()] = true
	}

	c.pickOptimistic(ranked, unchoke, now)

	for _, rp := range ranked {
		want := unchoke[rp.peer.ID()]
		switch {
		case want && rp.peer.Choking():
			rp.peer.Unchoke()
		case !want && !rp.peer.Choking():
			rp.peer.Choke()
		}
	}
}

// pickOptimistic adds one more peer to unchoke, rotating on a timer rather
// than every call so a newly unchoked peer has time to start reciprocating
// before being judged.
func (c *Choker) pickOptimistic(ranked []rankedPeer, unchoke map[string]bool, now time.Time) {
	if len(ranked) == 0 {
		c.optimistic = ""
		return
	}

	stillEligible := c.optimistic != "" && !unchoke[c.optimistic] && containsID(ranked, c.optimistic)
	due := c.lastRotation.IsZero() || now.Sub(c.lastRotation) >= c.rotation

	if !stillEligible || due {
		candidates := make([]rankedPeer, 0, len(ranked))
		for _, rp := range ranked {
			if !unchoke[rp.peer.ID()] {
				candidates = append(candidates, rp)
			}
		}
		if len(candidates) == 0 {
			// Everyone interested already has a regular slot; there is
			// nothing left to be optimistic about this round.
			c.optimistic = ""
			c.lastRotation = now
			return
		}
		c.optimistic = candidates[c.rng.Intn(len(candidates))].peer.ID()
		c.lastRotation = now
	}

	if c.optimistic != "" {
		unchoke[c.optimistic] = true
	}
}

func containsID(ranked []rankedPeer, id string) bool {
	for _, rp := range ranked {
		if rp.peer.ID() == id {
			return true
		}
	}
	return false
}

// Reap drops rate history for peers that are no longer connected, so a churny
// swarm does not leak memory into lastBytes forever.
func (c *Choker) Reap(stillConnected func(id string) bool) {
	for id := range c.lastBytes {
		if !stillConnected(id) {
			delete(c.lastBytes, id)
		}
	}
}
