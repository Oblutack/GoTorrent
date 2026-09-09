package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/ws"
)

// sessionStatsInterval is how often the WebSocket stream pushes a
// sessionStats-kind message on top of whatever real Engine events arrive
// in between — ROADMAP.md's "1 Hz stat deltas".
const sessionStatsInterval = 1 * time.Second

// WSEvent is one message on the wire, WS /api/v1/events - engine.Event
// itself is not marshaled directly (same "an API response is a public
// contract, an internal type is free to change shape" reasoning as
// TorrentSummary not being engine.Summary reused). Only the fields
// relevant to Kind are populated; the rest are omitted.
type WSEvent struct {
	Kind       string        `json:"kind"`
	Time       time.Time     `json:"time"`
	InfoHash   string        `json:"infoHash,omitempty"`
	State      string        `json:"state,omitempty"`
	PeerAddr   string        `json:"peerAddr,omitempty"`
	PieceIndex *int          `json:"pieceIndex,omitempty"`
	Session    *SessionStats `json:"session,omitempty"`
}

func wsEventDTO(ev engine.Event) WSEvent {
	out := WSEvent{Kind: string(ev.Kind), Time: ev.Time}
	if !ev.InfoHash.IsZero() {
		out.InfoHash = ev.InfoHash.String()
	}
	switch ev.Kind {
	case engine.EventTorrentStateChanged:
		out.State = ev.State.String()
	case engine.EventPeerConnected, engine.EventPeerDisconnected:
		out.PeerAddr = ev.PeerAddr
	case engine.EventPieceVerified:
		index := ev.PieceIndex
		out.PieceIndex = &index
	}
	return out
}

// EventsHandler serves WS /api/v1/events: every real engine.Event
// (torrent added/removed/state-changed, peer connected/disconnected,
// piece verified) as it happens, interleaved with a sessionStats message
// once a second. The connection's own read side only ever has to see
// ping/pong (handled inside ws.Conn already) and the close handshake — a
// client is not expected to send this endpoint anything meaningful, so any
// actual message it does send is silently ignored rather than treated as
// a protocol error.
func EventsHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := ws.Upgrade(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer conn.Close()

		sub, cancel := e.Subscribe()
		defer cancel()

		// ReadMessage blocks, so it needs its own goroutine; its only job
		// here is noticing the connection died (a close frame, a read
		// error) so the write side below can stop promptly instead of
		// only finding out on its next failed write.
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()

		ticker := time.NewTicker(sessionStatsInterval)
		defer ticker.Stop()

		for {
			select {
			case <-closed:
				return
			case ev, ok := <-sub:
				if !ok {
					return
				}
				if !writeWSJSON(conn, wsEventDTO(ev)) {
					return
				}
			case <-ticker.C:
				stats := sessionStatsSnapshot(e)
				if !writeWSJSON(conn, WSEvent{Kind: "sessionStats", Time: time.Now(), Session: &stats}) {
					return
				}
			}
		}
	}
}

// writeWSJSON marshals v and writes it as one text frame, returning false
// (and leaving the caller to give up on this connection) on any failure -
// a write error almost always means the connection is already gone.
func writeWSJSON(conn *ws.Conn, v any) bool {
	data, err := json.Marshal(v)
	if err != nil {
		return true // a bad DTO is a bug worth not crashing over, not a reason to drop the connection
	}
	return conn.WriteJSON(data) == nil
}
