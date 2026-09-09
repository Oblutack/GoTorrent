package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPeersHandlerEmptyForAFreshTorrent(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "peerless")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/torrents/{hash}/peers", PeersHandler(e))

	rec := routedRequest(t, mux, http.MethodGet, "/api/v1/torrents/"+hash.String()+"/peers")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got []PeerEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d peers, want 0 (dead tracker, no real connections)", len(got))
	}
}

func TestPeersHandlerUnknownHashReturns404(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/torrents/{hash}/peers", PeersHandler(e))

	unmanaged := "0000000000000000000000000000000000000000"
	rec := routedRequest(t, mux, http.MethodGet, "/api/v1/torrents/"+unmanaged+"/peers")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
