package api

import (
	"net/http"
	"time"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// TrackerEntry is one tracker's most recent announce result, as reported
// by GET /api/v1/torrents/{hash}/trackers.
type TrackerEntry struct {
	URL          string    `json:"url"`
	LastAnnounce time.Time `json:"lastAnnounce"`
	LastError    string    `json:"lastError,omitempty"`
	Seeders      int       `json:"seeders"`
	Leechers     int       `json:"leechers"`
}

func trackerEntryDTO(s torrent.TrackerStatus) TrackerEntry {
	return TrackerEntry{
		URL:          s.URL,
		LastAnnounce: s.LastAnnounce,
		LastError:    s.LastError,
		Seeders:      s.Seeders,
		Leechers:     s.Leechers,
	}
}

// TrackersHandler serves GET /api/v1/torrents/{hash}/trackers. A tracker
// this torrent has never actually announced to yet (no metadata, or one
// added via AddTracker that hasn't had its first attempt) simply does not
// appear - there is nothing to report about it beyond its URL, and the
// URL alone belongs on GET .../files-adjacent metadata, not here.
func TrackersHandler(e *engine.Engine) http.HandlerFunc {
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

		statuses := tr.TrackerStatuses()
		out := make([]TrackerEntry, len(statuses))
		for i, s := range statuses {
			out[i] = trackerEntryDTO(s)
		}
		writeJSON(w, http.StatusOK, out)
	}
}
