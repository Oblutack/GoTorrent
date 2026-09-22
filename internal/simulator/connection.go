package simulator

import "github.com/Oblutack/GoTorrent/internal/bitfield"

// connection is the shared state of one simulated link between two nodes —
// the in-memory stand-in for a real peer.Client's per-connection fields
// (HasPiece, AmChoking/PeerChoking, AmInterested/PeerInterested), indexed
// [0]/[1] by which of the two nodes (a/b) a field describes. Every field
// named haveViewOf/interestedIn/choking/outstanding/bytesReceived follows
// the same convention: index k always describes nodes[k]'s own state or
// nodes[k]'s own belief about the other side — see each field's comment.
type connection struct {
	nodes [2]*Node

	// haveViewOf[k] is nodes[k]'s current belief about what nodes[1-k]
	// has — a snapshot taken at connect time, updated incrementally by
	// delayed "Have" delivery afterward. Deliberately never a direct
	// alias of the other node's real bitfield: real swarms only ever
	// know a peer's pieces through messages that take real time to
	// arrive, and that lag is part of what makes rarest-first decisions
	// interesting to measure.
	haveViewOf [2]*bitfield.Bitfield

	// interestedIn[k] is whether nodes[k] currently wants to download
	// something from nodes[1-k] — recomputed live on every pick attempt,
	// not itself message-delayed (see connectionEnd's own doc comment
	// for why that's a deliberately accepted simplification).
	interestedIn [2]bool

	// choking[k] is whether nodes[k] is choking nodes[1-k] — nodes[k]'s
	// own authoritative decision, applied immediately for gating a
	// request the instant it is evaluated (see attemptFill/deliverBlock
	// in swarm.go). Both start choking the other, same as the real
	// protocol's initial per-connection state.
	choking [2]bool

	// outstanding[k] is how many requests nodes[k] has sent to nodes[1-k]
	// and not yet had answered — capped at pipelineCap. Only ever
	// decremented on a real delivery, mirroring the real client's own
	// pc.outstanding (see CLAUDE.md's "Adaptive pipelining" paragraph):
	// a block that is lost or a peer that simply stops responding leaves
	// this stuck at the cap, which is exactly what should happen — this
	// connection naturally stops being offered new requests without any
	// separate dead-peer detection, while internal/picker's own Expire
	// frees the underlying piece for a *different* connection to finish.
	outstanding [2]int

	// bytesReceived[k] is the cumulative bytes nodes[k] has received from
	// nodes[1-k] over this connection — both this node's own download
	// total for the connection and, read from the other side, exactly
	// the "how much has this peer given me" figure choker.Peer.
	// BytesDownloaded needs for tit-for-tat ranking.
	bytesReceived [2]int64
}

// indexOf returns 0 or 1 for whichever side id is, or -1 if id is neither
// end of this connection.
func (c *connection) indexOf(id string) int {
	switch id {
	case c.nodes[0].id:
		return 0
	case c.nodes[1].id:
		return 1
	default:
		return -1
	}
}

// other returns the far side's node, given one side's own index.
func (c *connection) other(side int) *Node { return c.nodes[1-side] }

// connEnd is one node's view of one of its connections, implementing
// choker.Peer directly against the shared connection state — the real
// production Choker.Run is driven by exactly this type, unmodified.
type connEnd struct {
	conn *connection
	side int
}

func (e connEnd) ID() string { return e.conn.other(e.side).id }

// Interested reports whether the *other* side wants to download from us —
// what the choker actually needs to decide if unchoking them costs
// anything.
func (e connEnd) Interested() bool { return e.conn.interestedIn[1-e.side] }

func (e connEnd) Choking() bool { return e.conn.choking[e.side] }

func (e connEnd) Choke() error {
	if !e.conn.choking[e.side] {
		e.conn.choking[e.side] = true
		e.conn.nodes[e.side].timesChoked++
		e.conn.nodes[e.side].swarm.onChokeChanged(e.conn, e.side)
	}
	return nil
}

func (e connEnd) Unchoke() error {
	if e.conn.choking[e.side] {
		e.conn.choking[e.side] = false
		e.conn.nodes[e.side].timesUnchoked++
		e.conn.nodes[e.side].swarm.onChokeChanged(e.conn, e.side)
	}
	return nil
}

// BytesDownloaded is what we have received from the other side — the
// tit-for-tat reciprocity signal.
func (e connEnd) BytesDownloaded() int64 { return e.conn.bytesReceived[e.side] }
