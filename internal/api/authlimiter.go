package api

import (
	"sync"
	"time"
)

// AuthFailureLimiter locks an address out of the API entirely for a cool-down
// period once it has failed the bearer-token check too many times in too
// short a window - a brute-force guard, separate from (and layered
// underneath) the bearer token itself: even a 256-bit token is not much of
// a defense on its own against a caller that gets to try forever with no
// consequence.
//
// This is deliberately its own small tracker rather than internal/ratelimit
// reused - ratelimit is a bytes-as-tokens rate limiter for transfer
// throughput, a different shape of problem (a continuous rate vs. "how
// many failures in this window"), and forcing one onto the other would be
// more confusing than a dozen lines of its own.
type AuthFailureLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	maxFails int
	window   time.Duration
	lockout  time.Duration
	lockedAt map[string]time.Time
}

// NewAuthFailureLimiter locks an address out for lockout once it has
// racked up maxFails failures within window. A zero maxFails means
// unlimited (the limiter is a no-op) - used by tests that want to isolate
// the bearer-token check itself from this layer.
func NewAuthFailureLimiter(maxFails int, window, lockout time.Duration) *AuthFailureLimiter {
	return &AuthFailureLimiter{
		failures: make(map[string][]time.Time),
		lockedAt: make(map[string]time.Time),
		maxFails: maxFails,
		window:   window,
		lockout:  lockout,
	}
}

// Allowed reports whether addr may attempt authentication right now - false
// while it is serving out a lockout from RecordFailure tripping the
// threshold.
func (l *AuthFailureLimiter) Allowed(addr string) bool {
	if l.maxFails <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if lockedSince, ok := l.lockedAt[addr]; ok {
		if time.Since(lockedSince) < l.lockout {
			return false
		}
		delete(l.lockedAt, addr)
		delete(l.failures, addr)
	}
	return true
}

// RecordFailure notes one more failed attempt from addr, pruning attempts
// older than window before checking whether the threshold is now crossed.
func (l *AuthFailureLimiter) RecordFailure(addr string) {
	if l.maxFails <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)
	kept := l.failures[addr][:0]
	for _, t := range l.failures[addr] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	l.failures[addr] = kept

	if len(kept) >= l.maxFails {
		l.lockedAt[addr] = now
	}
}

// RecordSuccess clears addr's failure history - a caller that eventually
// authenticates correctly should not still be one stray failure away from
// a lockout an hour later.
func (l *AuthFailureLimiter) RecordSuccess(addr string) {
	if l.maxFails <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, addr)
	delete(l.lockedAt, addr)
}
