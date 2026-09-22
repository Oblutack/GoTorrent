// Package debugserver exposes Go's own runtime instrumentation — pprof
// CPU/heap/goroutine profiles, plus a small JSON stats endpoint a caller
// can publish arbitrary named values to — on a private HTTP server.
//
// Deliberately never shares a mux with internal/api's control API: a
// CPU or heap profile can reveal memory addresses and full stack traces
// (including, in principle, the contents of in-flight requests), and a
// caller's own published stats might include things (real file paths,
// internal configuration) that have no business being reachable wherever
// the control API is exposed. Binding this to a *different* address
// (typically 127.0.0.1, never the control API's own listen address) is
// the operator's job — this package just never assumes the two should be
// the same server.
package debugserver

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/pprof"
	"sync"
)

// Server is a bound, not-yet-serving debug HTTP server.
type Server struct {
	ln  net.Listener
	srv *http.Server

	mu   sync.Mutex
	vars map[string]func() any
}

// New binds addr immediately — the same "fail at startup, not on the
// first request" shape engine.Listen already uses — and wires up
// /debug/pprof/* (net/http/pprof's own handler functions, called
// directly rather than relying on that package's init()-time
// registration onto http.DefaultServeMux, which this type deliberately
// never touches) and /debug/vars (this package's own JSON stats
// endpoint, see Publish).
func New(addr string) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln, vars: make(map[string]func() any)}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/vars", s.serveVars)

	s.srv = &http.Server{Handler: mux}
	return s, nil
}

// Addr is the real bound address — useful when New was given ":0" or a
// bare port and the caller wants to report (or test against) what the OS
// actually picked.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Publish registers name on /debug/vars, backed by fn — called fresh on
// every request, the same "always current, not a stale snapshot"
// contract engine.Summary/torrent.Stats already follow elsewhere in this
// project. Unlike the standard library's own expvar.Publish, this is
// scoped to one *Server instance rather than a single process-wide
// global registry, so it never panics on a name reused across two
// independent Servers (every test in this package constructs its own) —
// registering the same name twice on the *same* Server simply replaces
// the earlier callback, which is the more forgiving, still-correct
// choice for a debug endpoint nothing else in this codebase depends on
// for correctness.
func (s *Server) Publish(name string, fn func() any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vars[name] = fn
}

func (s *Server) serveVars(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	snapshot := make(map[string]any, len(s.vars))
	for name, fn := range s.vars {
		snapshot[name] = fn()
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(snapshot)
}

// Serve blocks, accepting connections until Close or Shutdown ends it —
// the same http.Server.Serve contract, just against the listener New
// already bound. A normal shutdown (ErrServerClosed) is reported as a
// nil error, matching how a caller almost always wants to treat it: "we
// asked this to stop" is not a failure worth logging as one.
func (s *Server) Serve() error {
	err := s.srv.Serve(s.ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close shuts the server down immediately, dropping any in-flight
// request — a profile capture (-profile, -trace) can take real seconds,
// so a caller that wants to let one finish should use Shutdown instead.
func (s *Server) Close() error { return s.srv.Close() }

// Shutdown stops accepting new connections and waits for in-flight ones
// to finish (or ctx to expire), the graceful counterpart to Close.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }
