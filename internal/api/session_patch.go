package api

import (
	"encoding/json"
	"net/http"

	"github.com/Oblutack/GoTorrent/internal/engine"
)

// PatchSessionRequest is PATCH /api/v1/session's body. Only fleet-wide
// rate limits today, of ROADMAP.md's "limits, port, DHT/PEX/LSD toggles" -
// changing the listen port or toggling DHT/PEX/LSD at runtime needs new
// engine-level control this package does not have yet (DHT/LSD are
// started once at daemon startup with no dynamic stop/restart exposed,
// and PEX has no engine-level switch at all — it's always on per-torrent),
// each its own real design pass rather than something to bolt on here.
// Pointer fields for the same "absent vs. explicitly zero" reason
// PatchTorrentRequest's fields are pointers.
type PatchSessionRequest struct {
	DownLimitKB *int64 `json:"downLimitKB,omitempty"`
	UpLimitKB   *int64 `json:"upLimitKB,omitempty"`
}

// SessionPatchResponse reports the fleet-wide limits actually in effect
// after applying the request.
type SessionPatchResponse struct {
	DownLimitKB int64 `json:"downLimitKB"`
	UpLimitKB   int64 `json:"upLimitKB"`
}

// PatchSessionHandler serves PATCH /api/v1/session.
func PatchSessionHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req PatchSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "decoding request body: "+err.Error())
			return
		}

		down, up := e.GlobalRateLimit()
		if req.DownLimitKB != nil {
			down = *req.DownLimitKB * 1024
		}
		if req.UpLimitKB != nil {
			up = *req.UpLimitKB * 1024
		}
		if req.DownLimitKB != nil || req.UpLimitKB != nil {
			e.SetGlobalRateLimit(down, up)
		}

		writeJSON(w, http.StatusOK, SessionPatchResponse{
			DownLimitKB: down / 1024,
			UpLimitKB:   up / 1024,
		})
	}
}
