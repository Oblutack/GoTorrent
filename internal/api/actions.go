package api

import (
	"net/http"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// torrentAction wraps one of Torrent's control methods (Pause/Resume/
// Recheck/Reannounce) as an http.HandlerFunc: resolve {hash}, call fn, map
// its error (if any) to a response. All four of ROADMAP.md's
// "POST .../{pause|resume|verify|reannounce}" routes are this same shape,
// just a different fn and success message.
func torrentAction(e *engine.Engine, fn func(*torrent.Torrent) error) http.HandlerFunc {
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
		if err := fn(tr); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// PauseHandler serves POST /api/v1/torrents/{hash}/pause.
func PauseHandler(e *engine.Engine) http.HandlerFunc {
	return torrentAction(e, (*torrent.Torrent).Pause)
}

// ResumeHandler serves POST /api/v1/torrents/{hash}/resume.
func ResumeHandler(e *engine.Engine) http.HandlerFunc {
	return torrentAction(e, (*torrent.Torrent).Resume)
}

// VerifyHandler serves POST /api/v1/torrents/{hash}/verify - Torrent's own
// method is named Recheck; "verify" is the route name ROADMAP.md uses, so
// that's what's exposed on the wire.
func VerifyHandler(e *engine.Engine) http.HandlerFunc {
	return torrentAction(e, (*torrent.Torrent).Recheck)
}

// ReannounceHandler serves POST /api/v1/torrents/{hash}/reannounce.
func ReannounceHandler(e *engine.Engine) http.HandlerFunc {
	return torrentAction(e, (*torrent.Torrent).Reannounce)
}

// DeleteTorrentHandler serves DELETE /api/v1/torrents/{hash}, honoring
// ?deleteData=true to also remove the downloaded content
// (engine.RemoveAndDeleteData) rather than just dropping the torrent from
// the fleet (engine.Remove, which leaves whatever was downloaded in
// place). Managed-or-not is checked up front via GetSummary, since neither
// Remove nor RemoveAndDeleteData exposes a distinct sentinel for "not
// managed" a handler could match with errors.Is - any error either returns
// past that point (a refused no-subfolder multi-file delete, a disk
// failure) is reported as 400, the simplest honest answer without engine
// exposing enough to tell those cases apart itself.
func DeleteTorrentHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash, ok := parseHashParam(w, r)
		if !ok {
			return
		}
		if _, ok := e.GetSummary(hash); !ok {
			writeError(w, http.StatusNotFound, "torrent not managed by this engine")
			return
		}

		deleteData := r.URL.Query().Get("deleteData")
		var err error
		if deleteData == "true" || deleteData == "1" {
			err = e.RemoveAndDeleteData(hash)
		} else {
			err = e.Remove(hash)
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
