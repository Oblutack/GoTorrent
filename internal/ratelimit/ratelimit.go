// Package ratelimit provides a hand-rolled token bucket for capping transfer
// rates, shared across every connection that draws from the same *Limiter.
package ratelimit

import (
	"sync"
	"time"
)

// minBurst is the smallest bucket size regardless of the configured rate, so
// a single request for one block is never permanently starved by a rate so
// low its steady-state burst would otherwise be smaller than one block.
const minBurst = 16 * 1024

// Limiter is a token bucket measured in bytes. The zero value is not usable;
// construct one with New. A Limiter is safe for concurrent use, and is
// normally shared by every connection whose combined rate it should bound —
// one Limiter per peer connection would cap each peer individually rather
// than the aggregate, which is never what a bandwidth cap means in practice.
type Limiter struct {
	mu sync.Mutex

	limit float64 // bytes/sec; <= 0 means unlimited
	burst float64 // maximum positive token balance
	// tokens may go negative: a reservation larger than the current balance
	// is granted immediately but recorded as debt, and the caller waits out
	// that debt instead of the bucket blocking outright. This keeps Wait
	// lock-free after the initial reservation and gives concurrent callers a
	// deterministic, fairly-ordered share of the rate.
	tokens float64
	last   time.Time
}

// New creates a Limiter capped at bytesPerSecond. A rate of 0 or less means
// unlimited: Wait always returns immediately.
func New(bytesPerSecond int64) *Limiter {
	l := &Limiter{last: time.Now()}
	l.SetLimit(bytesPerSecond)
	l.tokens = l.burst
	return l
}

// Unlimited returns a Limiter with no cap. It is equivalent to New(0); the
// separate name documents intent at call sites that wire optional limits.
func Unlimited() *Limiter { return New(0) }

// SetLimit changes the rate at runtime. Existing token balance is clamped to
// the new burst size but otherwise preserved, so tightening the limit does
// not grant a free burst and loosening it does not discard saved-up credit.
func (l *Limiter) SetLimit(bytesPerSecond int64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.limit = float64(bytesPerSecond)
	l.burst = l.limit
	if l.burst < minBurst {
		l.burst = minBurst
	}
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
}

// Wait blocks until n bytes' worth of budget is available, or done is
// closed first. It reports whether the budget was granted; false means done
// fired and the reservation was refunded, so the caller made no progress and
// should treat this like any other cancellation.
func (l *Limiter) Wait(done <-chan struct{}, n int) bool {
	if n <= 0 {
		return true
	}
	d := l.reserve(n)
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-done:
		l.refund(n)
		return false
	}
}

// reserve debits n tokens immediately and reports how long the caller must
// wait for that debit to be earned back, refilling the bucket for elapsed
// time first.
func (l *Limiter) reserve(n int) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.limit <= 0 {
		return 0
	}

	now := time.Now()
	if elapsed := now.Sub(l.last).Seconds(); elapsed > 0 {
		l.tokens += elapsed * l.limit
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
	}

	l.tokens -= float64(n)
	if l.tokens >= 0 {
		return 0
	}
	return time.Duration(-l.tokens / l.limit * float64(time.Second))
}

func (l *Limiter) refund(n int) {
	l.mu.Lock()
	l.tokens += float64(n)
	l.mu.Unlock()
}
