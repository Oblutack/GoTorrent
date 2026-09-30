package tui

import "time"

// The control API reports cumulative byte counters, not speeds, so every
// speed this UI shows is derived client-side from two successive readings -
// the same approach Desktop's own MainViewModel takes for its speed graph
// and per-peer rates.

const (
	// minRateInterval is the shortest gap between two readings that still
	// produces a rate. Refreshes arrive from both the poll timer and live
	// events, sometimes milliseconds apart; dividing a tiny byte delta by a
	// tiny elapsed time is pure noise, so such readings are skipped (the
	// baseline is not advanced, so the bytes still count next time).
	minRateInterval = 400 * time.Millisecond

	// rateSmoothing is the weight of the newest sample in the moving average
	// (1 = no smoothing). Raw per-interval rates jump around; this keeps the
	// number readable without lagging far behind a real change.
	rateSmoothing = 0.5

	// histLen is how many speed samples the header sparklines remember.
	histLen = 60
)

// rateState turns cumulative down/up counters into smoothed bytes/sec.
type rateState struct {
	seen             bool
	hasRate          bool
	lastDown, lastUp int64
	lastAt           time.Time
	down, up         float64
}

// observe folds one reading in and reports whether it produced a new rate.
func (r *rateState) observe(down, up int64, at time.Time) bool {
	if !r.seen || down < r.lastDown || up < r.lastUp {
		// First reading, or the counters went backwards (a restarted
		// engine): start over from here rather than show a negative rate.
		*r = rateState{seen: true, lastDown: down, lastUp: up, lastAt: at}
		return false
	}
	dt := at.Sub(r.lastAt)
	if dt < minRateInterval {
		return false
	}
	secs := dt.Seconds()
	instDown := float64(down-r.lastDown) / secs
	instUp := float64(up-r.lastUp) / secs
	if r.hasRate {
		r.down = rateSmoothing*instDown + (1-rateSmoothing)*r.down
		r.up = rateSmoothing*instUp + (1-rateSmoothing)*r.up
	} else {
		r.down, r.up = instDown, instUp
		r.hasRate = true
	}
	r.lastDown, r.lastUp, r.lastAt = down, up, at
	return true
}

// speedHistory is a rateState that also remembers its recent samples, for
// the header sparklines.
type speedHistory struct {
	rateState
	downHist, upHist []float64
}

func (s *speedHistory) observe(down, up int64, at time.Time) {
	if !s.rateState.observe(down, up, at) {
		return
	}
	s.downHist = appendCapped(s.downHist, s.down, histLen)
	s.upHist = appendCapped(s.upHist, s.up, histLen)
}

func appendCapped(vals []float64, v float64, limit int) []float64 {
	vals = append(vals, v)
	if len(vals) > limit {
		vals = vals[len(vals)-limit:]
	}
	return vals
}
