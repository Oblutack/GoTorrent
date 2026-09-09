package api

import (
	"encoding/json"
	"net/http"

	"github.com/Oblutack/GoTorrent/internal/engine"
)

// PatchTorrentRequest is PATCH /api/v1/torrents/{hash}'s body - every field
// a pointer, so "absent from the JSON" (leave alone) is distinguishable
// from "explicitly set to the zero value" (e.g. Category: "" to clear it,
// or DownLimitKB: 0 to remove a limit). Fields are applied independently
// and in the order below; a failure partway through still leaves whatever
// already-applied fields took effect (there is no combined transaction
// across these engine calls, since they touch unrelated pieces of state -
// changing category doesn't roll back because a save-path move failed
// afterward, and shouldn't).
type PatchTorrentRequest struct {
	Category      *string   `json:"category,omitempty"`
	Tags          *[]string `json:"tags,omitempty"`
	DownLimitKB   *int64    `json:"downLimitKB,omitempty"`
	UpLimitKB     *int64    `json:"upLimitKB,omitempty"`
	QueuePosition *int      `json:"queuePosition,omitempty"`
	ForceStart    *bool     `json:"forceStart,omitempty"`
	// DownloadDir moves the torrent's content root via engine.MoveData -
	// synchronous, and can take a while for a large torrent (it stops the
	// torrent, renames the directory, and restarts it).
	DownloadDir *string `json:"downloadDir,omitempty"`
}

// PatchTorrentHandler serves PATCH /api/v1/torrents/{hash}.
func PatchTorrentHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash, ok := parseHashParam(w, r)
		if !ok {
			return
		}
		if _, ok := e.GetSummary(hash); !ok {
			writeError(w, http.StatusNotFound, "torrent not managed by this engine")
			return
		}

		var req PatchTorrentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "decoding request body: "+err.Error())
			return
		}

		if req.Category != nil {
			if err := e.SetCategory(hash, *req.Category); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if req.Tags != nil {
			if err := e.SetTags(hash, *req.Tags); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if req.DownLimitKB != nil || req.UpLimitKB != nil {
			down, up, ok := e.TorrentRateLimit(hash)
			if !ok {
				writeError(w, http.StatusNotFound, "torrent not managed by this engine")
				return
			}
			if req.DownLimitKB != nil {
				down = *req.DownLimitKB * 1024
			}
			if req.UpLimitKB != nil {
				up = *req.UpLimitKB * 1024
			}
			if err := e.SetTorrentRateLimit(hash, down, up); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if req.QueuePosition != nil {
			if err := e.SetQueuePosition(hash, *req.QueuePosition); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if req.ForceStart != nil {
			if err := e.SetForceStart(hash, *req.ForceStart); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if req.DownloadDir != nil {
			if err := e.MoveData(hash, *req.DownloadDir); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}

		summary, ok := e.GetSummary(hash)
		if !ok {
			// MoveData above stops and rebuilds the torrent under a new hash
			// key only if the infohash itself changed, which it never does -
			// this is here purely so a genuinely surprising disappearance
			// (e.g. a concurrent DELETE racing this request) is reported
			// honestly rather than as a 200 with a stale body.
			writeError(w, http.StatusNotFound, "torrent no longer managed by this engine")
			return
		}
		writeJSON(w, http.StatusOK, summaryDTO(summary))
	}
}
