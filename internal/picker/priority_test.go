package picker

import (
	"testing"
	"time"
)

func TestPriorityTextRoundTrip(t *testing.T) {
	for _, p := range []Priority{PrioritySkip, PriorityLow, PriorityNormal, PriorityHigh} {
		text, err := p.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText(%v): %v", p, err)
		}
		var back Priority
		if err := back.UnmarshalText(text); err != nil {
			t.Fatalf("UnmarshalText(%q): %v", text, err)
		}
		if back != p {
			t.Fatalf("round-tripped %v through %q, got %v", p, text, back)
		}
	}
}

func TestPriorityUnmarshalTextRejectsUnknown(t *testing.T) {
	var p Priority
	if err := p.UnmarshalText([]byte("urgent")); err == nil {
		t.Fatal("UnmarshalText accepted an unknown priority name, want an error")
	}
}

func TestSetPrioritiesRejectsWrongLength(t *testing.T) {
	p := newTestPicker(t, 4, nil)
	if err := p.SetPriorities([]Priority{PriorityNormal, PriorityNormal}); err == nil {
		t.Fatal("SetPriorities accepted a slice shorter than NumPieces, want an error")
	}
}

func TestSkipPriorityPiecesAreNeverPicked(t *testing.T) {
	p := newTestPicker(t, 4, nil)
	seedAvailability(p, 4)
	if err := p.SetPriorities([]Priority{PriorityNormal, PrioritySkip, PriorityNormal, PrioritySkip}); err != nil {
		t.Fatalf("SetPriorities: %v", err)
	}

	now := time.Now()
	seen := make(map[int]bool)
	for {
		reqs := p.Pick(everything, 4, now)
		if len(reqs) == 0 {
			break
		}
		for _, r := range reqs {
			seen[r.Index] = true
			p.Received(r.Index, r.Begin, r.Length)
		}
		for i := range reqs {
			p.MarkVerified(reqs[i].Index)
		}
	}

	if seen[1] || seen[3] {
		t.Fatalf("a skip-priority piece was picked: %v", seen)
	}
	if !seen[0] || !seen[2] {
		t.Fatalf("a normal-priority piece was never picked: %v", seen)
	}
}

func TestHighPriorityPiecesArePickedBeforeNormal(t *testing.T) {
	p := newTestPicker(t, 4, func(c *Config) { c.MaxActivePieces = 1 })
	seedAvailability(p, 4)
	if err := p.SetPriorities([]Priority{PriorityNormal, PriorityHigh, PriorityNormal, PriorityNormal}); err != nil {
		t.Fatalf("SetPriorities: %v", err)
	}

	reqs := p.Pick(everything, 4, time.Now())
	if len(reqs) == 0 || reqs[0].Index != 1 {
		t.Fatalf("got %v, want piece 1 (the only High-priority one) picked first", reqs)
	}
}

func TestLowPriorityPiecesArePickedAfterNormal(t *testing.T) {
	p := newTestPicker(t, 4, func(c *Config) { c.MaxActivePieces = 1 })
	seedAvailability(p, 4)
	if err := p.SetPriorities([]Priority{PriorityLow, PriorityLow, PriorityNormal, PriorityLow}); err != nil {
		t.Fatalf("SetPriorities: %v", err)
	}

	reqs := p.Pick(everything, 4, time.Now())
	if len(reqs) == 0 || reqs[0].Index != 2 {
		t.Fatalf("got %v, want piece 2 (the only Normal-priority one) picked first", reqs)
	}
}

func TestRemainingAndCompleteIgnoreSkippedPieces(t *testing.T) {
	p := newTestPicker(t, 4, nil)
	seedAvailability(p, 4)
	if err := p.SetPriorities([]Priority{PriorityNormal, PrioritySkip, PriorityNormal, PrioritySkip}); err != nil {
		t.Fatalf("SetPriorities: %v", err)
	}

	if got := p.Remaining(); got != 2 {
		t.Fatalf("Remaining() = %d, want 2 (skip pieces excluded)", got)
	}
	if p.Complete() {
		t.Fatal("Complete() = true before the two wanted pieces are verified")
	}

	now := time.Now()
	for _, index := range []int{0, 2} {
		reqs := p.Pick(func(i int) bool { return i == index }, 4, now)
		for _, r := range reqs {
			p.Received(r.Index, r.Begin, r.Length)
		}
		p.MarkVerified(index)
	}

	if got := p.Remaining(); got != 0 {
		t.Fatalf("Remaining() = %d after both wanted pieces verified, want 0", got)
	}
	if !p.Complete() {
		t.Fatal("Complete() = false after every non-skip piece is verified")
	}
}

func TestSetPrioritiesDoesNotEvictAnAlreadyActivePiece(t *testing.T) {
	p := newTestPicker(t, 2, nil)
	seedAvailability(p, 2)
	now := time.Now()

	// Start piece 0 and receive one of its blocks before it's ever marked
	// skip.
	reqs := p.Pick(func(i int) bool { return i == 0 }, 1, now)
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	p.Received(reqs[0].Index, reqs[0].Begin, reqs[0].Length)

	if err := p.SetPriorities([]Priority{PrioritySkip, PriorityNormal}); err != nil {
		t.Fatalf("SetPriorities: %v", err)
	}

	if p.ActiveCount() != 1 {
		t.Fatalf("ActiveCount() = %d after skipping an in-progress piece, want it to stay active (not evicted)", p.ActiveCount())
	}
}

func TestSequentialStrategyRespectsSkipPriority(t *testing.T) {
	p := newTestPicker(t, 4, func(c *Config) { c.Strategy = Sequential; c.MaxActivePieces = 1 })
	seedAvailability(p, 4)
	if err := p.SetPriorities([]Priority{PriorityNormal, PrioritySkip, PriorityNormal, PriorityNormal}); err != nil {
		t.Fatalf("SetPriorities: %v", err)
	}

	now := time.Now()
	reqs := p.Pick(everything, 4, now)
	if len(reqs) == 0 || reqs[0].Index != 0 {
		t.Fatalf("got %v, want piece 0 first", reqs)
	}
	for _, r := range reqs {
		p.Received(r.Index, r.Begin, r.Length)
	}
	p.MarkVerified(0)

	reqs = p.Pick(everything, 4, now)
	if len(reqs) == 0 || reqs[0].Index != 2 {
		t.Fatalf("got %v, want piece 2 next (piece 1 is skip-priority)", reqs)
	}
}
