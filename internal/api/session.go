package api

import (
	"net/http"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// SessionStats is GET /api/v1/session's response: a fleet-wide rollup
// computed from List() rather than tracked separately, since engine.Engine
// keeps no running aggregate of its own and every one of these numbers is
// cheap to sum over a torrent count any single machine will ever manage.
type SessionStats struct {
	TorrentCount     int   `json:"torrentCount"`
	DownloadingCount int   `json:"downloadingCount"`
	SeedingCount     int   `json:"seedingCount"`
	PausedCount      int   `json:"pausedCount"`
	ErrorCount       int   `json:"errorCount"`
	TotalDownloaded  int64 `json:"totalDownloaded"`
	TotalUploaded    int64 `json:"totalUploaded"`
	TotalPeerCount   int   `json:"totalPeerCount"`
}

// sessionStatsSnapshot computes the current fleet-wide rollup — shared by
// SessionHandler and the WebSocket event stream's 1Hz stats tick
// (events.go), so the two never drift apart into two slightly different
// definitions of "session stats."
func sessionStatsSnapshot(e *engine.Engine) SessionStats {
	list := e.List()
	stats := SessionStats{TorrentCount: len(list)}
	for _, s := range list {
		stats.TotalDownloaded += s.Stats.Downloaded
		stats.TotalUploaded += s.Stats.Uploaded
		stats.TotalPeerCount += s.Stats.PeerCount
		switch s.Stats.State {
		case torrent.StateSeeding:
			stats.SeedingCount++
		case torrent.StateFetchingMetadata, torrent.StateCheckingFiles, torrent.StateDownloading:
			stats.DownloadingCount++
		case torrent.StateError:
			stats.ErrorCount++
		default: // StatePaused, plus the momentary StateAdded before the
			// actor's first tick - not truly "paused", but there is no
			// fifth bucket for a state a client should essentially never
			// observe in practice.
			stats.PausedCount++
		}
	}
	return stats
}

// SessionHandler serves GET /api/v1/session. PATCH /api/v1/session
// (limits, port, DHT/PEX/LSD toggles) is a 4.2 mutation route, not built
// yet - this is read-only.
func SessionHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, sessionStatsSnapshot(e))
	}
}
