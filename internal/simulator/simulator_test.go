package simulator

import (
	"strconv"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/picker"
)

// baseConfig is a small, fast-converging swarm shared by the tests below —
// short tick/choke intervals so a modest torrent finishes in a handful of
// virtual seconds, keeping the tests themselves fast without touching real
// wall-clock time at all.
func baseConfig(seed int64) Config {
	return Config{
		TotalLength:   2 << 20, // 2 MiB
		PieceLength:   64 << 10,
		MaxPeers:      30,
		TickInterval:  200 * time.Millisecond,
		ChokeInterval: 2 * time.Second,
		MaxDuration:   10 * time.Minute,
		Seed:          seed,
	}
}

// TestOneSeederOneLeecherCompletes is the simplest possible real proof
// this package works at all: a single leecher with one seeder as its only
// peer must finish downloading everything.
func TestOneSeederOneLeecherCompletes(t *testing.T) {
	sw, err := NewSwarm(baseConfig(1))
	if err != nil {
		t.Fatalf("NewSwarm: %v", err)
	}
	if err := sw.AddNode(NodeConfig{ID: "seed", Seed: true}); err != nil {
		t.Fatalf("AddNode(seed): %v", err)
	}
	if err := sw.AddNode(NodeConfig{ID: "leech"}); err != nil {
		t.Fatalf("AddNode(leech): %v", err)
	}

	result := sw.Run()
	if !result.Completed {
		t.Fatalf("swarm did not complete within MaxDuration (elapsed %s)", result.Elapsed)
	}

	var leech *NodeResult
	for i := range result.Nodes {
		if result.Nodes[i].ID == "leech" {
			leech = &result.Nodes[i]
		}
	}
	if leech == nil {
		t.Fatal("no result for the leecher")
	}
	if leech.CompletedAt == nil {
		t.Fatal("leecher never completed")
	}
	if leech.BytesDown < 2<<20 {
		t.Fatalf("leecher only received %d bytes, want at least the full 2 MiB", leech.BytesDown)
	}
}

// TestSameSeedProducesIdenticalResults is this package's whole reason for
// existing: the same Config and Seed must replay to the exact same
// outcome, byte for byte — not "close," identical — so a swarm run is a
// real regression test, not a flaky one.
func TestSameSeedProducesIdenticalResults(t *testing.T) {
	build := func() Result {
		sw, err := NewSwarm(baseConfig(42))
		if err != nil {
			t.Fatalf("NewSwarm: %v", err)
		}
		mustAddSwarm(t, sw, 1, 6, NetworkParams{Latency: 30 * time.Millisecond, LossRate: 0.02})
		return sw.Run()
	}

	a := build()
	b := build()

	if !a.Completed || !b.Completed {
		t.Fatalf("expected both runs to complete: a=%v b=%v", a.Completed, b.Completed)
	}
	if a.Elapsed != b.Elapsed {
		t.Fatalf("Elapsed differs across identical runs: %s vs %s", a.Elapsed, b.Elapsed)
	}
	if len(a.Nodes) != len(b.Nodes) {
		t.Fatalf("node count differs: %d vs %d", len(a.Nodes), len(b.Nodes))
	}
	for i := range a.Nodes {
		na, nb := a.Nodes[i], b.Nodes[i]
		if na.ID != nb.ID || na.BytesUp != nb.BytesUp || na.BytesDown != nb.BytesDown {
			t.Fatalf("node %d diverged: a=%+v b=%+v", i, na, nb)
		}
		if (na.CompletedAt == nil) != (nb.CompletedAt == nil) {
			t.Fatalf("node %d completion presence diverged: a=%v b=%v", i, na.CompletedAt, nb.CompletedAt)
		}
		if na.CompletedAt != nil && *na.CompletedAt != *nb.CompletedAt {
			t.Fatalf("node %d CompletedAt diverged: %s vs %s", i, *na.CompletedAt, *nb.CompletedAt)
		}
	}
}

// mustAddSwarm adds one seeder and n leechers with the given network
// params, failing the test on any error.
func mustAddSwarm(t *testing.T, sw *Swarm, seeds, leechers int, net NetworkParams) {
	t.Helper()
	sw.cfg.Network = net.withDefaults()
	for i := 0; i < seeds; i++ {
		if err := sw.AddNode(NodeConfig{ID: seedID(i), Seed: true}); err != nil {
			t.Fatalf("AddNode(%s): %v", seedID(i), err)
		}
	}
	for i := 0; i < leechers; i++ {
		if err := sw.AddNode(NodeConfig{ID: leechID(i)}); err != nil {
			t.Fatalf("AddNode(%s): %v", leechID(i), err)
		}
	}
}

func seedID(i int) string  { return "seed" + strconv.Itoa(i) }
func leechID(i int) string { return "leech" + strconv.Itoa(i) }

// TestRarestFirstBeatsSequentialUnderScarcity is the real payoff this
// whole package exists for: proving, not just asserting in a doc comment,
// that rarest-first genuinely outperforms sequential when a single
// bandwidth-limited seeder is the only initial source and several
// leechers must eventually trade with each other to finish quickly.
// Sequential makes every leecher want the exact same early pieces first,
// so nobody has anything unique to offer anyone else until the seeder has
// serially served enough of piece 0 to everyone — the classic, textbook
// reason rarest-first is the real default. Both runs share an identical
// topology (built before any strategy-dependent picking happens, so the
// only Config difference between them is each leecher's Strategy).
func TestRarestFirstBeatsSequentialUnderScarcity(t *testing.T) {
	const numLeechers = 6
	net := NetworkParams{Latency: 20 * time.Millisecond}

	run := func(strategy picker.Strategy) Result {
		cfg := baseConfig(7)
		cfg.Network = net
		sw, err := NewSwarm(cfg)
		if err != nil {
			t.Fatalf("NewSwarm: %v", err)
		}
		if err := sw.AddNode(NodeConfig{ID: "seed", Seed: true, UploadRate: 64 << 10}); err != nil {
			t.Fatalf("AddNode(seed): %v", err)
		}
		for i := 0; i < numLeechers; i++ {
			if err := sw.AddNode(NodeConfig{ID: leechID(i), Strategy: strategy}); err != nil {
				t.Fatalf("AddNode(%s): %v", leechID(i), err)
			}
		}
		return sw.Run()
	}

	rarest := run(picker.RarestFirst)
	sequential := run(picker.Sequential)

	if !rarest.Completed {
		t.Fatalf("rarest-first swarm did not complete (elapsed %s)", rarest.Elapsed)
	}
	if !sequential.Completed {
		t.Fatalf("sequential swarm did not complete (elapsed %s)", sequential.Elapsed)
	}

	rm, sm := rarest.Makespan(), sequential.Makespan()
	if rm >= sm {
		t.Fatalf("expected rarest-first makespan (%s) to beat sequential's (%s) under a scarce single seeder", rm, sm)
	}
	t.Logf("makespan: rarest-first=%s sequential=%s (%.0f%% faster)", rm, sm, 100*(1-float64(rm)/float64(sm)))
}

// TestLossyNetworkStillCompletes proves internal/picker's real
// RequestTimeout/Expire recovery — reused unmodified here, not
// reimplemented — actually works against a lossy link: a meaningful
// fraction of every request/response round trip simply never arrives.
func TestLossyNetworkStillCompletes(t *testing.T) {
	cfg := baseConfig(9)
	cfg.MaxDuration = 30 * time.Minute
	sw, err := NewSwarm(cfg)
	if err != nil {
		t.Fatalf("NewSwarm: %v", err)
	}
	mustAddSwarm(t, sw, 1, 4, NetworkParams{Latency: 40 * time.Millisecond, LossRate: 0.15})

	result := sw.Run()
	if !result.Completed {
		t.Fatalf("swarm with 15%% loss did not complete within %s", cfg.MaxDuration)
	}
}

// TestChurnStillCompletes is the concurrency-made-testable claim this
// feature is for: leechers repeatedly disconnecting and reconnecting must
// not stall the swarm forever — a real regression test for exactly the
// class of bug (a torrent that silently never finishes) that has no other
// automated coverage anywhere in this project, since the real actor has
// no way to simulate churn deterministically at all.
func TestChurnStillCompletes(t *testing.T) {
	cfg := baseConfig(11)
	cfg.MaxDuration = 30 * time.Minute
	cfg.ChurnInterval = 3 * time.Second
	cfg.ChurnProbability = 0.3
	cfg.ChurnOfflineMin = 2 * time.Second
	cfg.ChurnOfflineMax = 8 * time.Second
	sw, err := NewSwarm(cfg)
	if err != nil {
		t.Fatalf("NewSwarm: %v", err)
	}
	if err := sw.AddNode(NodeConfig{ID: "seed", Seed: true}); err != nil {
		t.Fatalf("AddNode(seed): %v", err)
	}
	for i := 0; i < 8; i++ {
		if err := sw.AddNode(NodeConfig{ID: leechID(i), ChurnEligible: true}); err != nil {
			t.Fatalf("AddNode(%s): %v", leechID(i), err)
		}
	}

	result := sw.Run()
	if !result.Completed {
		t.Fatalf("churny swarm did not complete within %s", cfg.MaxDuration)
	}
}

// TestTwoHundredPeerSwarmRunsInMilliseconds is the literal claim
// ROADMAP.md makes for this feature: a 200-peer swarm, deterministic, and
// fast — measured here against real wall-clock time, not asserted in
// prose. A generous ceiling (a full second) leaves headroom for a slow CI
// runner while still failing loudly if the event loop's complexity ever
// regresses badly enough to matter.
func TestTwoHundredPeerSwarmRunsInMilliseconds(t *testing.T) {
	cfg := baseConfig(200)
	cfg.TotalLength = 8 << 20
	cfg.PieceLength = 256 << 10
	cfg.MaxDuration = time.Hour
	sw, err := NewSwarm(cfg)
	if err != nil {
		t.Fatalf("NewSwarm: %v", err)
	}
	mustAddSwarm(t, sw, 3, 197, NetworkParams{Latency: 25 * time.Millisecond})

	start := time.Now()
	result := sw.Run()
	wall := time.Since(start)

	if !result.Completed {
		t.Fatalf("200-peer swarm did not complete within %s (simulated)", cfg.MaxDuration)
	}
	if wall > time.Second {
		t.Fatalf("200-peer swarm took %s of real wall-clock time, want well under a second", wall)
	}
	t.Logf("200-peer swarm: %s simulated time, %s real wall-clock time", result.Elapsed, wall)
}
