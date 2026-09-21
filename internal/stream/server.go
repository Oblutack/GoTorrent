package stream

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// Server serves streamable content for every torrent an *engine.Engine
// manages — the HTTP half of Phase 8's streaming mode. Handler is what a
// caller (cmd/gottrent's -stream flag) actually hands to http.ListenAndServe
// or http.Server.
type Server struct {
	e *engine.Engine
}

// NewServer wraps e. Nothing here starts a listener — that's the caller's
// job, same division of responsibility internal/api's own NewHandler uses.
func NewServer(e *engine.Engine) *Server {
	return &Server{e: e}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /stream/{hash}/{file}", s.handleStream)
	return mux
}

// fileEntry is one file's real geometry within its torrent's flat content
// space — resolved fresh from real metadata on every request, never
// cached, since a magnet's file list does not exist until metadata
// actually arrives.
type fileEntry struct {
	name   string
	offset int64
	length int64
}

func filesOf(mi *metainfo.MetaInfo) []fileEntry {
	if !mi.Info.IsMultiFile() {
		return []fileEntry{{name: mi.Info.Name, offset: 0, length: mi.Info.Length}}
	}
	out := make([]fileEntry, len(mi.Info.Files))
	var offset int64
	for i, f := range mi.Info.Files {
		out[i] = fileEntry{name: strings.Join(f.Path, "/"), offset: offset, length: f.Length}
		offset += f.Length
	}
	return out
}

// handleIndex lists every managed torrent's streamable files as plain
// links — enough to find a stream URL by hand (or point a script at) without
// needing the control API's own JSON routes just to discover one path.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, "<!DOCTYPE html><meta charset=\"utf-8\"><title>GoTorrent stream</title>"+
		"<h1>Streamable files</h1>")

	list := s.e.List()
	if len(list) == 0 {
		fmt.Fprint(w, "<p>No torrents managed yet.</p>")
		return
	}
	fmt.Fprint(w, "<ul>")
	for _, summary := range list {
		tr, ok := s.e.Get(summary.InfoHash)
		if !ok {
			continue
		}
		mi := tr.Metadata()
		fmt.Fprintf(w, "<li>%s", html.EscapeString(summary.Name))
		if mi == nil {
			fmt.Fprint(w, " <em>(metadata not available yet)</em></li>")
			continue
		}
		fmt.Fprint(w, "<ul>")
		for i, f := range filesOf(mi) {
			href := fmt.Sprintf("/stream/%s/%d", summary.InfoHash, i)
			fmt.Fprintf(w, "<li><a href=\"%s\">%s</a> (%d bytes)</li>", href, html.EscapeString(f.name), f.length)
		}
		fmt.Fprint(w, "</ul></li>")
	}
	fmt.Fprint(w, "</ul>")
}

// handleStream serves one file's bytes over HTTP, honoring Range requests
// via http.ServeContent. Enables sequential piece ordering on this torrent
// for as long as it's being streamed — best-effort, since a stream request
// is itself the strongest possible signal that playback order matters more
// than swarm health right now for this particular torrent.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	hash, err := metainfo.ParseHash(r.PathValue("hash"))
	if err != nil {
		http.Error(w, "invalid info hash", http.StatusBadRequest)
		return
	}
	tr, ok := s.e.Get(hash)
	if !ok {
		http.NotFound(w, r)
		return
	}
	mi := tr.Metadata()
	if mi == nil {
		http.Error(w, "metadata not available yet (magnet still fetching)", http.StatusServiceUnavailable)
		return
	}
	fileIndex, err := strconv.Atoi(r.PathValue("file"))
	if err != nil {
		http.Error(w, "invalid file index", http.StatusBadRequest)
		return
	}
	files := filesOf(mi)
	if fileIndex < 0 || fileIndex >= len(files) {
		http.NotFound(w, r)
		return
	}
	f := files[fileIndex]

	if err := tr.SetSequential(true); err != nil {
		logger.Warning.Printf("stream: enabling sequential mode for %s: %v\n", hash, err)
	}

	content := NewContent(r.Context(), tr, f.offset, f.length, mi.Info.PieceLength)
	http.ServeContent(w, r, f.name, time.Time{}, content)
}
