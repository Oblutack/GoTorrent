package api

import (
	"net/http"

	"github.com/Oblutack/GoTorrent/internal/storage"
)

// DiskSpaceResponse is GET /api/v1/diskspace's response body.
type DiskSpaceResponse struct {
	Path      string `json:"path"`
	FreeBytes int64  `json:"freeBytes"`
}

// DiskSpaceHandler serves GET /api/v1/diskspace?path=<dir> - reports free
// disk space at an arbitrary path, not just the fleet's own configured
// download directory (which GET /api/v1/session's freeDiskBytes field
// already covers, via engine.Engine.Info). A caller building a real
// disk-space guard before Add (Stage 6) needs this for whatever custom
// save path the user actually picked in the Add dialog, which the
// session endpoint has no way to express. Deliberately takes no
// *engine.Engine at all - the same "structurally incapable of touching
// the engine" shape PreviewTorrentHandler already uses, since querying
// free space for an arbitrary directory has nothing to do with the fleet.
func DiskSpaceHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			writeError(w, http.StatusBadRequest, `"path" query parameter is required`)
			return
		}
		free, err := storage.AvailableSpace(path)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, DiskSpaceResponse{Path: path, FreeBytes: free})
	}
}
