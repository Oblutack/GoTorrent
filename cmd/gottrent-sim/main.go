// Command gottrent-sim runs a configurable, deterministic swarm scenario
// through internal/simulator and prints a summary — a small, real way to
// see the deterministic swarm simulator's own headline claim for
// yourself: a 200-peer swarm, reproducible bit for bit given the same
// -seed, finishing in real milliseconds of wall-clock time.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/Oblutack/GoTorrent/internal/picker"
	"github.com/Oblutack/GoTorrent/internal/simulator"
)

func main() {
	var (
		seeds       = flag.Int("seeds", 1, "number of seeders")
		leechers    = flag.Int("leechers", 20, "number of leechers")
		totalLength = flag.Int64("size", 8<<20, "torrent size in bytes")
		pieceLength = flag.Int64("piece-length", 256<<10, "piece length in bytes")
		latency     = flag.Duration("latency", 30*time.Millisecond, "one-way network latency")
		lossRate    = flag.Float64("loss", 0, "probability, 0..1, that a request/response round trip is dropped")
		seedUpRate  = flag.Int64("seed-up-rate", 0, "seeder upload rate in bytes/sec, 0 = unbounded")
		leechUpRate = flag.Int64("leech-up-rate", 0, "leecher upload rate in bytes/sec, 0 = unbounded")
		sequential  = flag.Bool("sequential", false, "use sequential piece order instead of rarest-first")
		churnProb   = flag.Float64("churn-prob", 0, "probability per churn interval that an eligible leecher disconnects, 0 disables churn")
		churnEvery  = flag.Duration("churn-interval", 30*time.Second, "how often the churn check runs")
		maxDuration = flag.Duration("max-duration", time.Hour, "hard cap on simulated time")
		rngSeed     = flag.Int64("seed", 1, "RNG seed — the same seed and flags always reproduce the exact same result")
		verbose     = flag.Bool("v", false, "print a per-node table, not just the summary")
	)
	flag.Parse()

	strategy := picker.RarestFirst
	if *sequential {
		strategy = picker.Sequential
	}

	cfg := simulator.Config{
		TotalLength: *totalLength,
		PieceLength: *pieceLength,
		Network: simulator.NetworkParams{
			Latency:  *latency,
			LossRate: *lossRate,
		},
		ChurnProbability: *churnProb,
		ChurnInterval:    *churnEvery,
		MaxDuration:      *maxDuration,
		Seed:             *rngSeed,
	}

	sw, err := simulator.NewSwarm(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gottrent-sim:", err)
		os.Exit(1)
	}

	for i := 0; i < *seeds; i++ {
		id := fmt.Sprintf("seed%d", i)
		if err := sw.AddNode(simulator.NodeConfig{ID: id, Seed: true, UploadRate: *seedUpRate}); err != nil {
			fmt.Fprintln(os.Stderr, "gottrent-sim:", err)
			os.Exit(1)
		}
	}
	for i := 0; i < *leechers; i++ {
		id := fmt.Sprintf("leech%d", i)
		nc := simulator.NodeConfig{ID: id, Strategy: strategy, UploadRate: *leechUpRate, ChurnEligible: *churnProb > 0}
		if err := sw.AddNode(nc); err != nil {
			fmt.Fprintln(os.Stderr, "gottrent-sim:", err)
			os.Exit(1)
		}
	}

	start := time.Now()
	result := sw.Run()
	wall := time.Since(start)

	fmt.Printf("%d seeders, %d leechers, %s torrent, %s pieces, strategy=%s\n",
		*seeds, *leechers, humanBytes(*totalLength), humanBytes(*pieceLength), strategy)
	fmt.Printf("completed=%v  simulated=%s  wall-clock=%s\n", result.Completed, result.Elapsed, wall)
	fmt.Printf("makespan (slowest leecher, from its own join time)=%s\n", result.Makespan())

	if *verbose {
		printTable(result)
	}
}

func printTable(result simulator.Result) {
	rows := append([]simulator.NodeResult(nil), result.Nodes...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })

	fmt.Printf("\n%-10s %-6s %-10s %-10s %-10s %-12s %s\n", "ID", "SEED", "UP", "DOWN", "CHOKED", "UNCHOKED", "DONE")
	for _, n := range rows {
		done := "-"
		if n.CompletedAt != nil {
			done = n.CompletedAt.String()
		}
		fmt.Printf("%-10s %-6v %-10s %-10s %-10d %-12d %s\n",
			n.ID, n.Seed, humanBytes(n.BytesUp), humanBytes(n.BytesDown), n.TimesChoked, n.TimesUnchoked, done)
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
