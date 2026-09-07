package torrent

import (
	"context"
	"time"

	"github.com/Oblutack/GoTorrent/internal/dht"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// dhtReannounceInterval mirrors a typical tracker interval: frequent enough
// that a torrent finds new peers reasonably quickly, infrequent enough that
// an iterative lookup (several round trips to several nodes) is not running
// back-to-back.
const dhtReannounceInterval = 5 * time.Minute

// DHTClient is the subset of *dht.DHT a torrent needs. One method covers
// both directions BEP 5 peer discovery works in: finding peers for infohash,
// and letting other DHT peers find this one right back, since a real
// get_peers lookup already visits exactly the nodes an announce_peer needs
// to reach — see dht.DHT.FindPeers.
type DHTClient interface {
	FindPeers(ctx context.Context, infoHash dht.NodeID, port uint16) []tracker.PeerInfo
}

// dhtLoop mirrors announceLoop's cadence but through Config.DHT instead of a
// tracker. Unlike a tracker it needs no announce URL to get started — an
// infohash is all a DHT lookup needs — so it runs from the moment the
// torrent starts, magnet or not. It re-checks Config.DHT's private-torrent
// exemption on every iteration rather than only once, since metadata (and so
// Info.Private) is not necessarily known yet when this starts.
func (t *Torrent) dhtLoop(ctx context.Context) {
	defer t.wg.Done()

	for {
		if mi := t.mi.Load(); mi != nil && mi.Info.Private {
			return // BEP 27: a private torrent must never touch the DHT
		}

		peers := t.cfg.DHT.FindPeers(ctx, dht.NodeID(t.infoHash), t.cfg.ListenPort)
		if len(peers) > 0 {
			t.sendEvent(ctx, eventTrackerPeers{peers: peers})
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(dhtReannounceInterval):
		}
	}
}

// restartDHTLoop stops whatever DHT loop is currently running (if any) and,
// if this torrent has a DHT configured, starts a fresh one — the DHT
// counterpart to restartAnnounceLoop, called from the same places (Run's
// setup, doResume, doRecheck) so the two peer-discovery loops always start
// and stop together.
func (t *Torrent) restartDHTLoop() {
	t.stopDHTLoop()
	if t.cfg.DHT == nil {
		return
	}
	ctx, cancel := context.WithCancel(t.ctx)
	t.dhtCancel = cancel
	t.wg.Add(1)
	go t.dhtLoop(ctx)
}

// stopDHTLoop cancels the running DHT loop, if any.
func (t *Torrent) stopDHTLoop() {
	if t.dhtCancel != nil {
		t.dhtCancel()
		t.dhtCancel = nil
	}
}
