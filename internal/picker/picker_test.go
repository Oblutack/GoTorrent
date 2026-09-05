package picker

import (
	"math/rand"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bitfield"
)

const testPieceLength = 4 * BlockLength // 4 blocks per piece

func newTestPicker(t *testing.T, numPieces int, tweak func(*Config)) *Picker {
	t.Helper()
	cfg := Config{
		NumPieces:   numPieces,
		PieceLength: func(int) int64 { return testPieceLength },
		Rand:        rand.New(rand.NewSource(1)),
		// These fixtures use tiny torrents, which would otherwise sit in
		// endgame permanently and duplicate every request. Tests that care
		// about endgame raise this themselves.
		EndgameThreshold: 1,
	}
	if tweak != nil {
		tweak(&cfg)
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("picker.New: %v", err)
	}
	return p
}

func everything(int) bool { return true }

// seedAvailability makes every piece available from one notional peer, so
// rarest-first has something to work with.
func seedAvailability(p *Picker, numPieces int) {
	p.Availability().AddPeer(bitfield.Full(numPieces))
}

func TestPickCoversAPieceExactlyOnce(t *testing.T) {
	p := newTestPicker(t, 4, nil)
	seedAvailability(p, 4)
	now := time.Now()

	reqs := p.Pick(everything, 4, now)
	if len(reqs) != 4 {
		t.Fatalf("Pick returned %d requests, want 4", len(reqs))
	}
	index := reqs[0].Index
	for i, r := range reqs {
		if r.Index != index {
			t.Fatalf("request %d is for piece %d, want the picker to finish piece %d first", i, r.Index, index)
		}
		if r.Begin != i*BlockLength || r.Length != BlockLength {
			t.Fatalf("request %d = %v, want begin %d length %d", i, r, i*BlockLength, BlockLength)
		}
	}

	// Everything is now outstanding, so a second Pick on the same piece must
	// hand out nothing more from it.
	more := p.Pick(func(i int) bool { return i == index }, 4, now)
	if len(more) != 0 {
		t.Fatalf("Pick handed out %d already-outstanding blocks: %v", len(more), more)
	}
}

func TestShortFinalPiece(t *testing.T) {
	// 2 pieces: a full one and a 100-byte tail.
	p := newTestPicker(t, 2, func(c *Config) {
		c.PieceLength = func(i int) int64 {
			if i == 1 {
				return 100
			}
			return testPieceLength
		}
		c.Strategy = Sequential
	})
	seedAvailability(p, 2)
	now := time.Now()

	// Drain the first piece.
	p.Pick(everything, 4, now)
	for i := 0; i < 4; i++ {
		p.Received(0, i*BlockLength, BlockLength)
	}
	p.MarkVerified(0)

	reqs := p.Pick(everything, 4, now)
	if len(reqs) != 1 {
		t.Fatalf("the short final piece produced %d requests, want 1: %v", len(reqs), reqs)
	}
	if reqs[0].Index != 1 || reqs[0].Begin != 0 || reqs[0].Length != 100 {
		t.Fatalf("final block = %v, want piece 1 [0,100)", reqs[0])
	}
}

func TestReceivedCompletesAPiece(t *testing.T) {
	p := newTestPicker(t, 2, nil)
	seedAvailability(p, 2)
	now := time.Now()

	reqs := p.Pick(everything, 4, now)
	index := reqs[0].Index

	for i, r := range reqs {
		done, wanted := p.Received(r.Index, r.Begin, r.Length)
		if !wanted {
			t.Fatalf("block %d was not wanted", i)
		}
		if want := i == len(reqs)-1; done != want {
			t.Fatalf("after block %d, completed = %v, want %v", i, done, want)
		}
	}

	// A duplicate of an already-stored block is not wanted.
	if done, wanted := p.Received(index, 0, BlockLength); done || wanted {
		t.Fatalf("a duplicate block reported done=%v wanted=%v", done, wanted)
	}
	// Neither is a block for a piece that was never started.
	if _, wanted := p.Received(1, 0, BlockLength); wanted {
		t.Fatal("a block for an unstarted piece was accepted")
	}
}

func TestReceivedRejectsMisalignedBlocks(t *testing.T) {
	p := newTestPicker(t, 1, nil)
	seedAvailability(p, 1)
	p.Pick(everything, 4, time.Now())

	for _, tc := range []struct{ begin, length int }{
		{begin: 1, length: BlockLength},               // not on a block boundary
		{begin: -BlockLength, length: BlockLength},    // negative
		{begin: 0, length: BlockLength - 1},           // wrong length
		{begin: 0, length: BlockLength + 1},           // too long
		{begin: 4 * BlockLength, length: BlockLength}, // past the end
	} {
		if _, wanted := p.Received(0, tc.begin, tc.length); wanted {
			t.Errorf("Received(0, %d, %d) was accepted", tc.begin, tc.length)
		}
	}
}

func TestExpireReoffersBlocks(t *testing.T) {
	// Two pieces with room for one in flight: enough to stay out of endgame,
	// which would otherwise re-offer outstanding blocks on its own.
	p := newTestPicker(t, 2, func(c *Config) {
		c.RequestTimeout = time.Second
		c.MaxActivePieces = 1
	})
	seedAvailability(p, 2)

	start := time.Now()
	if got := len(p.Pick(everything, 4, start)); got != 4 {
		t.Fatalf("Pick returned %d requests, want 4", got)
	}
	if got := len(p.Pick(everything, 4, start)); got != 0 {
		t.Fatalf("blocks were re-offered before the timeout: %d", got)
	}

	// Store one block so only three are still outstanding.
	p.Received(0, 0, BlockLength)

	if n := p.Expire(start.Add(500 * time.Millisecond)); n != 0 {
		t.Fatalf("Expire reset %d blocks before the timeout", n)
	}
	if n := p.Expire(start.Add(2 * time.Second)); n != 3 {
		t.Fatalf("Expire reset %d blocks, want 3", n)
	}

	again := p.Pick(everything, 4, start.Add(2*time.Second))
	if len(again) != 3 {
		t.Fatalf("after expiry Pick returned %d requests, want 3", len(again))
	}
	for _, r := range again {
		if r.Begin == 0 {
			t.Fatal("Expire re-offered a block that had already arrived")
		}
	}
}

func TestEndgameDuplicatesRequests(t *testing.T) {
	p := newTestPicker(t, 2, func(c *Config) { c.EndgameThreshold = 2 })
	seedAvailability(p, 2)
	now := time.Now()

	if !p.InEndgame() {
		t.Fatal("2 remaining pieces with a threshold of 2 is not endgame")
	}

	first := p.Pick(everything, 4, now)
	if len(first) != 4 {
		t.Fatalf("first Pick returned %d requests", len(first))
	}
	// A second peer asking must get the same blocks, not nothing.
	second := p.Pick(func(i int) bool { return i == first[0].Index }, 4, now)
	if len(second) != 4 {
		t.Fatalf("endgame did not duplicate: second Pick returned %d requests", len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("endgame handed out different blocks: %v vs %v", first[i], second[i])
		}
	}
}

func TestNoEndgameEarlyOn(t *testing.T) {
	p := newTestPicker(t, 100, func(c *Config) { c.EndgameThreshold = 8 })
	seedAvailability(p, 100)
	if p.InEndgame() {
		t.Fatal("endgame started with 100 pieces remaining")
	}

	now := time.Now()
	first := p.Pick(everything, 4, now)
	second := p.Pick(func(i int) bool { return i == first[0].Index }, 4, now)
	if len(second) != 0 {
		t.Fatalf("outside endgame a second peer got %d duplicate blocks", len(second))
	}
}

func TestCancels(t *testing.T) {
	p := newTestPicker(t, 1, nil)
	seedAvailability(p, 1)
	p.Pick(everything, 4, time.Now())

	// Two blocks arrive; the other two are still outstanding elsewhere.
	p.Received(0, 0, BlockLength)
	p.Received(0, BlockLength, BlockLength)

	cancels := p.Cancels(0)
	if len(cancels) != 2 {
		t.Fatalf("Cancels returned %d requests, want 2: %v", len(cancels), cancels)
	}
	for _, c := range cancels {
		if c.Begin != 2*BlockLength && c.Begin != 3*BlockLength {
			t.Fatalf("unexpected cancel %v", c)
		}
	}

	if got := p.Cancels(99); got != nil {
		t.Fatalf("Cancels for an unstarted piece returned %v", got)
	}
}

func TestMaxActivePiecesIsRespected(t *testing.T) {
	p := newTestPicker(t, 100, func(c *Config) { c.MaxActivePieces = 3 })
	seedAvailability(p, 100)
	now := time.Now()

	// Ask for far more blocks than 3 pieces can supply.
	reqs := p.Pick(everything, 1000, now)
	if p.ActiveCount() != 3 {
		t.Fatalf("%d pieces active, cap is 3", p.ActiveCount())
	}
	if len(reqs) != 12 { // 3 pieces x 4 blocks
		t.Fatalf("Pick returned %d requests, want 12", len(reqs))
	}

	seen := map[int]bool{}
	for _, r := range reqs {
		seen[r.Index] = true
	}
	if len(seen) != 3 {
		t.Fatalf("requests span %d pieces, want 3", len(seen))
	}
}

func TestRarestFirst(t *testing.T) {
	p := newTestPicker(t, 5, nil)
	avail := p.Availability()

	// Piece 3 is held by one peer, everything else by three.
	for i := 0; i < 5; i++ {
		count := 3
		if i == 3 {
			count = 1
		}
		for c := 0; c < count; c++ {
			avail.Add(i)
		}
	}

	reqs := p.Pick(everything, 1, time.Now())
	if len(reqs) != 1 {
		t.Fatalf("Pick returned %d requests", len(reqs))
	}
	if reqs[0].Index != 3 {
		t.Fatalf("rarest-first started piece %d, want the rarest (3)", reqs[0].Index)
	}
}

func TestRarestFirstSkipsUnavailablePieces(t *testing.T) {
	p := newTestPicker(t, 3, nil)
	// Only piece 2 is available anywhere. Pieces 0 and 1 have a count of zero,
	// which means unobtainable, not maximally rare.
	p.Availability().Add(2)

	reqs := p.Pick(everything, 1, time.Now())
	if len(reqs) != 1 || reqs[0].Index != 2 {
		t.Fatalf("Pick returned %v, want a request for piece 2", reqs)
	}
}

// TestRarestFirstBreaksTiesRandomly matters for swarm health: without it every
// peer starts the same piece at the same instant.
func TestRarestFirstBreaksTiesRandomly(t *testing.T) {
	chosen := map[int]int{}
	for seed := int64(0); seed < 200; seed++ {
		p := newTestPicker(t, 10, func(c *Config) { c.Rand = rand.New(rand.NewSource(seed)) })
		seedAvailability(p, 10) // every piece equally available
		reqs := p.Pick(everything, 1, time.Now())
		chosen[reqs[0].Index]++
	}
	if len(chosen) < 5 {
		t.Fatalf("ties resolved to only %d distinct pieces across 200 runs: %v", len(chosen), chosen)
	}
}

func TestSequentialStrategy(t *testing.T) {
	p := newTestPicker(t, 5, func(c *Config) { c.Strategy = Sequential; c.MaxActivePieces = 1 })
	seedAvailability(p, 5)
	now := time.Now()

	for expected := 0; expected < 5; expected++ {
		reqs := p.Pick(everything, 4, now)
		if len(reqs) == 0 || reqs[0].Index != expected {
			t.Fatalf("sequential picked %v, want piece %d", reqs, expected)
		}
		for _, r := range reqs {
			p.Received(r.Index, r.Begin, r.Length)
		}
		p.MarkVerified(expected)
	}
	if !p.Complete() {
		t.Fatal("picker is not complete after every piece was verified")
	}
}

func TestPeerWithoutThePieceGetsNothing(t *testing.T) {
	p := newTestPicker(t, 4, nil)
	seedAvailability(p, 4)

	if reqs := p.Pick(func(int) bool { return false }, 10, time.Now()); len(reqs) != 0 {
		t.Fatalf("a peer with no pieces got %d requests", len(reqs))
	}
}

func TestMarkFailedRewindsThePiece(t *testing.T) {
	p := newTestPicker(t, 1, nil)
	seedAvailability(p, 1)
	now := time.Now()

	reqs := p.Pick(everything, 4, now)
	for _, r := range reqs {
		p.Received(r.Index, r.Begin, r.Length)
	}

	// Hash mismatch: every block has to come again, there is no way to tell
	// which one was bad.
	p.MarkFailed(0)
	again := p.Pick(everything, 4, now)
	if len(again) != 4 {
		t.Fatalf("after MarkFailed, Pick returned %d requests, want 4", len(again))
	}
	if p.Complete() {
		t.Fatal("picker reports complete after a failed piece")
	}
}

func TestSetHaveFromResumeData(t *testing.T) {
	p := newTestPicker(t, 10, nil)
	seedAvailability(p, 10)

	resume := bitfield.New(10)
	for i := 0; i < 7; i++ {
		resume.Set(i)
	}
	if err := p.SetHave(resume); err != nil {
		t.Fatalf("SetHave: %v", err)
	}
	if p.Remaining() != 3 {
		t.Fatalf("Remaining() = %d, want 3", p.Remaining())
	}

	reqs := p.Pick(everything, 100, time.Now())
	for _, r := range reqs {
		if r.Index < 7 {
			t.Fatalf("picked piece %d, which resume data says we already have", r.Index)
		}
	}

	if err := p.SetHave(bitfield.New(9)); err == nil {
		t.Fatal("SetHave accepted a bitfield of the wrong width")
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(Config{NumPieces: 0, PieceLength: func(int) int64 { return 1 }}); err == nil {
		t.Error("New accepted zero pieces")
	}
	if _, err := New(Config{NumPieces: 1}); err == nil {
		t.Error("New accepted a nil PieceLength")
	}
}

// TestFullDownload runs the picker through a whole torrent the way the torrent
// actor will, with several peers and blocks arriving out of order.
func TestFullDownload(t *testing.T) {
	const numPieces = 40
	p := newTestPicker(t, numPieces, func(c *Config) { c.MaxActivePieces = 5 })

	peers := []*bitfield.Bitfield{
		bitfield.Full(numPieces),
		bitfield.New(numPieces),
	}
	for i := 0; i < numPieces; i += 2 {
		peers[1].Set(i)
	}
	for _, bf := range peers {
		p.Availability().AddPeer(bf)
	}

	now := time.Now()
	for step := 0; !p.Complete(); step++ {
		if step > 10000 {
			t.Fatalf("download did not converge: %d pieces left", p.Remaining())
		}
		progressed := false
		for _, bf := range peers {
			reqs := p.Pick(func(i int) bool { return bf.Has(i) }, 8, now)
			for _, r := range reqs {
				progressed = true
				done, _ := p.Received(r.Index, r.Begin, r.Length)
				if done {
					p.MarkVerified(r.Index)
				}
			}
		}
		if !progressed {
			now = now.Add(time.Minute)
			if p.Expire(now) == 0 {
				t.Fatalf("stalled with %d pieces left and nothing to expire", p.Remaining())
			}
		}
	}

	if p.ActiveCount() != 0 {
		t.Fatalf("%d pieces still active after completion", p.ActiveCount())
	}
	if p.Have().Count() != numPieces {
		t.Fatalf("have %d of %d pieces", p.Have().Count(), numPieces)
	}
}

func TestAvailability(t *testing.T) {
	a := NewAvailability(8)
	if a.Len() != 8 {
		t.Fatalf("Len() = %d", a.Len())
	}

	peer := bitfield.New(8)
	peer.Set(1)
	peer.Set(3)
	a.AddPeer(peer)
	if a.Count(1) != 1 || a.Count(3) != 1 || a.Count(0) != 0 {
		t.Fatalf("after AddPeer: %d %d %d", a.Count(0), a.Count(1), a.Count(3))
	}

	a.Add(1)
	if a.Count(1) != 2 {
		t.Fatalf("after Add: %d", a.Count(1))
	}

	a.RemovePeer(peer)
	if a.Count(1) != 1 || a.Count(3) != 0 {
		t.Fatalf("after RemovePeer: %d %d", a.Count(1), a.Count(3))
	}

	// Counts must never go negative, however many removals arrive.
	a.RemovePeer(peer)
	a.RemovePeer(peer)
	if a.Count(1) != 0 || a.Count(3) != 0 {
		t.Fatalf("counts went negative: %d %d", a.Count(1), a.Count(3))
	}

	// Out-of-range access is safe.
	if a.Count(-1) != 0 || a.Count(99) != 0 {
		t.Fatal("out-of-range Count returned non-zero")
	}
	a.Add(-1)
	a.Add(99)
}

func TestAvailabilityRarest(t *testing.T) {
	a := NewAvailability(5)
	for i := 0; i < 5; i++ {
		for c := 0; c <= i; c++ {
			a.Add(i)
		}
	}
	// Piece 0 has 1 peer, piece 4 has 5.
	if got := a.Rarest(func(int) bool { return true }); got != 0 {
		t.Fatalf("Rarest() = %d, want 0", got)
	}
	if got := a.Rarest(func(i int) bool { return i >= 2 }); got != 2 {
		t.Fatalf("filtered Rarest() = %d, want 2", got)
	}
	if got := a.Rarest(func(int) bool { return false }); got != -1 {
		t.Fatalf("Rarest() with nothing wanted = %d, want -1", got)
	}
}

func BenchmarkPickRarestFirst(b *testing.B) {
	const numPieces = 20000
	p, err := New(Config{
		NumPieces:       numPieces,
		PieceLength:     func(int) int64 { return testPieceLength },
		MaxActivePieces: 64,
		Rand:            rand.New(rand.NewSource(1)),
	})
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		p.Availability().AddPeer(bitfield.Full(numPieces))
	}

	now := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Pick(everything, 16, now)
	}
}
