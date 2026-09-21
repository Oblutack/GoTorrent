package torrent

import (
	"sort"

	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// orderDiscoveredPeers ranks a batch of freshly-discovered peers (from a
// tracker announce, a DHT lookup, or PEX — all funneled through the same
// eventTrackerPeers event, see its own doc comment) by BEP 40 canonical
// peer priority before dialing them, exactly as its own spec text
// recommends: "A peer's priority can be computed before connecting, so it
// may be beneficial for clients to take that into consideration when
// deciding which peers to connect to." Only meaningful once Config.LocalIP
// is known (this client's own internet-facing address, learned via
// engine.Engine.StartPortMapping's gateway query) — without it, peers are
// dialed in whatever order they arrived, same as before this existed, a
// graceful no-op rather than an error.
//
// Deliberately does not touch existing connections or evict anything to
// make room for a higher-priority candidate — BEP 40's own text describes
// that as a legitimate further use (addressing swarms where early peers
// permanently hog every connection slot), but the spec itself does not
// define a concrete eviction/agreement protocol for it, and getting that
// right needs real interop testing against another compliant client this
// project has no way to do. This is the one recommendation the spec states
// outright and that a purely local, no-negotiation-needed implementation
// can get unambiguously right: order candidates before connecting.
func (t *Torrent) orderDiscoveredPeers(peers []tracker.PeerInfo) []tracker.PeerInfo {
	if t.cfg.LocalIP == nil || len(peers) < 2 {
		return peers
	}
	ordered := append([]tracker.PeerInfo(nil), peers...)
	sort.SliceStable(ordered, func(i, j int) bool {
		pi := peer.CanonicalPriority(t.cfg.LocalIP, t.cfg.ListenPort, ordered[i].IP, ordered[i].Port)
		pj := peer.CanonicalPriority(t.cfg.LocalIP, t.cfg.ListenPort, ordered[j].IP, ordered[j].Port)
		return pi > pj
	})
	return ordered
}
