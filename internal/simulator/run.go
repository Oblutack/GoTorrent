package simulator

import "time"

// Result is what a Swarm.Run produces: the whole run's outcome, in enough
// detail to write a real regression assertion against ("rarest-first
// finishes faster than sequential under these conditions") rather than
// just eyeballing log output.
type Result struct {
	// Elapsed is how much virtual time the run actually covered — either
	// the moment every node completed (StopWhenAllComplete) or
	// Config.MaxDuration, whichever ended the run.
	Elapsed time.Duration
	// Completed is false if MaxDuration was hit before every node
	// finished — a real, reportable outcome in its own right (the swarm
	// stalled, or churn never let everyone back online at once), not a
	// failure of the simulator.
	Completed bool
	Nodes     []NodeResult
}

// NodeResult is one participant's outcome.
type NodeResult struct {
	ID                         string
	Seed                       bool
	JoinedAt                   time.Duration
	CompletedAt                *time.Duration // nil if this node never finished within the run
	BytesUp                    int64
	BytesDown                  int64
	TimesChoked, TimesUnchoked int
}

// Makespan is how long the slowest node that did finish took, measured
// from its own JoinedAt — 0 if nothing finished. A swarm where nodes join
// at different times cares about how long each one personally waited, not
// wall-clock-since-simulation-start.
func (r Result) Makespan() time.Duration {
	var worst time.Duration
	for _, n := range r.Nodes {
		if n.CompletedAt == nil {
			continue
		}
		d := *n.CompletedAt - n.JoinedAt
		if d > worst {
			worst = d
		}
	}
	return worst
}

// Run drives the simulation forward until every node has completed (when
// Config.StopWhenAllComplete, the default) or Config.MaxDuration is
// reached, whichever comes first — a single-threaded loop popping the
// next scheduled event and jumping straight to it, which is the entire
// reason a swarm that would take real hours to download finishes in real
// milliseconds here: nothing ever waits on anything, time only ever
// advances by processing the next cause-and-effect step.
func (s *Swarm) Run() Result {
	stopWhenComplete := s.cfg.StopWhenAllComplete == nil || *s.cfg.StopWhenAllComplete
	for s.clock.Elapsed() < s.cfg.MaxDuration {
		if stopWhenComplete && s.pending <= 0 {
			break
		}
		if !s.clock.step() {
			break // nothing left scheduled — every tick loop stopped (should not happen while any node is online, but a genuinely empty swarm has nothing to run at all)
		}
	}
	return s.result()
}

func (s *Swarm) result() Result {
	nodes := make([]NodeResult, 0, len(s.order))
	allDone := true
	for _, id := range s.order {
		n := s.nodes[id]
		nodes = append(nodes, NodeResult{
			ID:            n.id,
			Seed:          n.seed,
			JoinedAt:      n.joinedAt,
			CompletedAt:   n.completedAt,
			BytesUp:       n.bytesUp,
			BytesDown:     n.bytesDown,
			TimesChoked:   n.timesChoked,
			TimesUnchoked: n.timesUnchoked,
		})
		if !n.seed && n.completedAt == nil {
			allDone = false
		}
	}
	return Result{
		Elapsed:   s.clock.Elapsed(),
		Completed: allDone,
		Nodes:     nodes,
	}
}
