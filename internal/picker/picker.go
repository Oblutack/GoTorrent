package picker

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bitfield"
)

// BlockLength is the request size every client uses (BEP 3).
const BlockLength = 16384

// Strategy selects the order pieces are started in.
type Strategy int

const (
	// RarestFirst starts the pieces fewest peers hold, which keeps the swarm
	// healthy and is the right default.
	RarestFirst Strategy = iota

	// Sequential starts pieces in order. Useful for streaming, terrible for
	// the swarm.
	Sequential
)

func (s Strategy) String() string {
	if s == Sequential {
		return "sequential"
	}
	return "rarest-first"
}

// blockState is where one block of an in-progress piece has got to.
type blockState uint8

const (
	blockNeeded blockState = iota
	blockPending
	blockDone
)

// Request is one block to ask a peer for.
type Request struct {
	Index  int
	Begin  int
	Length int
}

func (r Request) String() string {
	return fmt.Sprintf("piece %d [%d,%d)", r.Index, r.Begin, r.Begin+r.Length)
}

// pieceProgress tracks the blocks of one piece that has been started.
type pieceProgress struct {
	index  int
	length int64

	states    []blockState
	requested []time.Time
	done      int
}

func (p *pieceProgress) complete() bool { return p.done == len(p.states) }

func (p *pieceProgress) blockLength(i int) int {
	offset := int64(i) * BlockLength
	if remaining := p.length - offset; remaining < BlockLength {
		return int(remaining)
	}
	return BlockLength
}

// reset puts every block back to needed, which is what a hash failure means.
func (p *pieceProgress) reset() {
	for i := range p.states {
		p.states[i] = blockNeeded
	}
	p.done = 0
}

// Config configures a Picker.
type Config struct {
	// NumPieces is the torrent's piece count.
	NumPieces int

	// PieceLength returns the length of a piece, accounting for the short
	// final one.
	PieceLength func(index int) int64

	// Strategy is the piece ordering. Defaults to RarestFirst.
	Strategy Strategy

	// MaxActivePieces bounds how many pieces are in progress at once. Each one
	// holds a full piece buffer upstream, so this is what bounds memory.
	// Defaults to 64.
	MaxActivePieces int

	// RequestTimeout is how long a block may be outstanding before it is
	// offered to another peer. Defaults to 15s.
	RequestTimeout time.Duration

	// EndgameThreshold is how many pieces may remain before endgame starts.
	// In endgame the same block is requested from several peers at once, so
	// one slow peer cannot hold the whole download hostage. Defaults to 8.
	EndgameThreshold int

	// EndgameTimeout is the shorter timeout used during endgame. Defaults
	// to 3s.
	EndgameTimeout time.Duration

	// Rand is the source used to break ties between equally rare pieces.
	// Defaults to a private source seeded from the clock.
	Rand *rand.Rand
}

func (c *Config) withDefaults() {
	if c.Strategy != Sequential {
		c.Strategy = RarestFirst
	}
	if c.MaxActivePieces <= 0 {
		c.MaxActivePieces = 64
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = 15 * time.Second
	}
	if c.EndgameThreshold <= 0 {
		c.EndgameThreshold = 8
	}
	if c.EndgameTimeout <= 0 {
		c.EndgameTimeout = 3 * time.Second
	}
	if c.Rand == nil {
		c.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
}

// Picker owns which pieces are in progress and which blocks are outstanding.
//
// It is not safe for concurrent use. It is designed to be owned by a single
// goroutine — the torrent actor — which is what removes the global lock the
// old design took twenty times a second.
type Picker struct {
	cfg Config

	have   *bitfield.Bitfield
	avail  *Availability
	active map[int]*pieceProgress

	// order is a scratch slice reused by the rarest-first scan so picking does
	// not allocate on every tick.
	order []int
}

// New returns a Picker for a torrent.
func New(cfg Config) (*Picker, error) {
	if cfg.NumPieces <= 0 {
		return nil, fmt.Errorf("picker: NumPieces must be positive, got %d", cfg.NumPieces)
	}
	if cfg.PieceLength == nil {
		return nil, fmt.Errorf("picker: PieceLength is required")
	}
	cfg.withDefaults()

	return &Picker{
		cfg:    cfg,
		have:   bitfield.New(cfg.NumPieces),
		avail:  NewAvailability(cfg.NumPieces),
		active: make(map[int]*pieceProgress),
	}, nil
}

// Have exposes the verified-pieces bitfield. Callers must not mutate it
// directly; use MarkVerified.
func (p *Picker) Have() *bitfield.Bitfield { return p.have }

// Availability exposes the index so peer events can update it.
func (p *Picker) Availability() *Availability { return p.avail }

// Complete reports whether every piece is verified.
func (p *Picker) Complete() bool { return p.have.Complete() }

// Remaining is how many pieces are still missing.
func (p *Picker) Remaining() int { return p.cfg.NumPieces - p.have.Count() }

// ActiveCount is how many pieces are currently in progress.
func (p *Picker) ActiveCount() int { return len(p.active) }

// InEndgame reports whether the picker is duplicating requests to finish off
// the last few pieces.
func (p *Picker) InEndgame() bool {
	return p.Remaining() > 0 && p.Remaining() <= p.cfg.EndgameThreshold
}

// SetHave seeds the picker from resume data.
func (p *Picker) SetHave(have *bitfield.Bitfield) error {
	if have.Len() != p.cfg.NumPieces {
		return fmt.Errorf("picker: bitfield covers %d pieces, torrent has %d", have.Len(), p.cfg.NumPieces)
	}
	p.have = have.Clone()
	for index := range p.active {
		if p.have.Has(index) {
			delete(p.active, index)
		}
	}
	return nil
}

// MarkVerified records a piece as verified and on disk.
func (p *Picker) MarkVerified(index int) {
	p.have.Set(index)
	delete(p.active, index)
}

// MarkFailed puts a piece back after a hash mismatch. Every block is
// re-requested; there is no way to tell which one was corrupt.
func (p *Picker) MarkFailed(index int) {
	if pp, ok := p.active[index]; ok {
		pp.reset()
		return
	}
	p.have.Clear(index)
}

// Received records the arrival of a block and reports whether that completed
// the piece. A block that was not outstanding — a duplicate from the endgame,
// or a late arrival after a timeout — returns false and is otherwise ignored.
func (p *Picker) Received(index, begin, length int) (completed bool, wanted bool) {
	pp, ok := p.active[index]
	if !ok {
		return false, false
	}
	blockIndex, ok := blockIndexOf(pp, begin, length)
	if !ok || pp.states[blockIndex] == blockDone {
		return false, false
	}
	pp.states[blockIndex] = blockDone
	pp.done++
	return pp.complete(), true
}

func blockIndexOf(pp *pieceProgress, begin, length int) (int, bool) {
	if begin < 0 || begin%BlockLength != 0 {
		return 0, false
	}
	i := begin / BlockLength
	if i >= len(pp.states) {
		return 0, false
	}
	if length != pp.blockLength(i) {
		return 0, false
	}
	return i, true
}

// Expire returns outstanding blocks whose requests have timed out to the
// needed state, and reports how many were reset.
func (p *Picker) Expire(now time.Time) int {
	timeout := p.cfg.RequestTimeout
	if p.InEndgame() {
		timeout = p.cfg.EndgameTimeout
	}

	reset := 0
	for _, pp := range p.active {
		for i, state := range pp.states {
			if state == blockPending && now.Sub(pp.requested[i]) > timeout {
				pp.states[i] = blockNeeded
				reset++
			}
		}
	}
	return reset
}

// Cancels returns the requests that should be cancelled on other peers now
// that a piece is complete. BitTorrent's Cancel message exists precisely for
// the endgame, where the same block is outstanding on several connections.
func (p *Picker) Cancels(index int) []Request {
	pp, ok := p.active[index]
	if !ok {
		return nil
	}
	var out []Request
	for i, state := range pp.states {
		if state == blockPending {
			out = append(out, Request{Index: index, Begin: i * BlockLength, Length: pp.blockLength(i)})
		}
	}
	return out
}

// Pick returns up to max block requests for a peer, given what that peer has.
//
// It first fills out pieces that are already in progress, then starts new ones
// if there is room under MaxActivePieces. During endgame it will hand out
// blocks that are already outstanding elsewhere.
func (p *Picker) Pick(peerHas func(index int) bool, max int, now time.Time) []Request {
	if max <= 0 || p.Complete() {
		return nil
	}

	endgame := p.InEndgame()
	out := make([]Request, 0, max)

	// In-progress pieces first: finishing a started piece frees its buffer and
	// gets us something to share sooner than spreading across new ones.
	for _, index := range p.activeOrder() {
		if len(out) >= max {
			return out
		}
		if !peerHas(index) {
			continue
		}
		out = p.fill(out, p.active[index], max, now, endgame)
	}

	for len(out) < max && len(p.active) < p.cfg.MaxActivePieces {
		index := p.nextPiece(peerHas)
		if index < 0 {
			break
		}
		pp := p.start(index)
		out = p.fill(out, pp, max, now, endgame)
	}

	return out
}

// activeOrder returns the in-progress piece indexes. Map iteration order is
// random in Go, which would scatter requests across pieces; sorting keeps a
// peer working on the same piece from one tick to the next.
func (p *Picker) activeOrder() []int {
	p.order = p.order[:0]
	for index := range p.active {
		p.order = append(p.order, index)
	}
	sort.Ints(p.order)
	return p.order
}

// fill appends requests from one piece.
func (p *Picker) fill(out []Request, pp *pieceProgress, max int, now time.Time, endgame bool) []Request {
	for i, state := range pp.states {
		if len(out) >= max {
			return out
		}
		switch state {
		case blockDone:
			continue
		case blockPending:
			// Outside endgame a block outstanding on another peer is left
			// alone; inside it, duplicating is the whole point.
			if !endgame {
				continue
			}
		}
		pp.states[i] = blockPending
		pp.requested[i] = now
		out = append(out, Request{
			Index:  pp.index,
			Begin:  i * BlockLength,
			Length: pp.blockLength(i),
		})
	}
	return out
}

// nextPiece chooses a piece to start.
func (p *Picker) nextPiece(peerHas func(index int) bool) int {
	want := func(index int) bool {
		if p.have.Has(index) {
			return false
		}
		if _, active := p.active[index]; active {
			return false
		}
		return peerHas(index)
	}

	if p.cfg.Strategy == Sequential {
		for i := 0; i < p.cfg.NumPieces; i++ {
			if want(i) {
				return i
			}
		}
		return -1
	}

	return p.rarestWithTieBreak(want)
}

// rarestWithTieBreak picks the least available piece, choosing uniformly at
// random between ties. Without the tie-break every peer starts the same piece
// at the same moment, which is exactly the wrong thing for a swarm.
func (p *Picker) rarestWithTieBreak(want func(int) bool) int {
	best, bestCount, ties := -1, 0, 0
	for i := 0; i < p.cfg.NumPieces; i++ {
		count := p.avail.Count(i)
		if count == 0 || !want(i) {
			continue
		}
		switch {
		case best < 0 || count < bestCount:
			best, bestCount, ties = i, count, 1
		case count == bestCount:
			ties++
			// Reservoir sampling: each of the k equally rare pieces ends up
			// with probability 1/k, in one pass and without allocating.
			if p.cfg.Rand.Intn(ties) == 0 {
				best = i
			}
		}
	}
	return best
}

func (p *Picker) start(index int) *pieceProgress {
	length := p.cfg.PieceLength(index)
	blocks := int((length + BlockLength - 1) / BlockLength)
	pp := &pieceProgress{
		index:     index,
		length:    length,
		states:    make([]blockState, blocks),
		requested: make([]time.Time, blocks),
	}
	p.active[index] = pp
	return pp
}
