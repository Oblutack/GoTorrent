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
// PATCH /api/v1/session covers rate limits only, not the port/DHT/PEX/LSD
// toggles ROADMAP.md's sketch also mentions — see PatchSessionRequest's own
// doc comment for why those need their own design pass. GET /api/v1/events
// is a WebSocket upgrade (internal/ws, hand-rolled RFC 6455), not an
// ordinary JSON response — see EventsHandler.
func Routes(e *engine.Engine, userAgent, uploadDir string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", HealthHandler(userAgent))
	mux.HandleFunc("GET /api/v1/torrents", ListTorrentsHandler(e))
	mux.HandleFunc("POST /api/v1/torrents", AddTorrentHandler(e, uploadDir))
	mux.HandleFunc("GET /api/v1/torrents/{hash}", TorrentDetailHandler(e))
	mux.HandleFunc("GET /api/v1/torrents/{hash}/files", FilesHandler(e))
	mux.HandleFunc("GET /api/v1/torrents/{hash}/peers", PeersHandler(e))
	mux.HandleFunc("GET /api/v1/torrents/{hash}/trackers", TrackersHandler(e))
	mux.HandleFunc("GET /api/v1/torrents/{hash}/pieces", PiecesHandler(e))
	mux.HandleFunc("POST /api/v1/torrents/{hash}/pause", PauseHandler(e))
	mux.HandleFunc("POST /api/v1/torrents/{hash}/resume", ResumeHandler(e))
	mux.HandleFunc("POST /api/v1/torrents/{hash}/verify", VerifyHandler(e))
	mux.HandleFunc("POST /api/v1/torrents/{hash}/reannounce", ReannounceHandler(e))
	mux.HandleFunc("PATCH /api/v1/torrents/{hash}", PatchTorrentHandler(e))
	mux.HandleFunc("DELETE /api/v1/torrents/{hash}", DeleteTorrentHandler(e))
	mux.HandleFunc("GET /api/v1/session", SessionHandler(e))
	mux.HandleFunc("PATCH /api/v1/session", PatchSessionHandler(e))
	mux.HandleFunc("GET /api/v1/events", EventsHandler(e))
	return mux
}
