package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

// PatchFilePriorityRequest is PATCH /api/v1/torrents/{hash}/files/{index}'s
// body - just the one field, since a file's priority is the only thing this
// route lets a caller change about it.
type PatchFilePriorityRequest struct {
	Priority picker.Priority `json:"priority"`
}

// PatchFilePriorityHandler serves PATCH /api/v1/torrents/{hash}/files/{index},
// a thin wrap around engine.Engine.SetFilePriority (which itself wraps
// Torrent.SetFilePriority, see 3.2) - the write-side counterpart to
// FilesHandler, which already reports each file's current priority.
func PatchFilePriorityHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash, ok := parseHashParam(w, r)
		if !ok {
			return
		}
		index, err := strconv.Atoi(r.PathValue("index"))
		if err != nil || index < 0 {
			writeError(w, http.StatusBadRequest, "index must be a non-negative integer")
			return
		}
		if _, ok := e.GetSummary(hash); !ok {
			writeError(w, http.StatusNotFound, "torrent not managed by this engine")
			return
		}

		var req PatchFilePriorityRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "decoding request body: "+err.Error())
			return
		}

		if err := e.SetFilePriority(hash, index, req.Priority); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"index": index, "priority": req.Priority})
	}
}
