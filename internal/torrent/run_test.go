package torrent

import (
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

// TestAdaptPipelineStartsConservative checks the first call (no throughput
// history yet) sets the floor rather than guessing high.
func TestAdaptPipelineStartsConservative(t *testing.T) {
	pc := &peerConn{}
	now := time.Now()
	adaptPipeline(pc, now)

	if pc.pipelineTarget != minPipeline {
		t.Fatalf("pipelineTarget = %d, want %d (the floor) on the first call", pc.pipelineTarget, minPipeline)
	}
}

// TestAdaptPipelineTracksThroughput checks that the target scales linearly
// with measured rate — a peer ten times faster than another gets a target
// ten times larger — well clear of the floor so clamping cannot mask a
// broken formula.
func TestAdaptPipelineTracksThroughput(t *testing.T) {
	// Chosen so the resulting target (rate * pipelineWindow / BlockLength)
	// is comfortably above minPipeline: 5 blocks/sec of "target rate".
	baseRate := 5 * float64(picker.BlockLength) / pipelineWindow.Seconds()

	slow := &peerConn{}
	start := time.Now()
	adaptPipeline(slow, start) // seed lastAdaptTime/lastAdaptBytes
	slow.downloaded.Store(int64(baseRate * pipelineAdaptInterval.Seconds()))
	adaptPipeline(slow, start.Add(pipelineAdaptInterval))

	if slow.pipelineTarget != 5 {
		t.Fatalf("pipelineTarget = %d, want 5 for the base rate", slow.pipelineTarget)
	}

	fast := &peerConn{}
	adaptPipeline(fast, start)
	fast.downloaded.Store(int64(baseRate * 10 * pipelineAdaptInterval.Seconds()))
	adaptPipeline(fast, start.Add(pipelineAdaptInterval))

	if fast.pipelineTarget != 50 {
		t.Fatalf("pipelineTarget = %d, want 50 (10x the base rate's target)", fast.pipelineTarget)
	}
}

// TestAdaptPipelineClampsToHardCeiling ensures an implausibly high measured
// rate cannot grow the target past what the WorkQueue channel actually
// holds.
func TestAdaptPipelineClampsToHardCeiling(t *testing.T) {
	pc := &peerConn{}
	start := time.Now()
	adaptPipeline(pc, start)

	pc.downloaded.Store(1 << 40) // an absurd one-terabyte-per-second peer
	adaptPipeline(pc, start.Add(pipelineAdaptInterval))

	if pc.pipelineTarget > peer.MaxPipelineSize {
		t.Fatalf("pipelineTarget = %d, want <= %d (the hard ceiling)", pc.pipelineTarget, peer.MaxPipelineSize)
	}
}

// TestAdaptPipelineIgnoresSubIntervalTicks confirms recomputation is
// throttled to pipelineAdaptInterval so a single fast block does not cause
// the target to chase noise on every 100ms tick.
func TestAdaptPipelineIgnoresSubIntervalTicks(t *testing.T) {
	pc := &peerConn{}
	start := time.Now()
	adaptPipeline(pc, start)
	pc.pipelineTarget = 42 // sentinel: a real recompute would overwrite this

	pc.downloaded.Store(1 << 20)
	adaptPipeline(pc, start.Add(pipelineAdaptInterval/2))

	if pc.pipelineTarget != 42 {
		t.Fatalf("pipelineTarget changed to %d before pipelineAdaptInterval elapsed", pc.pipelineTarget)
	}
}
