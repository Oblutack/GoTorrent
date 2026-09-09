package api

import (
	"net/http"

	"github.com/Oblutack/GoTorrent/internal/engine"
)

// Routes registers every route this package knows about on a fresh
// *http.ServeMux and returns it - unwrapped by NewHandler's security chain,
// so a caller (cmd/gottrentd) composes the two itself: Routes(e) is what
// changes as 4.2 grows more endpoints, NewHandler(cfg, ...) is what changes
// as 4.3 grows more hardening, and neither should have to know about the
// other's internals to do that.
//
// Read-only today (list, detail, files, session); the mutating routes in
// ROADMAP.md's 4.2 table (add, pause/resume/verify/reannounce, patch,
// delete, session PATCH) and the WebSocket event stream are not built yet.
func Routes(e *engine.Engine, userAgent string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", HealthHandler(userAgent))
	mux.HandleFunc("GET /api/v1/torrents", ListTorrentsHandler(e))
	mux.HandleFunc("GET /api/v1/torrents/{hash}", TorrentDetailHandler(e))
	mux.HandleFunc("GET /api/v1/torrents/{hash}/files", FilesHandler(e))
	mux.HandleFunc("GET /api/v1/session", SessionHandler(e))
	return mux
}
