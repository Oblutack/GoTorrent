package choker

import (
	"math/rand"
	"testing"
	"time"
)

type fakePeer struct {
	id         string
	interested bool
	choking    bool
	downloaded int64

	chokeCalls   int
	unchokeCalls int
}

func (p *fakePeer) ID() string             { return p.id }
func (p *fakePeer) Interested() bool       { return p.interested }
func (p *fakePeer) Choking() bool          { return p.choking }
func (p *fakePeer) BytesDownloaded() int64 { return p.downloaded }

func (p *fakePeer) Choke() error {
	p.chokeCalls++
	p.choking = true
	return nil
}

func (p *fakePeer) Unchoke() error {
	p.unchokeCalls++
	p.choking = false
	return nil
}

func newFakePeer(id string, interested bool) *fakePeer {
	return &fakePeer{id: id, interested: interested, choking: true}
}

func TestUninterestedPeersStayChoked(t *testing.T) {
	c := New()
	p := newFakePeer("a", false)
	c.Run([]Peer{p}, time.Now())
	if !p.choking || p.unchokeCalls != 0 {
		t.Fatalf("an uninterested peer was unchoked: choking=%v calls=%d", p.choking, p.unchokeCalls)
	}
}

func TestTopRatesGetUnchoked(t *testing.T) {
	c := New(WithSlots(3))
	peers := []*fakePeer{
		newFakePeer("fast", true),
		newFakePeer("medium", true),
		newFakePeer("slow", true),
		newFakePeer("idle", true),
	}
	// Two rounds: the first establishes a baseline, the second measures rate.
	now := time.Now()
	asPeers := func() []Peer {
		out := make([]Peer, len(peers))
		for i, p := range peers {
			out[i] = p
		}
		return out
	}
	c.Run(asPeers(), now)

	peers[0].downloaded += 1_000_000 // fast
	peers[1].downloaded += 500_000   // medium
	peers[2].downloaded += 100_000   // slow
	// idle gets nothing

	now = now.Add(10 * time.Second)
	c.Run(asPeers(), now)

	if peers[0].choking {
		t.Error("the fastest peer is still choked")
	}
	if peers[1].choking {
		t.Error("the second-fastest peer is still choked")
	}
	// With 3 slots, one reserved for the optimistic pick, only the top 2 by
	// rate are guaranteed a regular slot. "slow" and "idle" are the two
	// candidates left for that one optimistic slot.
	unchokedCount := 0
	for _, p := range peers {
		if !p.choking {
			unchokedCount++
		}
	}
	if unchokedCount != 3 {
		t.Fatalf("%d peers unchoked, want exactly 3 (slots=3)", unchokedCount)
	}
}

func TestOptimisticSlotGivesEveryoneAChance(t *testing.T) {
	c := New(WithSlots(1), WithRand(rand.New(rand.NewSource(1))))
	peers := []Peer{newFakePeer("only", true)}

	c.Run(peers, time.Now())
	if peers[0].(*fakePeer).choking {
		t.Fatal("the only interested peer was not given the optimistic slot")
	}
}

func TestOptimisticSlotRotates(t *testing.T) {
	// Zero regular slots (WithSlots(1) reserves the single slot for
	// optimistic), several equally-idle peers, so which one is unchoked is
	// purely the optimistic pick.
	c := New(WithSlots(1), WithRotation(10*time.Second), WithRand(rand.New(rand.NewSource(1))))
	peers := make([]Peer, 5)
	for i := range peers {
		peers[i] = newFakePeer(string(rune('a'+i)), true)
	}

	now := time.Now()
	c.Run(peers, now)
	first := currentlyUnchoked(peers)
	if len(first) != 1 {
		t.Fatalf("expected exactly one unchoked peer, got %v", first)
	}

	// Before rotation is due, the same peer must stay unchoked even though
	// nothing distinguishes the candidates on rate.
	now = now.Add(2 * time.Second)
	c.Run(peers, now)
	if got := currentlyUnchoked(peers); len(got) != 1 || got[0] != first[0] {
		t.Fatalf("optimistic pick changed before rotation was due: %v -> %v", first, got)
	}

	// After the rotation interval, it is allowed to move (not required to,
	// since random choice could repeat, but across many rotations it must
	// visit more than one peer).
	seen := map[string]bool{first[0]: true}
	for i := 0; i < 50; i++ {
		now = now.Add(11 * time.Second)
		c.Run(peers, now)
		for _, id := range currentlyUnchoked(peers) {
			seen[id] = true
		}
	}
	if len(seen) < 2 {
		t.Fatalf("optimistic slot never rotated across 50 opportunities: %v", seen)
	}
}

func currentlyUnchoked(peers []Peer) []string {
	var out []string
	for _, p := range peers {
		if !p.(*fakePeer).choking {
			out = append(out, p.ID())
		}
	}
	return out
}

func TestChokingIsIdempotent(t *testing.T) {
	c := New(WithSlots(4))
	p := newFakePeer("a", true)
	c.Run([]Peer{p}, time.Now())
	if p.unchokeCalls != 1 {
		t.Fatalf("Unchoke called %d times on first Run, want 1", p.unchokeCalls)
	}
	// A peer that stays in the same slot must not be re-unchoked every tick.
	c.Run([]Peer{p}, time.Now().Add(time.Second))
	if p.unchokeCalls != 1 {
		t.Fatalf("Unchoke called %d times total, want 1 (idempotent)", p.unchokeCalls)
	}
}

func TestDroppedPeerStopsBeingChoked(t *testing.T) {
	c := New(WithSlots(4))
	a := newFakePeer("a", true)
	b := newFakePeer("b", true)
	c.Run([]Peer{a, b}, time.Now())
	if a.choking || b.choking {
		t.Fatal("with slots to spare, both interested peers should be unchoked")
	}

	// b disconnects; a subsequent Run must not panic or misbehave when handed
	// a peer list that no longer includes it.
	c.Run([]Peer{a}, time.Now().Add(time.Second))
	if a.choking {
		t.Fatal("the remaining peer was choked after another peer dropped")
	}
}

func TestNegativeRateIsClamped(t *testing.T) {
	// A peer that reconnects gets a fresh Client whose BytesDownloaded starts
	// at 0, which would look like a negative rate against the choker's
	// memory of the old connection's cumulative total.
	c := New(WithSlots(4))
	p := newFakePeer("a", true)
	p.downloaded = 1_000_000
	c.Run([]Peer{p}, time.Now())

	p.downloaded = 0 // simulate a reconnect
	// Must not panic and must still treat the peer as interested/eligible.
	c.Run([]Peer{p}, time.Now().Add(time.Second))
	if p.choking {
		t.Fatal("a reconnected peer with a reset counter was left choked")
	}
}

func TestReap(t *testing.T) {
	c := New()
	c.Run([]Peer{newFakePeer("a", true), newFakePeer("b", true)}, time.Now())
	if len(c.lastBytes) != 2 {
		t.Fatalf("lastBytes has %d entries, want 2", len(c.lastBytes))
	}
	c.Reap(func(id string) bool { return id == "a" })
	if len(c.lastBytes) != 1 {
		t.Fatalf("lastBytes has %d entries after Reap, want 1", len(c.lastBytes))
	}
	if _, ok := c.lastBytes["b"]; ok {
		t.Fatal("Reap did not remove the disconnected peer")
	}
	if _, ok := c.lastBytes["a"]; !ok {
		t.Fatal("Reap removed a peer that is still connected")
	}
}

func TestEmptyPeerList(t *testing.T) {
	c := New()
	c.Run(nil, time.Now()) // must not panic
}
