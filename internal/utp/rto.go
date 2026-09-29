package utp

import "time"

// RFC 6298's own standard smoothing constants — BEP 29 explicitly defers
// to this RFC for retransmission-timeout management rather than defining
// its own.
const (
	rtoAlpha = 0.125 // 1/8 - SRTT's own weight on a fresh sample
	rtoBeta  = 0.25  // 1/4 - RTTVAR's own weight on a fresh deviation
	rtoK     = 4     // RTO = SRTT + K*RTTVAR
)

// minRTO/maxRTO bound the computed timeout. RFC 6298 itself mandates a
// 1-second floor, written for ordinary TCP traffic; µTP's whole reason to
// exist is *low* latency, and real implementations commonly allow
// sub-second RTOs rather than inheriting TCP's own floor verbatim — a
// deliberate, documented deviation, not an oversight. maxRTO is a sane
// ceiling so a connection with no working path at all backs off but
// doesn't grow its retransmit interval unboundedly.
const (
	minRTO = 200 * time.Millisecond
	maxRTO = 60 * time.Second
	// initialRTO is used before any real RTT sample exists — deliberately
	// generous, since guessing too short just means one wasted early
	// retransmit before the first real sample corrects it.
	initialRTO = 1 * time.Second
)

// rtoEstimator tracks SRTT/RTTVAR (RFC 6298) and the resulting RTO for one
// connection. Not safe for concurrent use — always called from the owning
// Conn's own single actor goroutine, the same "no separate lock needed"
// shape internal/torrent's actor state already relies on throughout.
type rtoEstimator struct {
	hasSample bool
	srtt      time.Duration
	rttvar    time.Duration
	rto       time.Duration
}

func newRTOEstimator() *rtoEstimator {
	return &rtoEstimator{rto: initialRTO}
}

// sample folds in one real RTT measurement — the caller is responsible for
// Karn's algorithm (never call this for an ACK that might correspond to a
// retransmitted packet, since which transmission it actually acknowledges
// is ambiguous).
func (e *rtoEstimator) sample(rtt time.Duration) {
	if rtt < 0 {
		return
	}
	if !e.hasSample {
		e.srtt = rtt
		e.rttvar = rtt / 2
		e.hasSample = true
	} else {
		diff := e.srtt - rtt
		if diff < 0 {
			diff = -diff
		}
		e.rttvar = time.Duration((1-rtoBeta)*float64(e.rttvar) + rtoBeta*float64(diff))
		e.srtt = time.Duration((1-rtoAlpha)*float64(e.srtt) + rtoAlpha*float64(rtt))
	}
	e.rto = clampRTO(e.srtt + rtoK*e.rttvar)
}

// timeout is called when a retransmit timer actually fires with nothing
// acknowledged — RFC 6298's own backoff: double the *current* RTO
// directly, leaving SRTT/RTTVAR untouched until a fresh, unambiguous
// sample arrives (a doubled RTO computed straight from possibly-stale
// SRTT/RTTVAR would just immediately halve back down on the very next
// sample, defeating the point of backing off at all).
func (e *rtoEstimator) timeout() {
	e.rto = clampRTO(e.rto * 2)
}

// current returns the RTO to arm the next retransmit timer with.
func (e *rtoEstimator) current() time.Duration {
	return e.rto
}

func clampRTO(d time.Duration) time.Duration {
	if d < minRTO {
		return minRTO
	}
	if d > maxRTO {
		return maxRTO
	}
	return d
}
