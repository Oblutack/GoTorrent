package api

import (
	"net/http"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// PeerEntry is one connected peer, as reported by GET
// /api/v1/torrents/{hash}/peers.
type PeerEntry struct {
	Addr           string  `json:"addr"`
	Outbound       bool    `json:"outbound"`
	Downloaded     int64   `json:"downloaded"`
	Uploaded       int64   `json:"uploaded"`
	AmChoking      bool    `json:"amChoking"`
	AmInterested   bool    `json:"amInterested"`
	PeerChoking    bool    `json:"peerChoking"`
	PeerInterested bool    `json:"peerInterested"`
	Progress       float64 `json:"progress"`
	// PeerID is the remote's BEP 20 handshake peer ID, lowercase hex -
	// decoding it into a display name ("qBittorrent 4.6.5") is left to the
	// caller, same reasoning as torrent.PeerSnapshot.PeerID's own doc
	// comment.
	PeerID string `json:"peerId"`
}

func peerEntryDTO(p torrent.PeerSnapshot) PeerEntry {
	return PeerEntry{
		Addr:           p.Addr,
		Outbound:       p.Outbound,
		Downloaded:     p.Downloaded,
		Uploaded:       p.Uploaded,
		AmChoking:      p.AmChoking,
		AmInterested:   p.AmInterested,
		PeerChoking:    p.PeerChoking,
		PeerInterested: p.PeerInterested,
		Progress:       p.Progress,
		PeerID:         p.PeerID,
	}
}

// PeersHandler serves GET /api/v1/torrents/{hash}/peers.
func PeersHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash, ok := parseHashParam(w, r)
		if !ok {
			return
		}
		tr, ok := e.Get(hash)
		if !ok {
			writeError(w, http.StatusNotFound, "torrent not managed by this engine")
			return
		}

		snapshot := tr.Peers()
		out := make([]PeerEntry, len(snapshot))
		for i, p := range snapshot {
			out[i] = peerEntryDTO(p)
		}
		writeJSON(w, http.StatusOK, out)
	}
}
