package simulator

import (
	"time"

	"github.com/Oblutack/GoTorrent/internal/choker"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

// bringOnline connects n to a random subset of the swarm's other
// currently-online nodes and starts its periodic tick loops. Used both
// for a node's very first join (from AddNode's scheduled JoinAt event) and
// for a churned-off node's later reconnection (from takeOffline's
// scheduled return) — the two cases need identical work, since a
// reconnecting node has already had every one of its old connections torn
// down by takeOffline.
func (s *Swarm) bringOnline(n *Node, churnEligible bool) {
	n.online = true
	n.joinedAt = s.clock.Elapsed()

	candidates := make([]*Node, 0, len(s.nodes))
	for _, id := range s.order {
		other := s.nodes[id]
		if other.id != n.id && other.online {
			candidates = append(candidates, other)
		}
	}
	s.rng.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })

	max := s.cfg.MaxPeers
	if max > len(candidates) {
		max = len(candidates)
	}
	for _, other := range candidates[:max] {
		s.connect(n, other)
	}

	s.scheduleTick(n)
	s.scheduleChoke(n)
	if churnEligible {
		s.scheduleChurnCheck(n)
	}
}

// connect creates the shared connection state for a new pair and schedules
// their handshake (bitfield exchange) after one network latency — nothing
// about a fresh connection is known to either side until that lands,
// exactly like a real TCP handshake plus an initial Bitfield message.
func (s *Swarm) connect(a, b *Node) {
	conn := &connection{nodes: [2]*Node{a, b}}
	a.conns[b.id] = conn
	b.conns[a.id] = conn

	s.clock.After(s.cfg.Network.Latency, func() {
		for m := 0; m < 2; m++ {
			// haveViewOf[m] is nodes[m]'s belief about nodes[1-m] — see
			// connection's own doc comment for this indexing convention,
			// used consistently everywhere in this file.
			conn.haveViewOf[m] = conn.nodes[1-m].have.Clone()
			conn.nodes[m].pick.Availability().AddPeer(conn.haveViewOf[m])
			conn.interestedIn[m] = conn.nodes[m].wantsFrom(conn.haveViewOf[m])
		}
		s.fillConnection(conn, 0)
		s.fillConnection(conn, 1)
	})
}

// disconnect removes conn from both sides — a churn departure, modeled as
// both ends independently and immediately noticing the peer is gone
// (unlike every other event in this file, there is no latency to
// disconnecting: a closed TCP connection is detected locally, not learned
// about over the wire).
func (s *Swarm) disconnect(conn *connection) {
	for m := 0; m < 2; m++ {
		self, other := conn.nodes[m], conn.nodes[1-m]
		delete(self.conns, other.id)
		// Back out exactly what was added at connect/Have time — self's
		// own belief about other, which is what self's own Availability
		// was built from.
		self.pick.Availability().RemovePeer(conn.haveViewOf[m])
	}
}

// takeOffline is a churn departure: every connection is torn down, and if
// churnEligible the node is scheduled to rejoin after a random offline
// span.
func (s *Swarm) takeOffline(n *Node, churnEligible bool) {
	if !n.online {
		return
	}
	for _, id := range sortedConnIDs(n) {
		s.disconnect(n.conns[id])
	}
	n.online = false

	if churnEligible {
		lo, hi := s.cfg.ChurnOfflineMin, s.cfg.ChurnOfflineMax
		offline := lo
		if hi > lo {
			offline = lo + s.durationJitter(hi-lo)
		}
		s.clock.After(offline, func() {
			s.bringOnline(n, true)
		})
	}
}

// durationJitter returns a uniformly random duration in [0, max).
func (s *Swarm) durationJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(s.rng.Int63n(int64(max)))
}

// fillConnection lets nodes[side] send as many new requests to the other
// side as its pipeline has room for right now — called after a connect,
// after a block delivery frees a slot, and reactively the moment the
// other side unchokes it, in addition to the periodic backstop in
// pickTick.
func (s *Swarm) fillConnection(conn *connection, side int) {
	self := conn.nodes[side]
	if !self.online {
		return
	}
	conn.interestedIn[side] = self.wantsFrom(conn.haveViewOf[side])
	if !conn.interestedIn[side] || conn.choking[1-side] {
		return
	}
	room := pipelineCap - conn.outstanding[side]
	if room <= 0 {
		return
	}
	peerHas := func(i int) bool { return conn.haveViewOf[side].Has(i) }
	reqs := self.pick.Pick(peerHas, room, s.clock.Now())
	for _, r := range reqs {
		s.sendRequest(conn, side, r)
	}
}

// sendRequest is the one place loss is rolled for a whole request/response
// round trip: either the entire exchange happens (request transits,
// provider serves it, the block transits back) or none of it leaves any
// trace at all, the same all-or-nothing granularity a single dropped TCP
// segment has in practice.
func (s *Swarm) sendRequest(conn *connection, side int, req picker.Request) {
	conn.outstanding[side]++
	if s.cfg.Network.lost(s.rng) {
		return
	}
	s.clock.After(s.cfg.Network.Latency, func() {
		s.serveRequest(conn, side, req)
	})
}

// serveRequest runs on the provider's side once the request arrives —
// choke state is checked live, here, not at send time, so a choke that
// happens mid-flight correctly still blocks a request that was sent
// before it landed.
func (s *Swarm) serveRequest(conn *connection, side int, req picker.Request) {
	providerSide := 1 - side
	if !conn.nodes[providerSide].online || conn.choking[providerSide] {
		return
	}
	provider, requester := conn.nodes[providerSide], conn.nodes[side]
	rate := effectiveRate(provider.upRate, requester.downRate)
	s.clock.After(s.cfg.Network.Latency+transferTime(req.Length, rate), func() {
		s.deliverBlock(conn, side, req)
	})
}

// effectiveRate combines an upload and a download cap into the rate a
// single transfer actually gets — the smaller of the two, treating <= 0 as
// unbounded on either side. Deliberately does not divide by how many
// other transfers either node has in flight concurrently — see
// network.go's own doc comment on NetworkParams for why that fair-share
// refinement is a documented v2 gap, not an oversight.
func effectiveRate(up, down int64) int64 {
	switch {
	case up <= 0 && down <= 0:
		return 0
	case up <= 0:
		return down
	case down <= 0:
		return up
	case up < down:
		return up
	default:
		return down
	}
}

// deliverBlock is where a requested block actually lands — the only place
// picker.Received is ever called, mirroring exactly what a real block
// arrival does in the production actor.
func (s *Swarm) deliverBlock(conn *connection, side int, req picker.Request) {
	requester, provider := conn.nodes[side], conn.nodes[1-side]
	conn.outstanding[side]--
	if !requester.online {
		return
	}
	conn.bytesReceived[side] += int64(req.Length)
	requester.bytesDown += int64(req.Length)
	provider.bytesUp += int64(req.Length)

	// pieceDone means this one piece just finished — not the whole
	// torrent; Picker.Received's own doc comment is explicit about that,
	// and conflating the two here would mark a node "complete" after its
	// very first piece.
	pieceDone, wanted := requester.pick.Received(req.Index, req.Begin, req.Length)
	if wanted && pieceDone {
		requester.have.Set(req.Index)
		requester.pick.MarkVerified(req.Index)
		if requester.completedAt == nil && requester.pick.Complete() {
			t := s.clock.Elapsed()
			requester.completedAt = &t
			s.pending--
		}
		s.broadcastHave(requester, req.Index)
	}
	s.fillConnection(conn, side)
}

// broadcastHave tells every connection of a node that just finished piece
// index — each arrives after that connection's own latency, and updates
// the *other* side's belief and Availability, per the indexing convention
// connection's own doc comment establishes.
func (s *Swarm) broadcastHave(n *Node, index int) {
	for _, id := range sortedConnIDs(n) {
		conn := n.conns[id]
		ownerSide := conn.indexOf(n.id)
		otherSide := 1 - ownerSide
		s.clock.After(s.cfg.Network.Latency, func() {
			if !conn.nodes[otherSide].online {
				return
			}
			conn.haveViewOf[otherSide].Set(index)
			conn.nodes[otherSide].pick.Availability().Add(index)
			s.fillConnection(conn, otherSide)
		})
	}
}

// onChokeChanged is called synchronously from connEnd.Choke/Unchoke — the
// choking node's own bookkeeping is already updated by the time this
// runs, so it only has to handle the reactive side: giving the *other*
// end a prompt chance to start requesting once it actually learns it has
// been unchoked, one latency later.
func (s *Swarm) onChokeChanged(conn *connection, side int) {
	if conn.choking[side] {
		return // a fresh choke needs no reaction — the peer just stops getting served, discovered the next time it tries
	}
	other := 1 - side
	s.clock.After(s.cfg.Network.Latency, func() {
		s.fillConnection(conn, other)
	})
}

func (s *Swarm) scheduleTick(n *Node) {
	var tick func()
	tick = func() {
		if !n.online {
			return
		}
		n.pick.Expire(s.clock.Now())
		for _, id := range sortedConnIDs(n) {
			s.fillConnection(n.conns[id], n.conns[id].indexOf(n.id))
		}
		s.clock.After(s.cfg.TickInterval, tick)
	}
	s.clock.After(s.cfg.TickInterval, tick)
}

func (s *Swarm) scheduleChoke(n *Node) {
	var tick func()
	tick = func() {
		if !n.online {
			return
		}
		ids := sortedConnIDs(n)
		peers := make([]choker.Peer, len(ids))
		for i, id := range ids {
			conn := n.conns[id]
			peers[i] = connEnd{conn: conn, side: conn.indexOf(n.id)}
		}
		n.choke.Run(peers, s.clock.Now())
		s.clock.After(s.cfg.ChokeInterval, tick)
	}
	s.clock.After(s.cfg.ChokeInterval, tick)
}

func (s *Swarm) scheduleChurnCheck(n *Node) {
	if s.cfg.ChurnProbability <= 0 || s.cfg.ChurnInterval <= 0 {
		return
	}
	var tick func()
	tick = func() {
		if !n.online {
			return // already offline (or a completed, no-longer-eligible node) — no need to keep polling; bringOnline restarts this on rejoin
		}
		if s.rng.Float64() < s.cfg.ChurnProbability {
			s.takeOffline(n, true)
			return // takeOffline schedules the rejoin, which restarts this same churn check
		}
		s.clock.After(s.cfg.ChurnInterval, tick)
	}
	s.clock.After(s.cfg.ChurnInterval, tick)
}
