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
// uploadDir is where AddTorrentHandler writes an uploaded or URL-fetched
// .torrent file before handing its path to engine.AddWithOptions — see
// saveTorrentBytes for why that has to be a stable location, not a temp
// directory.
//
// Mutating routes still open: session PATCH (limits, port, DHT/PEX/LSD
// toggles). The WebSocket event stream needs new observer hooks on the
// torrent actor that don't exist yet.
func Routes(e *engine.Engine, userAgent, uploadDir string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", HealthHandler(userAgent))
	mux.HandleFunc("GET /api/v1/torrents", ListTorrentsHandler(e))
	mux.HandleFunc("POST /api/v1/torrents", AddTorrentHandler(e, uploadDir))
	mux.HandleFunc("GET /api/v1/torrents/{hash}", TorrentDetailHandler(e))
	mux.HandleFunc("GET /api/v1/torrents/{hash}/files", FilesHandler(e))
	mux.HandleFunc("POST /api/v1/torrents/{hash}/pause", PauseHandler(e))
	mux.HandleFunc("POST /api/v1/torrents/{hash}/resume", ResumeHandler(e))
	mux.HandleFunc("POST /api/v1/torrents/{hash}/verify", VerifyHandler(e))
	mux.HandleFunc("POST /api/v1/torrents/{hash}/reannounce", ReannounceHandler(e))
	mux.HandleFunc("PATCH /api/v1/torrents/{hash}", PatchTorrentHandler(e))
	mux.HandleFunc("DELETE /api/v1/torrents/{hash}", DeleteTorrentHandler(e))
	mux.HandleFunc("GET /api/v1/session", SessionHandler(e))
	return mux
}
