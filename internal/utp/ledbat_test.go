package utp

import (
	"testing"
	"time"
)

func TestLedbatStartsAtInitCwnd(t *testing.T) {
	l := newLedbat()
	want := ledbatInitCwndMSS * mss
	if l.cwndBytes() != want {
		t.Fatalf("cwndBytes() = %d, want %d (INIT_CWND * MSS)", l.cwndBytes(), want)
	}
}

// TestOnAckGrowsCwndByHandComputedAmount pins RFC 6817's own formula
// against a hand-computed value for one concrete, unclamped case — not
// just a round trip through the same arithmetic the production code
// itself performs. With queuing_delay == 0 (the sample equals the
// already-established base delay), off_target == 1.0 exactly, so
// cwnd += GAIN * 1.0 * bytesAcked * MSS / cwnd is fully determined:
// 2800 + (1400*1400)/2800 = 2800 + 700 = 3500.
func TestOnAckGrowsCwndByHandComputedAmount(t *testing.T) {
	l := newLedbat()
	now := time.Now()
	l.send(2 * mss) // 2800 bytes in flight, so the ALLOWED_INCREASE clamp (flightsize + 1*MSS = 4200) doesn't bind here

	l.onAck(now, 50*time.Millisecond, mss) // first-ever sample: base_delay becomes exactly this sample too

	want := 3500
	if l.cwndBytes() != want {
		t.Fatalf("cwndBytes() = %d, want %d", l.cwndBytes(), want)
	}
}

// TestOnAckAllowedIncreaseClampBinds is the companion case where the
// ALLOWED_INCREASE clamp *does* bind: only 1 MSS was ever in flight, so
// max_allowed_cwnd = flightsize + 1*MSS = 2*MSS, exactly the starting
// cwnd — the formula's own unclamped result (3500) must be capped back
// down to 2800.
func TestOnAckAllowedIncreaseClampBinds(t *testing.T) {
	l := newLedbat()
	now := time.Now()
	l.send(mss) // only 1400 bytes in flight

	l.onAck(now, 50*time.Millisecond, mss)

	want := 2 * mss
	if l.cwndBytes() != want {
		t.Fatalf("cwndBytes() = %d, want %d (ALLOWED_INCREASE should have clamped growth back down)", l.cwndBytes(), want)
	}
}

// TestSustainedHighDelayShrinksCwnd is the actual point of LEDBAT,
// verified behaviorally: once a real queuing delay trend is established
// (current delay well above the already-established base delay), cwnd
// must genuinely shrink, not just fail to grow.
func TestSustainedHighDelayShrinksCwnd(t *testing.T) {
	l := newLedbat()
	now := time.Now()

	// Establish a low base delay first.
	l.send(4 * mss)
	l.onAck(now, 10*time.Millisecond, mss)
	afterBaseline := l.cwndBytes()

	// Now feed several samples at a much higher delay - enough for the
	// current-delay filter's window to fully turn over.
	for i := 0; i < ledbatCurrentFilterSize+2; i++ {
		l.send(mss)
		l.onAck(now, 300*time.Millisecond, mss)
	}

	if l.cwndBytes() >= afterBaseline {
		t.Fatalf("cwnd = %d after sustained high delay, want it to have shrunk below the post-baseline %d", l.cwndBytes(), afterBaseline)
	}
}

func TestCwndRecoversOnceDelaySubsides(t *testing.T) {
	l := newLedbat()
	now := time.Now()

	l.send(4 * mss)
	l.onAck(now, 10*time.Millisecond, mss)

	for i := 0; i < ledbatCurrentFilterSize+2; i++ {
		l.send(mss)
		l.onAck(now, 300*time.Millisecond, mss)
	}
	shrunk := l.cwndBytes()

	// Delay drops back to baseline - cwnd should start growing again, not
	// stay pinned at the shrunk value.
	for i := 0; i < ledbatCurrentFilterSize+2; i++ {
		l.send(4 * mss)
		l.onAck(now, 10*time.Millisecond, mss)
	}

	if l.cwndBytes() <= shrunk {
		t.Fatalf("cwnd = %d after delay subsided, want growth above the shrunk value %d", l.cwndBytes(), shrunk)
	}
}

func TestCwndNeverDropsBelowMinCwnd(t *testing.T) {
	l := newLedbat()
	now := time.Now()
	l.send(mss)
	l.onAck(now, 10*time.Millisecond, mss)

	// Extreme, sustained delay - cwnd must still never go below MIN_CWND*MSS.
	for i := 0; i < 50; i++ {
		l.send(mss)
		l.onAck(now, 5*time.Second, mss)
	}
	minCwnd := ledbatMinCwndMSS * mss
	if l.cwndBytes() < minCwnd {
		t.Fatalf("cwndBytes() = %d, want at least MIN_CWND*MSS = %d", l.cwndBytes(), minCwnd)
	}
}

func TestOnLossHalvesCwnd(t *testing.T) {
	l := newLedbat()
	l.cwnd = 10 * mss
	l.onLoss()
	want := 5 * mss
	if l.cwndBytes() != want {
		t.Fatalf("cwndBytes() = %d, want %d (halved)", l.cwndBytes(), want)
	}
}

func TestOnLossNeverDropsBelowMinCwnd(t *testing.T) {
	l := newLedbat()
	l.cwnd = ledbatMinCwndMSS * mss // already at the floor
	l.onLoss()
	minCwnd := ledbatMinCwndMSS * mss
	if l.cwndBytes() != minCwnd {
		t.Fatalf("cwndBytes() = %d, want it to stay at MIN_CWND*MSS = %d, not go below", l.cwndBytes(), minCwnd)
	}
}

func TestOnTimeoutResetsCwndToOneMSS(t *testing.T) {
	l := newLedbat()
	l.cwnd = 20 * mss
	l.onTimeout()
	if l.cwndBytes() != mss {
		t.Fatalf("cwndBytes() = %d, want exactly 1*MSS = %d", l.cwndBytes(), mss)
	}
}

func TestAvailableWindowReflectsFlightSize(t *testing.T) {
	l := newLedbat()
	full := l.availableWindow()
	if full != l.cwndBytes() {
		t.Fatalf("availableWindow() = %d, want the full cwnd %d before anything is in flight", full, l.cwndBytes())
	}
	l.send(mss)
	if got := l.availableWindow(); got != full-mss {
		t.Fatalf("availableWindow() after sending 1 MSS = %d, want %d", got, full-mss)
	}
}

func TestAvailableWindowNeverGoesNegative(t *testing.T) {
	l := newLedbat()
	l.send(l.cwndBytes() * 10) // deliberately over-commit
	if got := l.availableWindow(); got != 0 {
		t.Fatalf("availableWindow() = %d, want 0 when flight already exceeds cwnd", got)
	}
}
