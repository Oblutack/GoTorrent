package torrent

import (
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// controlKind distinguishes the request/response messages sent over
// Torrent.control. These are the operations an external caller can ask the
// actor to perform; everything else (peer traffic, tracker results) arrives
// as an event instead, since nothing outside the actor needs to wait for
// those to complete.
type controlKind int

const (
	ctrlPause controlKind = iota
	ctrlResume
	ctrlRecheck
	ctrlSetMetadata
	ctrlStats
)

type controlMsg struct {
	kind controlKind

	// metadata is set for ctrlSetMetadata.
	metadata *metainfo.MetaInfo

	// errReply receives the result of Pause/Resume/Recheck/SetMetadata.
	errReply chan error
	// statsReply receives the actor-owned half of a Stats snapshot.
	statsReply chan Stats
}

// The event types below all arrive on Torrent.events. They are a closed set
// dispatched by run() in a type switch; unlike controlMsg, none of them
// expect a reply; the actor is exclusively the receiver.

// eventDialRequest asks the actor to connect to a peer, subject to the
// dedup/cap checks only the actor can safely make against its own peers map.
type eventDialRequest struct {
	addr tracker.PeerInfo
}

// eventPeerConnected reports a successful handshake. The actor registers the
// connection and starts pumping its events/blocks.
type eventPeerConnected struct {
	pc *peerConn
}

// eventDialFailed clears a dialing reservation after a failed connection
// attempt.
type eventDialFailed struct {
	addr string
}

// eventPeerBlock is a received data block.
type eventPeerBlock struct {
	pc    *peerConn
	block *peer.PieceBlock
}

// eventPeerControl wraps a peer.Event (Have/Bitfield/choke/interest).
type eventPeerControl struct {
	pc *peerConn
	ev peer.Event
}

// eventPeerGone reports that a peer's connection ended, for any reason.
type eventPeerGone struct {
	pc *peerConn
}

// eventPieceVerified reports the outcome of hashing a piece that just
// received its last block.
type eventPieceVerified struct {
	index int
	ok    bool
	err   error
}

// eventTrackerPeers delivers the peers from one successful announce.
type eventTrackerPeers struct {
	peers []tracker.PeerInfo
}
