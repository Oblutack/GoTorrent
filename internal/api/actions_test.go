package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/torrent"
)

func routedRequest(t *testing.T, mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPauseResumeHandlersChangeState(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "action")
	tr, _ := e.Get(hash)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/torrents/{hash}/pause", PauseHandler(e))
	mux.HandleFunc("POST /api/v1/torrents/{hash}/resume", ResumeHandler(e))

	rec := routedRequest(t, mux, http.MethodPost, "/api/v1/torrents/"+hash.String()+"/pause")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pause status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for tr.State() != torrent.StatePaused {
		if time.Now().After(deadline) {
			t.Fatalf("torrent never reached Paused, stuck at %s", tr.State())
		}
		time.Sleep(5 * time.Millisecond)
	}

	rec = routedRequest(t, mux, http.MethodPost, "/api/v1/torrents/"+hash.String()+"/resume")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resume status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
	deadline = time.Now().Add(2 * time.Second)
	for tr.State() == torrent.StatePaused {
		if time.Now().After(deadline) {
			t.Fatal("torrent never left Paused after resume")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestVerifyHandlerTriggersRecheck(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "verifyme")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/torrents/{hash}/verify", VerifyHandler(e))

	rec := routedRequest(t, mux, http.MethodPost, "/api/v1/torrents/"+hash.String()+"/verify")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
}

func TestActionHandlersUnknownHashReturns404(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/torrents/{hash}/pause", PauseHandler(e))

	unmanaged := "0000000000000000000000000000000000000000"
	rec := routedRequest(t, mux, http.MethodPost, "/api/v1/torrents/"+unmanaged+"/pause")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestReannounceHandlerFailsOnPausedTorrent(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "reannounceme")
	tr, _ := e.Get(hash)
	if err := tr.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for tr.State() != torrent.StatePaused {
		if time.Now().After(deadline) {
			t.Fatalf("torrent never reached Paused, stuck at %s", tr.State())
		}
		time.Sleep(5 * time.Millisecond)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/torrents/{hash}/reannounce", ReannounceHandler(e))

	rec := routedRequest(t, mux, http.MethodPost, "/api/v1/torrents/"+hash.String()+"/reannounce")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a paused torrent, body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteTorrentHandlerRemovesFromFleet(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "deleteme")

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/torrents/{hash}", DeleteTorrentHandler(e))

	rec := routedRequest(t, mux, http.MethodDelete, "/api/v1/torrents/"+hash.String())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := e.Get(hash); ok {
		t.Fatal("torrent is still managed after DELETE")
	}
}

func TestDeleteTorrentHandlerUnknownHashReturns404(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/torrents/{hash}", DeleteTorrentHandler(e))

	unmanaged := "0000000000000000000000000000000000000000"
	rec := routedRequest(t, mux, http.MethodDelete, "/api/v1/torrents/"+unmanaged)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
