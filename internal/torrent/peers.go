package torrent

// PeerSnapshot is one connected peer's state, for a caller (4.2's GET
// .../peers route) that wants to see the live swarm rather than just the
// aggregate PeerCount Stats already reports.
type PeerSnapshot struct {
	Addr string
	// Outbound is true for a peer this client dialed itself (peerConn.
	// peerInfo is only ever populated for those — see its own doc comment
	// in torrent.go), false for one that connected in.
	Outbound       bool
	Downloaded     int64
	Uploaded       int64
	AmChoking      bool
	AmInterested   bool
	PeerChoking    bool
	PeerInterested bool
	// Progress is the fraction of pieces this peer has advertised having,
	// in [0, 1] — 0 if metadata (and so a piece count) isn't known yet.
	Progress float64
}

// Peers returns a snapshot of every currently connected peer. Safe to call
// from any goroutine; returns an empty slice (not an error) once the actor
// has already stopped, the same "atomics are the final answer" shape
// Stats uses.
func (t *Torrent) Peers() []PeerSnapshot {
	resp := make(chan []PeerSnapshot, 1)
	select {
	case t.control <- controlMsg{kind: ctrlPeers, peersReply: resp}:
	case <-t.done:
		return nil
	}
	select {
	case peers := <-resp:
		return peers
	case <-t.done:
		return nil
	}
}

// peersSnapshot builds the Peers() result. Actor-only: t.peers is
// actor-owned, so this must run on the actor goroutine (handleControl).
func (t *Torrent) peersSnapshot() []PeerSnapshot {
	out := make([]PeerSnapshot, 0, len(t.peers))
	for _, pc := range t.peers {
		var progress float64
		if info := pc.client.BitfieldSnapshot(); info != nil && info.Len() > 0 {
			progress = float64(info.Count()) / float64(info.Len())
		}
		out = append(out, PeerSnapshot{
			Addr:           pc.addr,
			Outbound:       pc.peerInfo.Port != 0,
			Downloaded:     pc.downloaded.Load(),
			Uploaded:       pc.client.Uploaded(),
			AmChoking:      pc.client.AmChoking(),
			AmInterested:   pc.client.AmInterested(),
			PeerChoking:    pc.client.PeerChoking(),
			PeerInterested: pc.client.PeerInterested(),
			Progress:       progress,
		})
	}
	return out
}
