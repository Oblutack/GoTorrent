package simulator

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bitfield"
	"github.com/Oblutack/GoTorrent/internal/choker"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

// Config configures a Swarm.
type Config struct {
	// TotalLength and PieceLength describe the synthetic torrent every
	// node in the swarm shares — a single uniform piece length (the last
	// piece short, exactly metainfo's own PieceLen rule), not a real
	// per-torrent geometry read from a .torrent file, since a simulated
	// scenario has no real content to have one.
	TotalLength int64
	PieceLength int64

	// Network is the shared latency/loss model every connection uses —
	// see NetworkParams' own doc comment.
	Network NetworkParams

	// MaxPeers caps how many other online nodes a node connects to when
	// it (re)joins the swarm — real clients cap this too; an unbounded
	// all-to-all topology would make upload slots meaningless, since
	// nothing would ever be scarce.
	MaxPeers int

	// ChokerSlots overrides choker.DefaultSlots for every node.
	ChokerSlots int

	// TickInterval is how often a node re-evaluates its connections for
	// new requests to send — the picking half of the real actor's own
	// tick loop, just on virtual time. ChokeInterval is the same for
	// re-running the unchoke algorithm, deliberately much less frequent
	// (real clients rechoke roughly every 10s; picking needs to react
	// far faster than that).
	TickInterval  time.Duration
	ChokeInterval time.Duration

	// ChurnInterval/ChurnProbability drive optional peer churn: every
	// ChurnInterval, each currently-online, churn-eligible node has an
	// independent ChurnProbability chance of disconnecting, later
	// rejoining after a random offline span in
	// [ChurnOfflineMin,ChurnOfflineMax]. Leaving ChurnProbability at 0
	// (the default) disables churn entirely — no churn event is ever
	// scheduled, so a plain swarm pays nothing for a feature it isn't
	// using.
	ChurnInterval                    time.Duration
	ChurnProbability                 float64
	ChurnOfflineMin, ChurnOfflineMax time.Duration

	// StopWhenAllComplete ends Run the instant every node currently
	// expected to finish (every node that isn't mid-churn-offline
	// forever, in practice: every node at all, since churned nodes do
	// eventually come back) has completed, rather than running out the
	// full MaxDuration. Defaults to true — the common case ("how long
	// does this swarm take") wants the real answer, not a padded one.
	StopWhenAllComplete *bool

	// MaxDuration is a hard safety cap on virtual time, always enforced
	// regardless of StopWhenAllComplete — a scenario with no seeder, or
	// churn that never lets everyone reconnect at once, would otherwise
	// run forever. Defaults to 24 simulated hours, which is already an
	// enormous number of ticks for any realistic scenario to need.
	MaxDuration time.Duration

	// Seed is the single source of randomness for the whole run — tie
	// breaks, the choker's optimistic pick, topology selection, loss
	// rolls, and churn timing all draw from one *rand.Rand seeded from
	// this value, so the exact same Config and Seed always replay to the
	// exact same result. There is no "unset" case: 0 is as valid and as
	// deterministic a seed as any other.
	Seed int64
}

func (c Config) withDefaults() Config {
	if c.MaxPeers <= 0 {
		c.MaxPeers = 30
	}
	if c.ChokerSlots <= 0 {
		c.ChokerSlots = choker.DefaultSlots
	}
	if c.TickInterval <= 0 {
		c.TickInterval = time.Second
	}
	if c.ChokeInterval <= 0 {
		c.ChokeInterval = 10 * time.Second
	}
	if c.ChurnOfflineMin <= 0 {
		c.ChurnOfflineMin = 30 * time.Second
	}
	if c.ChurnOfflineMax <= 0 {
		c.ChurnOfflineMax = 5 * time.Minute
	}
	if c.MaxDuration <= 0 {
		c.MaxDuration = 24 * time.Hour
	}
	if c.StopWhenAllComplete == nil {
		t := true
		c.StopWhenAllComplete = &t
	}
	c.Network = c.Network.withDefaults()
	return c
}

// Swarm is a whole deterministic simulation run: a shared virtual Clock,
// a shared *rand.Rand, and every Node currently (or eventually) part of
// it.
type Swarm struct {
	cfg       Config
	clock     *Clock
	rng       *rand.Rand
	numPieces int

	nodes   map[string]*Node
	order   []string // insertion order, for deterministic reporting/iteration where map order would otherwise vary
	pending int      // nodes not yet complete and not permanently done contributing — used by the "stop when all complete" check
}

// NewSwarm returns an empty Swarm ready for AddNode calls.
func NewSwarm(cfg Config) (*Swarm, error) {
	if cfg.TotalLength <= 0 {
		return nil, fmt.Errorf("simulator: TotalLength must be positive")
	}
	if cfg.PieceLength <= 0 {
		return nil, fmt.Errorf("simulator: PieceLength must be positive")
	}
	cfg = cfg.withDefaults()

	numPieces := int((cfg.TotalLength + cfg.PieceLength - 1) / cfg.PieceLength)
	return &Swarm{
		cfg:       cfg,
		clock:     newClock(),
		rng:       rand.New(rand.NewSource(cfg.Seed)),
		numPieces: numPieces,
		nodes:     make(map[string]*Node),
	}, nil
}

// pieceLen mirrors metainfo.MetaInfo.PieceLen: every piece is
// cfg.PieceLength except the last, which is whatever remains.
func (s *Swarm) pieceLen(index int) int64 {
	if index < 0 || index >= s.numPieces {
		return 0
	}
	if index == s.numPieces-1 {
		return s.cfg.TotalLength - int64(s.numPieces-1)*s.cfg.PieceLength
	}
	return s.cfg.PieceLength
}

// AddNode registers a new participant. Its connection to the rest of the
// swarm happens at (a real, non-negative) NodeConfig.JoinAt, not
// immediately — the node exists from the moment AddNode returns, but
// nothing else in the swarm can see it until it actually joins.
func (s *Swarm) AddNode(nc NodeConfig) error {
	if nc.ID == "" {
		return fmt.Errorf("simulator: node ID is required")
	}
	if _, exists := s.nodes[nc.ID]; exists {
		return fmt.Errorf("simulator: node %q already added", nc.ID)
	}
	if nc.JoinAt < 0 {
		return fmt.Errorf("simulator: node %q has a negative JoinAt", nc.ID)
	}

	pk, err := picker.New(picker.Config{
		NumPieces:   s.numPieces,
		PieceLength: s.pieceLen,
		Strategy:    nc.Strategy,
		Rand:        s.rng,
	})
	if err != nil {
		return fmt.Errorf("simulator: node %q: %w", nc.ID, err)
	}

	ck := choker.New(choker.WithSlots(s.cfg.ChokerSlots), choker.WithRand(s.rng))

	n := &Node{
		id:       nc.ID,
		seed:     nc.Seed,
		swarm:    s,
		pick:     pk,
		choke:    ck,
		upRate:   nc.UploadRate,
		downRate: nc.DownloadRate,
		conns:    make(map[string]*connection),
	}
	if nc.Seed {
		n.have = bitfield.Full(s.numPieces)
		if err := n.pick.SetHave(n.have); err != nil {
			return fmt.Errorf("simulator: node %q: %w", nc.ID, err)
		}
	} else {
		n.have = bitfield.New(s.numPieces)
	}

	s.nodes[nc.ID] = n
	s.order = append(s.order, nc.ID)
	if !nc.Seed {
		s.pending++
	}

	churnEligible := nc.ChurnEligible && !nc.Seed
	s.clock.After(nc.JoinAt, func() {
		s.bringOnline(n, churnEligible)
	})
	return nil
}

// sortedConnIDs returns a node's current peer IDs in a fixed, reproducible
// order — Go's own map iteration order is randomized per run, and letting
// that leak into request/choke ordering would make two runs with the same
// Seed produce different results, defeating the entire point of this
// package.
func sortedConnIDs(n *Node) []string {
	ids := make([]string, 0, len(n.conns))
	for id := range n.conns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
