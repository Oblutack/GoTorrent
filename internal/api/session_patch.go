package api

import (
	"encoding/json"
	"net/http"

	"github.com/Oblutack/GoTorrent/internal/engine"
)

// PatchSessionRequest is PATCH /api/v1/session's body. Fleet-wide rate
// limits and the alt-speed on/off toggle today, of ROADMAP.md's "limits,
// port, DHT/PEX/LSD toggles" - changing the listen port or toggling
// DHT/PEX/LSD at runtime needs new engine-level control this package does
// not have yet (DHT/LSD are started once at daemon startup with no
// dynamic stop/restart exposed, and PEX has no engine-level switch at all
// — it's always on per-torrent), each its own real design pass rather
// than something to bolt on here. Pointer fields for the same "absent vs.
// explicitly zero" reason PatchTorrentRequest's fields are pointers.
type PatchSessionRequest struct {
	DownLimitKB *int64 `json:"downLimitKB,omitempty"`
	UpLimitKB   *int64 `json:"upLimitKB,omitempty"`
	// AltSpeedEnabled turns Defaults.AltDownLimit/AltUpLimit on or off
	// right now — see engine.Engine.SetAltSpeedEnabled. A manual toggle
	// here is a temporary override if Defaults.AltSchedule is also
	// configured: the schedule's own next tick re-asserts whatever it
	// thinks should be true.
	AltSpeedEnabled *bool `json:"altSpeedEnabled,omitempty"`
}

// SessionPatchResponse reports the fleet-wide normal rate limits (never
// the transient alt rate, even while AltSpeedEnabled is true — see
// engine.Engine.NormalRateLimit) and the alt-speed state actually in
// effect after applying the request.
type SessionPatchResponse struct {
	DownLimitKB     int64 `json:"downLimitKB"`
	UpLimitKB       int64 `json:"upLimitKB"`
	AltSpeedEnabled bool  `json:"altSpeedEnabled"`
}

// PatchSessionHandler serves PATCH /api/v1/session.
func PatchSessionHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req PatchSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "decoding request body: "+err.Error())
			return
		}

		// NormalRateLimit, not GlobalRateLimit: DownLimitKB/UpLimitKB must
		// always mean "the configured normal cap," even while alt-speed is
		// (or is about to be) overriding the live limiter with the alt
		// rate — otherwise a client that reads this back and later
		// resubmits it unchanged would silently clobber the normal cap
		// with whatever the alt rate happened to be at read time.
		down, up := e.NormalRateLimit()
		if req.DownLimitKB != nil {
			down = *req.DownLimitKB * 1024
		}
		if req.UpLimitKB != nil {
			up = *req.UpLimitKB * 1024
		}
		if req.DownLimitKB != nil || req.UpLimitKB != nil {
			e.SetGlobalRateLimit(down, up)
		}
		if req.AltSpeedEnabled != nil {
			e.SetAltSpeedEnabled(*req.AltSpeedEnabled)
		}

		writeJSON(w, http.StatusOK, SessionPatchResponse{
			DownLimitKB:     down / 1024,
			UpLimitKB:       up / 1024,
			AltSpeedEnabled: e.AltSpeedEnabled(),
		})
	}
}
