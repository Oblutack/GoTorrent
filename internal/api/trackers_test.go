package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTrackersHandlerReportsAnnounceResult(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "trackerhandler")
	tr, _ := e.Get(hash)

	// addTestTorrent's fixture points at a dead tracker (127.0.0.1:1), so
	// wait for the real, failed announce attempt to land rather than
	// asserting on an empty list.
	deadline := time.Now().Add(5 * time.Second)
	for len(tr.TrackerStatuses()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no tracker announce recorded within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/torrents/{hash}/trackers", TrackersHandler(e))

	rec := routedRequest(t, mux, http.MethodGet, "/api/v1/torrents/"+hash.String()+"/trackers")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got []TrackerEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d tracker entries, want 1", len(got))
	}
	if got[0].LastError == "" {
		t.Fatal("LastError is empty for a known-dead tracker")
	}
}

func TestTrackersHandlerUnknownHashReturns404(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/torrents/{hash}/trackers", TrackersHandler(e))

	unmanaged := "0000000000000000000000000000000000000000"
	rec := routedRequest(t, mux, http.MethodGet, "/api/v1/torrents/"+unmanaged+"/trackers")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAddTrackerHandlerAddsAndAnnounces(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "addtrackerhandler")
	tr, _ := e.Get(hash)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/torrents/{hash}/trackers", AddTrackerHandler(e))

	body, err := json.Marshal(AddTrackerRequest{URL: "http://127.0.0.1:2/announce"})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/"+hash.String()+"/trackers", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	if err := tr.Reannounce(); err != nil {
		t.Fatalf("Reannounce: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range tr.TrackerStatuses() {
			if s.URL == "http://127.0.0.1:2/announce" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("added tracker never appeared in TrackerStatuses(): %v", tr.TrackerStatuses())
}

func TestAddTrackerHandlerUnknownHashReturns404(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/torrents/{hash}/trackers", AddTrackerHandler(e))

	body, err := json.Marshal(AddTrackerRequest{URL: "http://example.com/announce"})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	unmanaged := "0000000000000000000000000000000000000000"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/"+unmanaged+"/trackers", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
