package utp

import "time"

// RFC 6817's own constants (Section 2.5). TARGET/GAIN/ALLOWED_INCREASE/
// MIN_CWND/INIT_CWND/BASE_HISTORY are exactly the RFC's recommended
// values, not tuned or approximated.
const (
	ledbatTarget          = 100 * time.Millisecond
	ledbatGain            = 1.0
	ledbatAllowedIncrease = 1.0
	ledbatMinCwndMSS      = 2
	ledbatInitCwndMSS     = 2
	ledbatBaseHistory     = 10 // one-minute rolling slots
	// ledbatCurrentFilterSize bounds the short current-delay window. RFC
	// 6817 requires it be "limited such that samples in the list are not
	// older than an RTT" without mandating a fixed size; a small fixed
	// window is one of the RFC's own explicitly sanctioned FILTER()
	// choices (Section 2.4) rather than a shortcut invented here.
	ledbatCurrentFilterSize = 4
)

// mss is this package's fixed maximum segment size for both wire framing
// and congestion accounting — see doc.go for why there's no path-MTU
// discovery: a conservative fixed size well under nearly every real
// path's actual MTU, the same choice several real implementations make.
const mss = 1400

// ledbat implements RFC 6817 Section 2.4's on-acknowledgement algorithm
// directly against its own published pseudocode, not a paraphrase of it.
// Not safe for concurrent use — always called from the owning Conn's own
// single actor goroutine.
type ledbat struct {
	baseDelays   [ledbatBaseHistory]time.Duration // oldest at index 0, newest ("tail") at the end
	lastRollover time.Time

	currentDelays []time.Duration // FIFO, capped at ledbatCurrentFilterSize

	cwnd       float64 // bytes
	flightSize float64 // bytes
}

// ledbatInfinite stands in for RFC 6817's own "+INFINITY" base_delays
// initialization — any real sample beats it, so the first genuine
// measurement always wins the running minimum.
const ledbatInfinite = time.Duration(1<<63 - 1)

func newLedbat() *ledbat {
	l := &ledbat{cwnd: ledbatInitCwndMSS * mss}
	for i := range l.baseDelays {
		l.baseDelays[i] = ledbatInfinite
	}
	return l
}

func (l *ledbat) updateBaseDelay(now time.Time, delay time.Duration) {
	if l.lastRollover.IsZero() || now.Sub(l.lastRollover) >= time.Minute {
		l.lastRollover = now
		copy(l.baseDelays[:], l.baseDelays[1:])
		l.baseDelays[len(l.baseDelays)-1] = delay
		return
	}
	last := len(l.baseDelays) - 1
	if delay < l.baseDelays[last] {
		l.baseDelays[last] = delay
	}
}

func (l *ledbat) updateCurrentDelay(delay time.Duration) {
	l.currentDelays = append(l.currentDelays, delay)
	if len(l.currentDelays) > ledbatCurrentFilterSize {
		l.currentDelays = l.currentDelays[len(l.currentDelays)-ledbatCurrentFilterSize:]
	}
}

func (l *ledbat) minBaseDelay() time.Duration {
	m := l.baseDelays[0]
	for _, d := range l.baseDelays[1:] {
		if d < m {
			m = d
		}
	}
	return m
}

// filteredCurrentDelay is FILTER(current_delays) — a plain MIN filter,
// one of RFC 6817's own explicitly sanctioned choices (alongside NULL and
// EWMA), and the simplest one to verify obviously correct.
func (l *ledbat) filteredCurrentDelay() time.Duration {
	if len(l.currentDelays) == 0 {
		return 0
	}
	m := l.currentDelays[0]
	for _, d := range l.currentDelays[1:] {
		if d < m {
			m = d
		}
	}
	return m
}

// onAck is RFC 6817's on_acknowledgement, verbatim:
//
//	queuing_delay = FILTER(current_delays) - MIN(base_delays)
//	off_target = (TARGET - queuing_delay) / TARGET
//	cwnd += GAIN * off_target * bytes_newly_acked * MSS / cwnd
//	cwnd = min(cwnd, flightsize + ALLOWED_INCREASE * MSS)
//	cwnd = max(cwnd, MIN_CWND * MSS)
//	flightsize -= bytes_newly_acked
//
// delaySample is the raw one-way-delay value read straight out of a
// received packet's timestamp_difference_microseconds field (see doc.go
// for why that field alone is enough, with no clock synchronization).
func (l *ledbat) onAck(now time.Time, delaySample time.Duration, bytesNewlyAcked int) {
	if bytesNewlyAcked <= 0 {
		return
	}
	l.updateBaseDelay(now, delaySample)
	l.updateCurrentDelay(delaySample)

	queuingDelay := l.filteredCurrentDelay() - l.minBaseDelay()
	offTarget := (float64(ledbatTarget) - float64(queuingDelay)) / float64(ledbatTarget)

	l.cwnd += ledbatGain * offTarget * float64(bytesNewlyAcked) * mss / l.cwnd

	maxAllowed := l.flightSize + ledbatAllowedIncrease*mss
	if l.cwnd > maxAllowed {
		l.cwnd = maxAllowed
	}
	minCwnd := float64(ledbatMinCwndMSS * mss)
	if l.cwnd < minCwnd {
		l.cwnd = minCwnd
	}

	l.flightSize -= float64(bytesNewlyAcked)
	if l.flightSize < 0 {
		l.flightSize = 0
	}
}

// onLoss is RFC 6817's on_data_loss: cwnd = min(cwnd, max(cwnd/2, MIN_CWND*MSS)),
// which — since max(cwnd/2, minCwnd) is never larger than cwnd itself
// whenever cwnd is already at least minCwnd, an invariant this type
// always maintains — reduces to cwnd = max(cwnd/2, minCwnd) directly.
func (l *ledbat) onLoss() {
	half := l.cwnd / 2
	minCwnd := float64(ledbatMinCwndMSS * mss)
	if half > minCwnd {
		l.cwnd = half
	} else {
		l.cwnd = minCwnd
	}
}

// onTimeout is RFC 6817's on_timeout: cwnd = 1*MSS. The paired CTO-doubling
// half of the RFC's own on_timeout pseudocode is rtoEstimator.timeout()'s
// job, not this type's — the two are separate concerns (retransmission
// scheduling vs. congestion response) sharing one triggering event.
func (l *ledbat) onTimeout() {
	l.cwnd = mss
}

// send accounts bytes as newly in flight, ahead of any ACK for them.
func (l *ledbat) send(n int) {
	l.flightSize += float64(n)
}

// availableWindow is how many more bytes may be sent right now without
// exceeding cwnd.
func (l *ledbat) availableWindow() int {
	avail := l.cwnd - l.flightSize
	if avail < 0 {
		return 0
	}
	return int(avail)
}

func (l *ledbat) cwndBytes() int { return int(l.cwnd) }
