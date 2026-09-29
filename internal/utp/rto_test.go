package utp

import (
	"testing"
	"time"
)

func TestRTOEstimatorStartsAtInitialRTO(t *testing.T) {
	e := newRTOEstimator()
	if e.current() != initialRTO {
		t.Fatalf("current() = %v, want the initial %v before any sample", e.current(), initialRTO)
	}
}

// TestRTOEstimatorFirstSample pins RFC 6298's own initialization formula
// (SRTT=R, RTTVAR=R/2, RTO=SRTT+4*RTTVAR) against hand-computed values,
// not just a round trip through the same arithmetic the production code
// itself performs.
func TestRTOEstimatorFirstSample(t *testing.T) {
	e := newRTOEstimator()
	e.sample(400 * time.Millisecond)
	if e.srtt != 400*time.Millisecond {
		t.Fatalf("srtt = %v, want 400ms (SRTT=R on the first sample)", e.srtt)
	}
	if e.rttvar != 200*time.Millisecond {
		t.Fatalf("rttvar = %v, want 200ms (RTTVAR=R/2 on the first sample)", e.rttvar)
	}
	// RTO = SRTT + 4*RTTVAR = 400ms + 4*200ms = 1200ms
	want := 1200 * time.Millisecond
	if e.current() != want {
		t.Fatalf("current() = %v, want %v", e.current(), want)
	}
}

// TestRTOEstimatorSecondSample pins the subsequent-sample smoothing
// formula against a hand-computed value.
func TestRTOEstimatorSecondSample(t *testing.T) {
	e := newRTOEstimator()
	e.sample(400 * time.Millisecond) // srtt=400ms, rttvar=200ms
	e.sample(300 * time.Millisecond)
	// RTTVAR = (1-1/4)*200ms + 1/4*|400ms-300ms| = 150ms + 25ms = 175ms
	// SRTT   = (1-1/8)*400ms + 1/8*300ms = 350ms + 37.5ms = 387.5ms
	wantRTTVAR := 175 * time.Millisecond
	wantSRTT := 387500 * time.Microsecond
	if diff := e.rttvar - wantRTTVAR; diff > time.Microsecond || diff < -time.Microsecond {
		t.Fatalf("rttvar = %v, want %v", e.rttvar, wantRTTVAR)
	}
	if diff := e.srtt - wantSRTT; diff > time.Microsecond || diff < -time.Microsecond {
		t.Fatalf("srtt = %v, want %v", e.srtt, wantSRTT)
	}
	// RTO = SRTT + 4*RTTVAR = 387.5ms + 700ms = 1087.5ms
	wantRTO := wantSRTT + 4*wantRTTVAR
	if diff := e.current() - wantRTO; diff > time.Microsecond || diff < -time.Microsecond {
		t.Fatalf("current() = %v, want %v", e.current(), wantRTO)
	}
}

func TestRTOEstimatorTimeoutDoublesWithoutTouchingSRTT(t *testing.T) {
	e := newRTOEstimator()
	e.sample(400 * time.Millisecond)
	before := e.current()
	srttBefore, rttvarBefore := e.srtt, e.rttvar

	e.timeout()

	if e.current() != before*2 {
		t.Fatalf("current() = %v, want exactly double %v", e.current(), before)
	}
	if e.srtt != srttBefore || e.rttvar != rttvarBefore {
		t.Fatal("timeout() must not touch SRTT/RTTVAR - only a fresh, unambiguous sample should")
	}
}

func TestRTOEstimatorClampsToMinRTO(t *testing.T) {
	e := newRTOEstimator()
	// A very small, very stable RTT would otherwise compute an RTO well
	// under minRTO.
	e.sample(1 * time.Millisecond)
	e.sample(1 * time.Millisecond)
	e.sample(1 * time.Millisecond)
	if e.current() < minRTO {
		t.Fatalf("current() = %v, want at least the floor %v", e.current(), minRTO)
	}
}

func TestRTOEstimatorClampsToMaxRTO(t *testing.T) {
	e := newRTOEstimator()
	e.sample(30 * time.Second)
	for i := 0; i < 10; i++ {
		e.timeout()
	}
	if e.current() > maxRTO {
		t.Fatalf("current() = %v, want at most the ceiling %v", e.current(), maxRTO)
	}
}

func TestRTOEstimatorIgnoresNegativeSamples(t *testing.T) {
	e := newRTOEstimator()
	e.sample(-1 * time.Second) // a caller bug (e.g. clock skew) must not poison the estimator
	if e.hasSample {
		t.Fatal("a negative RTT sample must be rejected, not accepted as real data")
	}
}
