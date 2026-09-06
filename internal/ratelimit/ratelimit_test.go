package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestUnlimitedNeverWaits(t *testing.T) {
	l := Unlimited()
	done := make(chan struct{})
	start := time.Now()
	if !l.Wait(done, 10<<20) {
		t.Fatal("Wait on an unlimited limiter returned false")
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("Wait on an unlimited limiter took %s, want ~instant", elapsed)
	}
}

func TestZeroOrNegativeNIsNoop(t *testing.T) {
	l := New(1) // one byte per second: any real wait would dominate the test
	done := make(chan struct{})
	if !l.Wait(done, 0) {
		t.Fatal("Wait(0) returned false")
	}
	if !l.Wait(done, -5) {
		t.Fatal("Wait(negative) returned false")
	}
}

func TestReserveWithinBurstIsImmediate(t *testing.T) {
	l := New(1000) // burst clamps to minBurst since 1000 < minBurst
	done := make(chan struct{})
	start := time.Now()
	if !l.Wait(done, 4096) {
		t.Fatal("Wait returned false")
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("Wait for a within-burst request took %s, want ~instant", elapsed)
	}
}

func TestReserveBeyondBurstWaits(t *testing.T) {
	const rate = 100 * 1024 // 100 KiB/s, well above minBurst
	l := New(rate)
	done := make(chan struct{})

	// Drain the initial burst so the next reservation must wait on the rate.
	if !l.Wait(done, rate) {
		t.Fatal("initial burst-draining Wait returned false")
	}

	const n = 20 * 1024 // ~200ms worth at 100 KiB/s
	start := time.Now()
	if !l.Wait(done, n) {
		t.Fatal("Wait returned false")
	}
	elapsed := time.Since(start)
	want := time.Duration(float64(n) / float64(rate) * float64(time.Second))
	if elapsed < want/2 || elapsed > want*3 {
		t.Fatalf("Wait took %s, want roughly %s", elapsed, want)
	}
}

func TestWaitCancelledRefunds(t *testing.T) {
	const rate = 32 * 1024 // >= minBurst, so burst equals rate exactly
	l := New(rate)
	done := make(chan struct{})

	// Drain the burst fully, then start a wait that needs real time to
	// satisfy — otherwise Wait returns before ever looking at done.
	l.Wait(done, rate)
	const n = 4096

	cancelled := make(chan struct{})
	close(cancelled)
	if l.Wait(cancelled, n) {
		t.Fatal("Wait on an already-closed channel should report false")
	}

	// The reservation should have been refunded: waiting for the same n
	// again (on a fresh, uncancelled done) should take about the same time
	// as it would have the first time, not double.
	start := time.Now()
	if !l.Wait(done, n) {
		t.Fatal("Wait returned false")
	}
	elapsed := time.Since(start)
	want := time.Duration(float64(n) / float64(rate) * float64(time.Second))
	if elapsed > want*3 {
		t.Fatalf("Wait after a cancelled reservation took %s, want roughly %s (refund likely missing)", elapsed, want)
	}
}

func TestSetLimitClampsExistingTokens(t *testing.T) {
	const newLimit = 64 * 1024
	l := New(1 << 20) // 1 MiB/s, burst = 1 MiB, tokens start full
	l.SetLimit(newLimit)

	done := make(chan struct{})
	// The bucket should now hold at most newLimit tokens, so a request for
	// more than that must wait rather than draining a stale, larger balance.
	start := time.Now()
	if !l.Wait(done, newLimit+4096) {
		t.Fatal("Wait returned false")
	}
	if elapsed := time.Since(start); elapsed < 1*time.Millisecond {
		t.Fatalf("Wait after tightening the limit was ~instant (%s); stale tokens were not clamped", elapsed)
	}
}

func TestConcurrentReservationsShareTheBudget(t *testing.T) {
	const rate = 200 * 1024
	l := New(rate)
	done := make(chan struct{})
	l.Wait(done, rate) // drain the initial burst

	const workers = 8
	const perWorker = 4096 // 32 KiB total, ~160ms at 200 KiB/s
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !l.Wait(done, perWorker) {
				t.Error("Wait returned false")
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	want := time.Duration(float64(workers*perWorker) / float64(rate) * float64(time.Second))
	if elapsed < want/2 {
		t.Fatalf("concurrent reservations finished in %s, want at least ~%s (budget not shared)", elapsed, want)
	}
}
