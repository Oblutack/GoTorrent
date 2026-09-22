// Package simulator is a deterministic, discrete-event swarm simulator. It
// drives the real internal/picker and internal/choker packages — the actual
// production piece-selection and unchoke logic, not a reimplementation —
// against a virtual clock and an in-memory network model, so a strategy
// change to either package is automatically reflected here with no code to
// keep in sync.
//
// Deliberately out of scope: real byte content, real SHA-1 verification,
// the real wire protocol (bencode, handshakes, TCP). A "piece" here is
// purely an index and a length; "having" one is a boolean. Correctness of
// the actual wire protocol and on-disk storage is already covered
// exhaustively by internal/torrent's own real seed/leech tests — this
// package exists to make picker/choker *strategy* measurable (does
// rarest-first actually beat sequential under churn? does the choker
// converge to rewarding the fastest peers?), which nothing else in this
// project can answer, since the real actor is wired to real wall-clock
// goroutines and real sockets.
package simulator

import (
	"container/heap"
	"time"
)

// epoch is the virtual clock's zero point. An arbitrary fixed instant
// rather than time.Time{} (the zero Time) so log output and duration math
// read naturally; its only role is to give picker.Pick/Expire and
// choker.Run — both of which take a real time.Time — something to compute
// Sub() against.
var epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// event is one scheduled action. fire runs synchronously on the single
// simulation goroutine and may itself schedule further events (a request
// arriving schedules the block's later delivery, a block arriving
// schedules the next request, and so on) — this is what makes the whole
// simulation a single-threaded, deterministic chain of cause and effect
// with no goroutines, no locks, and no real waiting.
type event struct {
	at   time.Duration
	seq  uint64 // insertion order — the tie-break for two events at the same instant, so a given seed always replays identically
	fire func()
}

// eventQueue is a min-heap ordered by (at, seq), the classic discrete-event
// simulation priority queue.
type eventQueue []event

func (q eventQueue) Len() int { return len(q) }
func (q eventQueue) Less(i, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	return q[i].seq < q[j].seq
}
func (q eventQueue) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *eventQueue) Push(x interface{}) { *q = append(*q, x.(event)) }
func (q *eventQueue) Pop() interface{} {
	old := *q
	n := len(old)
	e := old[n-1]
	*q = old[:n-1]
	return e
}

// Clock is the virtual-time event scheduler at the heart of the
// simulation. Nothing in this package ever calls time.Sleep or reads real
// wall-clock time — the only way time passes is by popping the next
// scheduled event and jumping straight to it, which is what makes a
// 200-peer, hours-long-in-swarm-time run finish in real milliseconds.
type Clock struct {
	now   time.Duration
	queue eventQueue
	seq   uint64
}

// newClock returns a Clock at t=0.
func newClock() *Clock {
	c := &Clock{}
	heap.Init(&c.queue)
	return c
}

// Now returns the current virtual time as a real time.Time, anchored to
// epoch — exactly what picker.Pick/Expire and choker.Run expect.
func (c *Clock) Now() time.Time { return epoch.Add(c.now) }

// Elapsed returns how much virtual time has passed since the simulation
// began.
func (c *Clock) Elapsed() time.Duration { return c.now }

// After schedules fire to run once, delay after the current time. delay
// must be >= 0 — scheduling into the past would make event order
// ambiguous with events already popped.
func (c *Clock) After(delay time.Duration, fire func()) {
	if delay < 0 {
		delay = 0
	}
	c.seq++
	heap.Push(&c.queue, event{at: c.now + delay, seq: c.seq, fire: fire})
}

// step pops and runs the single earliest-scheduled event, advancing now to
// its time. Reports false when the queue is empty — nothing left to
// simulate.
func (c *Clock) step() bool {
	if c.queue.Len() == 0 {
		return false
	}
	e := heap.Pop(&c.queue).(event)
	c.now = e.at
	e.fire()
	return true
}
