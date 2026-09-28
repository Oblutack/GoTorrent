package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// scrapeTimeout bounds how long ScrapeHandler waits on the trackers it
// asks — a dead UDP tracker's own retry/backoff schedule can otherwise run
// for a very long time (see internal/tracker's udpRoundTrip), and this is
// a synchronous, user-triggered request expecting a quick answer, not a
// background loop.
const scrapeTimeout = 20 * time.Second

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

// AddTrackerRequest is POST /api/v1/torrents/{hash}/trackers's body.
type AddTrackerRequest struct {
	URL string `json:"url"`
}

// AddTrackerHandler serves POST /api/v1/torrents/{hash}/trackers, a thin
// wrapper around Torrent.AddTracker (3.6, already exposed as a runtime
// capability, just never reachable over the control API until now). The
// new tracker's own first announce result shows up in a later
// GET .../trackers poll (or the next torrentStateChanged-adjacent event),
// not in this response - AddTracker itself only reports whether the URL
// was accepted, not any announce outcome yet to happen.
func AddTrackerHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash, ok := parseHashParam(w, r)
		if !ok {
			return
		}
		if _, ok := e.GetSummary(hash); !ok {
			writeError(w, http.StatusNotFound, "torrent not managed by this engine")
			return
		}

		var req AddTrackerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "decoding request body: "+err.Error())
			return
		}

		if err := e.AddTracker(hash, req.URL); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"url": req.URL})
	}
}

// ScrapeEntry is one tracker's scrape result, as reported by
// POST /api/v1/torrents/{hash}/scrape. Complete/Incomplete/Downloaded are
// only meaningful when Error is empty — a tracker that failed or doesn't
// support scraping (e.g. its announce URL doesn't follow the scrape URL
// convention) reports Error instead, rather than the request as a whole
// failing over one bad tracker among several.
type ScrapeEntry struct {
	URL        string `json:"url"`
	Complete   int    `json:"complete,omitempty"`
	Incomplete int    `json:"incomplete,omitempty"`
	Downloaded int    `json:"downloaded,omitempty"`
	Error      string `json:"error,omitempty"`
}

func scrapeEntryDTO(r torrent.TorrentScrapeResult) ScrapeEntry {
	entry := ScrapeEntry{URL: r.URL}
	if r.Err != nil {
		entry.Error = r.Err.Error()
		return entry
	}
	entry.Complete = r.Result.Complete
	entry.Incomplete = r.Result.Incomplete
	entry.Downloaded = r.Result.Downloaded
	return entry
}

// ScrapeHandler serves POST /api/v1/torrents/{hash}/scrape: an on-demand
// swarm-statistics refresh (BEP 48 for HTTP(S) trackers, BEP 15's scrape
// action for UDP ones) that, unlike GET .../trackers, doesn't wait for the
// next scheduled announce and carries a real all-time "downloaded" count
// no announce response has ever provided. Every tracker this torrent
// knows about is asked in parallel and reported independently — a torrent
// with five trackers where one is dead still returns four real answers
// plus one error entry, not a single failed request.
func ScrapeHandler(e *engine.Engine) http.HandlerFunc {
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

		ctx, cancel := context.WithTimeout(r.Context(), scrapeTimeout)
		defer cancel()

		results := tr.Scrape(ctx)
		out := make([]ScrapeEntry, len(results))
		for i, res := range results {
			out[i] = scrapeEntryDTO(res)
		}
		writeJSON(w, http.StatusOK, out)
	}
}
