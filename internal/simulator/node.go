package simulator

import (
	"time"

	"github.com/Oblutack/GoTorrent/internal/bitfield"
	"github.com/Oblutack/GoTorrent/internal/choker"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

// pipelineCap is the fixed max outstanding-block count per connection.
// The real client adapts this per peer from measured throughput
// (internal/torrent's adaptPipeline) — a real refinement deliberately not
// replicated here, since it is itself an implementation detail of the
// actor, not of picker/choker, which are what this package exists to
// measure. A single generous fixed cap is a documented simplification,
// not an oversight.
const pipelineCap = 12

// NodeConfig configures one swarm participant at AddNode time.
type NodeConfig struct {
	// ID must be unique within the swarm.
	ID string
	// Seed, if true, starts this node with every piece already verified —
	// a pure seeder, never interested in anything.
	Seed bool
	// UploadRate/DownloadRate are this node's own bandwidth caps in
	// bytes/sec, shared across every connection it currently has data in
	// flight on (see connection.go's transfer-time accounting). Zero
	// means unbounded, the same "0 = unlimited" convention
	// internal/ratelimit already uses.
	UploadRate, DownloadRate int64
	// Strategy overrides the swarm's default picker.Strategy for just
	// this node — lets a scenario mix rarest-first and sequential nodes
	// in the same run to compare them under identical network
	// conditions.
	Strategy picker.Strategy
	// JoinAt delays this node's connection to the swarm — 0 means present
	// from t=0. Models a peer arriving partway through the swarm's life,
	// the simplest form of churn (join without ever leaving).
	JoinAt time.Duration
	// ChurnEligible marks this node as a candidate for the swarm-wide
	// periodic churn sweep (see swarm.go's churnTick) — random
	// disconnect/reconnect cycles. Seeders are never churn-eligible
	// regardless of this flag: a simulator studying swarm resilience
	// wants to churn the leechers, not remove the only source of data.
	ChurnEligible bool
}

// Node is one swarm participant. It owns a real picker.Picker and a real
// choker.Choker — the production strategy code — plus the bookkeeping the
// simulation needs to drive them without a real actor goroutine, real
// sockets, or real wall-clock time.
type Node struct {
	id       string
	seed     bool
	swarm    *Swarm
	pick     *picker.Picker
	choke    *choker.Choker
	have     *bitfield.Bitfield
	upRate   int64
	downRate int64

	online bool // false while churned-off; also false before JoinAt

	conns map[string]*connection // peer ID -> shared connection state

	// stats
	joinedAt      time.Duration
	completedAt   *time.Duration
	bytesUp       int64
	bytesDown     int64
	timesChoked   int
	timesUnchoked int
}

// wantsFrom reports whether n still wants anything peer currently has, per
// n's own haveViewOf that connection — the same "does this connection have
// anything left to offer" check both the interested-flag recompute and
// picker.Pick's own peerHas closure are built from.
func (n *Node) wantsFrom(peerHave *bitfield.Bitfield) bool {
	if n.pick.Complete() {
		return false
	}
	want := false
	peerHave.Each(func(i int) bool {
		if !n.have.Has(i) {
			want = true
			return false
		}
		return true
	})
	return want
}

// Complete reports whether this node has finished downloading.
func (n *Node) Complete() bool { return n.pick.Complete() }
