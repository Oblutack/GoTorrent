package torrent

import (
	"net"
	"time"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/picker"
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
	ctrlSetFilePriority
	ctrlAddTracker
	ctrlReannounce
	ctrlSetSequential
	ctrlStats
	ctrlPeers
	ctrlSetSuperSeeding
	ctrlSetFirstLastPieceFirst
	ctrlSetSeedLimits
	ctrlSetStreamPosition
	ctrlApplyExternalPiece
)

type controlMsg struct {
	kind controlKind

	// metadata is set for ctrlSetMetadata.
	metadata *metainfo.MetaInfo
	// fileIndex and priority are set for ctrlSetFilePriority.
	fileIndex int
	priority  picker.Priority
	// trackerURL is set for ctrlAddTracker.
	trackerURL string
	// sequential is set for ctrlSetSequential.
	sequential bool
	// superSeeding is set for ctrlSetSuperSeeding.
	superSeeding bool
	// firstLastPieceFirst is set for ctrlSetFirstLastPieceFirst.
	firstLastPieceFirst bool
	// seedRatioLimit and seedTimeLimit are set for ctrlSetSeedLimits - a
	// nil pointer means "leave this one alone," the same partial-update
	// convention PatchTorrentOptions already uses at the API layer.
	seedRatioLimit *float64
	seedTimeLimit  *time.Duration
	// streamByteOffset is set for ctrlSetStreamPosition - a byte offset into
	// the torrent's flat content space (the same addressing ReadAt and peer
	// upload requests already use).
	streamByteOffset int64
	// externalPieceIndex and externalPieceData are set for ctrlApplyExternalPiece.
	externalPieceIndex int
	externalPieceData  []byte

	// errReply receives the result of Pause/Resume/Recheck/SetMetadata/
	// SetFilePriority/AddTracker/Reannounce/SetSequential/SetSuperSeeding/
	// SetFirstLastPieceFirst/SetSeedLimits/SetStreamPosition/
	// ApplyExternalPiece.
	errReply chan error
	// statsReply receives the actor-owned half of a Stats snapshot.
	statsReply chan Stats
	// peersReply receives Peers' result.
	peersReply chan []PeerSnapshot
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
// attempt or a rejected inbound one.
type eventDialFailed struct {
	addr string
}

// eventIncomingPeer hands the actor a connection accepted by the engine's
// shared listener. The engine has already read hs — it had to, to learn the
// infohash and route the connection to this torrent in the first place — so
// only the dedup/cap check and the reply handshake remain, both of which
// must happen from the actor (dedup) or a spawned goroutine (the reply, to
// avoid blocking run() on network I/O).
type eventIncomingPeer struct {
	conn net.Conn
	hs   *peer.Handshake
}

// eventPeerBlock is a received data block.
type eventPeerBlock struct {
	pc    *peerConn
	block *peer.PieceBlock
}

// eventPeerControl wraps a peer.Event (Have/Bitfield/choke/interest,
// extended handshake, metadata reject).
type eventPeerControl struct {
	pc *peerConn
	ev peer.Event
}

// eventMetadataPiece is one arrived BEP 9 metadata chunk.
type eventMetadataPiece struct {
	pc    *peerConn
	piece peer.MetadataPiece
}

// eventPEXUpdate is one arrived BEP 11 peer-exchange message.
type eventPEXUpdate struct {
	pc     *peerConn
	update peer.PEXUpdate
}

// eventPeerGone reports that a peer's connection ended, for any reason.
type eventPeerGone struct {
	pc *peerConn
}

// eventPieceVerified reports the outcome of hashing a piece that just
// received its last block. peerAddr is whichever peer delivered that last
// block — the piece may well have gathered earlier blocks from other peers
// too (endgame, or a peer that disconnected mid-piece), but "who delivered
// the block that completed it" is the one well-defined single attribution
// available without tracking per-peer-per-block assignment, which the actor
// deliberately does not do (see cancelDuplicates' own doc comment).
type eventPieceVerified struct {
	index    int
	ok       bool
	err      error
	peerAddr string
}

// eventTrackerPeers delivers the peers from one successful announce.
type eventTrackerPeers struct {
	peers []tracker.PeerInfo
}
