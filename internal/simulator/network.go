package simulator

import (
	"math/rand"
	"time"
)

// NetworkParams configures the in-memory network every link in a Swarm
// shares, unless a per-node override says otherwise (see Node's own
// UploadRate/DownloadRate). This is deliberately a single flat model, not
// a per-pair topology of distinct latencies — real internet path diversity
// is not the point here; making bandwidth/latency/loss/churn a dial a test
// can turn is.
type NetworkParams struct {
	// Latency is the one-way delay applied to every message on the wire —
	// a control message (Bitfield/Have/Interested/Choke/Request) as well
	// as the head of a block transfer. Symmetric in both directions.
	Latency time.Duration

	// LossRate is the probability, in [0,1], that any single message
	// (control or a block) never arrives at all. Modeled the same way a
	// real dropped TCP segment eventually looks to this client: nothing
	// arrives, and internal/picker's own real RequestTimeout/Expire
	// mechanism is what recovers — reused here unmodified, not
	// reimplemented, since simulating loss recovery any other way would
	// stop testing the real code path.
	LossRate float64
}

// withDefaults fills in a workable network when the caller leaves
// everything zero, the same "a zero Config still does something sane"
// convention picker.Config/choker's defaults already follow.
func (n NetworkParams) withDefaults() NetworkParams {
	if n.Latency <= 0 {
		n.Latency = 50 * time.Millisecond
	}
	return n
}

// transferTime is how long it takes to move length bytes at ratePerSec
// bytes/sec — the model's only concession to bandwidth being scarce.
// ratePerSec <= 0 means "unbounded," which resolves to zero transfer time
// rather than dividing by zero — the same "0 means unlimited" convention
// internal/ratelimit's own Limiter already uses for a caller that never
// configured a cap.
func transferTime(length int, ratePerSec int64) time.Duration {
	if ratePerSec <= 0 || length <= 0 {
		return 0
	}
	seconds := float64(length) / float64(ratePerSec)
	return time.Duration(seconds * float64(time.Second))
}

// lost consults the shared RNG to decide whether one message on this link
// is dropped this time — a single Bernoulli trial per message, not a
// stateful loss-burst model (real bursty loss is a further refinement this
// v1 deliberately does not attempt).
func (n NetworkParams) lost(rng *rand.Rand) bool {
	if n.LossRate <= 0 {
		return false
	}
	return rng.Float64() < n.LossRate
}
