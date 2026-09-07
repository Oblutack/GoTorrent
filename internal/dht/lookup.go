package dht

import (
	"context"
	"net"
	"sort"
	"sync"

	"github.com/Oblutack/GoTorrent/internal/tracker"
)

const (
	// lookupAlpha is how many closest-unqueried contacts an iterative lookup
	// queries concurrently per round — the standard Kademlia concurrency
	// parameter.
	lookupAlpha = 3
	// lookupK matches bucketSize: a lookup converges on the k closest nodes
	// it can find, the same k a bucket holds.
	lookupK = bucketSize
	// maxLookupRounds caps how many rounds an iterative lookup runs, so a
	// pathological or hostile set of responses (nodes that keep returning
	// "closer" nodes that lead nowhere) cannot loop forever.
	maxLookupRounds = 20
)

// DefaultBootstrapNodes are the well-known public routers most DHT
// implementations join the network through.
var DefaultBootstrapNodes = []string{
	"router.bittorrent.com:6881",
	"dht.transmissionbt.com:6881",
	"router.utorrent.com:6881",
}

// Bootstrap joins the DHT network: it pings each of addrs (typically
// DefaultBootstrapNodes plus whatever a persisted routing table already
// seeded — see New's StatePath), then runs a find_node lookup for this
// node's own ID, which is the standard way a fresh table fills in with real
// nodes rather than just the handful of routers. It blocks until done or ctx
// is cancelled, so callers typically run it in a spawned goroutine.
func (d *DHT) Bootstrap(ctx context.Context, addrs []string) {
	for _, a := range addrs {
		addr, err := net.ResolveUDPAddr("udp", a)
		if err != nil {
			continue
		}
		qctx, cancel := context.WithTimeout(ctx, queryTimeout)
		d.findNode(qctx, addr, d.id)
		cancel()
		if ctx.Err() != nil {
			return
		}
	}
	d.iterativeLookup(ctx, d.id, nil)
}

// shortlistEntry is one candidate in an iterative lookup's working set.
type shortlistEntry struct {
	id      NodeID
	addr    *net.UDPAddr
	queried bool
}

// iterativeLookup drives BEP 5's standard convergence: ask the alpha closest
// not-yet-queried contacts, merge whatever nodes they return into the
// working set, repeat against the new closest unqueried contacts, stop once
// nothing left in the current top-k is unqueried (or maxLookupRounds is hit).
//
// If collectPeers is non-nil, every query is get_peers instead of find_node,
// and each response's peers (if any) plus the token needed to announce_peer
// against that responder are handed to it; otherwise this is a plain
// find_node lookup, used by Bootstrap.
func (d *DHT) iterativeLookup(
	ctx context.Context,
	target NodeID,
	collectPeers func(addr *net.UDPAddr, token string, peers []tracker.PeerInfo),
) []nodeAddr {
	var mu sync.Mutex
	shortlist := make(map[NodeID]*shortlistEntry)
	for _, n := range d.table.FindClosest(target, lookupK) {
		shortlist[n.id] = &shortlistEntry{id: n.id, addr: n.addr}
	}

	closestIDs := func() []NodeID {
		ids := make([]NodeID, 0, len(shortlist))
		for id := range shortlist {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return xor(target, ids[i]).less(xor(target, ids[j])) })
		if len(ids) > lookupK {
			ids = ids[:lookupK]
		}
		return ids
	}

	for round := 0; round < maxLookupRounds; round++ {
		mu.Lock()
		var toQuery []*shortlistEntry
		for _, id := range closestIDs() {
			e := shortlist[id]
			if !e.queried {
				e.queried = true
				toQuery = append(toQuery, e)
				if len(toQuery) >= lookupAlpha {
					break
				}
			}
		}
		mu.Unlock()

		if len(toQuery) == 0 {
			break // every contact in the current top-k has already been asked
		}

		var wg sync.WaitGroup
		for _, e := range toQuery {
			wg.Add(1)
			go func(e *shortlistEntry) {
				defer wg.Done()
				qctx, cancel := context.WithTimeout(ctx, queryTimeout)
				defer cancel()

				var nodes []nodeAddr
				if collectPeers != nil {
					res, err := d.getPeers(qctx, e.addr, target)
					if err != nil {
						d.table.MarkFailed(e.id)
						return
					}
					if len(res.peers) > 0 {
						collectPeers(e.addr, res.token, res.peers)
					}
					nodes = res.nodes
				} else {
					found, err := d.findNode(qctx, e.addr, target)
					if err != nil {
						d.table.MarkFailed(e.id)
						return
					}
					nodes = found
				}

				mu.Lock()
				for _, n := range nodes {
					if _, ok := shortlist[n.id]; !ok {
						shortlist[n.id] = &shortlistEntry{id: n.id, addr: n.addr}
					}
				}
				mu.Unlock()
			}(e)
		}
		wg.Wait()

		if ctx.Err() != nil {
			break
		}
	}

	mu.Lock()
	defer mu.Unlock()
	ids := closestIDs()
	out := make([]nodeAddr, 0, len(ids))
	for _, id := range ids {
		out = append(out, nodeAddr{id: id, addr: shortlist[id].addr})
	}
	return out
}

// FindPeers runs an iterative get_peers lookup for infoHash, returning every
// distinct peer address any visited node reported. It also announces this
// node as a peer (with the port a caller downloading or seeding that
// infohash is actually listening on) to every node whose response carried a
// valid token — the standard way a real BitTorrent client both finds peers
// and makes itself findable in a single pass, since a get_peers lookup
// already visits precisely the nodes closest to the infohash: exactly who an
// announce_peer needs to reach.
func (d *DHT) FindPeers(ctx context.Context, infoHash NodeID, port uint16) []tracker.PeerInfo {
	var mu sync.Mutex
	var peers []tracker.PeerInfo
	seen := make(map[string]bool)

	type announceTarget struct {
		addr  *net.UDPAddr
		token string
	}
	var targets []announceTarget

	d.iterativeLookup(ctx, infoHash, func(addr *net.UDPAddr, token string, found []tracker.PeerInfo) {
		mu.Lock()
		for _, p := range found {
			key := p.Addr()
			if !seen[key] {
				seen[key] = true
				peers = append(peers, p)
			}
		}
		if token != "" {
			targets = append(targets, announceTarget{addr: addr, token: token})
		}
		mu.Unlock()
	})

	for _, t := range targets {
		actx, cancel := context.WithTimeout(ctx, queryTimeout)
		d.announcePeer(actx, t.addr, infoHash, port, t.token)
		cancel()
	}

	return peers
}
