package torrent

import (
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// pexInterval is how often broadcastPEX runs. BEP 11 says implementations
// SHOULD NOT send more than once per minute; this matches that floor
// exactly rather than padding it, since less frequent PEX just means slower
// peer discovery through this channel (trackers and DHT are still running
// on their own schedules regardless).
//
// A var, not a const, purely so tests can shorten it before calling Run —
// run() reads it once to build its ticker, so an override only has to land
// before the actor goroutine starts.
var pexInterval = 60 * time.Second

// broadcastPEX diffs the current outbound-dialed peer set (see
// peerConn.peerInfo) against pexKnownPeers to find what changed since the
// last broadcast, and sends the delta to every connected peer that
// advertised ut_pex support. A torrent with no change since last time sends
// nothing at all — an empty update is not useful to anyone. Disabled
// entirely for a private torrent (BEP 27), re-checked every call since
// metadata is not necessarily known yet when this first starts running (the
// magnet path) — same reasoning as dhtLoop's per-iteration Private check.
func (t *Torrent) broadcastPEX() {
	if mi := t.mi.Load(); mi != nil && mi.Info.Private {
		return
	}

	current := make(map[string]tracker.PeerInfo, len(t.peers))
	for addr, pc := range t.peers {
		if pc.peerInfo.Port == 0 {
			continue // inbound connection with no known dial-back address
		}
		current[addr] = pc.peerInfo
	}

	var added, dropped []tracker.PeerInfo
	for addr, pi := range current {
		if _, known := t.pexKnownPeers[addr]; !known {
			added = append(added, pi)
		}
	}
	for addr, pi := range t.pexKnownPeers {
		if _, still := current[addr]; !still {
			dropped = append(dropped, pi)
		}
	}
	t.pexKnownPeers = current

	if len(added) == 0 && len(dropped) == 0 {
		return
	}
	for _, pc := range t.peers {
		if err := pc.client.SendPEX(added, dropped); err != nil {
			logger.Logf("torrent %s: PEX to %s: %v\n", t.infoHash, pc.addr, err)
		}
	}
}

// onPEXUpdate dials every peer a PEX message told us about. Dropped peers
// need no action — the actor does not proactively disconnect on a peer's
// say-so, the same trust posture as every other rumor about the swarm.
// Disabled for a private torrent for the same reason broadcastPEX is: a
// compliant peer never sends PEX for one in the first place, but a
// non-compliant one might, and acting on it would defeat the point.
func (t *Torrent) onPEXUpdate(pc *peerConn, update peer.PEXUpdate) {
	if mi := t.mi.Load(); mi != nil && mi.Info.Private {
		return
	}
	for _, pi := range update.Added {
		t.dial(pi)
	}
}
