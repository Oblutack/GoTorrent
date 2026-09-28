package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
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

// TestScrapeHandlerReportsBothARealTrackerAndADeadOne drives ScrapeHandler
// against a torrent with two trackers - addTestTorrent's own fixture
// (a dead one at 127.0.0.1:1, never listening) plus a real httptest
// server that answers BEP 48 scrape requests - proving the route reaches
// Torrent.Scrape, that a real tracker's data comes back correctly shaped,
// and that the dead one's failure surfaces as its own entry rather than
// failing the whole request.
func TestScrapeHandlerReportsBothARealTrackerAndADeadOne(t *testing.T) {
	type scrapeFileWire struct {
		Complete   int64 `bencode:"complete"`
		Downloaded int64 `bencode:"downloaded"`
		Incomplete int64 `bencode:"incomplete"`
	}
	type scrapeResponseWire struct {
		Files map[string]scrapeFileWire `bencode:"files"`
	}

	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "scrapehandler")

	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hashes := r.URL.Query()["info_hash"]
		if len(hashes) != 1 {
			t.Fatalf("scrape request carried %d info_hash params, want 1", len(hashes))
		}
		data, err := bencode.Marshal(scrapeResponseWire{
			Files: map[string]scrapeFileWire{hashes[0]: {Complete: 6, Downloaded: 20, Incomplete: 2}},
		})
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		w.Write(data)
	}))
	defer live.Close()

	if err := e.AddTracker(hash, live.URL+"/announce"); err != nil {
		t.Fatalf("AddTracker: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/torrents/{hash}/scrape", ScrapeHandler(e))

	rec := routedRequest(t, mux, http.MethodPost, "/api/v1/torrents/"+hash.String()+"/scrape")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var got []ScrapeEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d scrape entries, want 2: %+v", len(got), got)
	}

	byURL := make(map[string]ScrapeEntry, len(got))
	for _, entry := range got {
		byURL[entry.URL] = entry
	}

	liveEntry, ok := byURL[live.URL+"/announce"]
	if !ok {
		t.Fatalf("no entry for the live tracker: %+v", got)
	}
	if liveEntry.Error != "" {
		t.Fatalf("live tracker entry has an error: %q", liveEntry.Error)
	}
	if liveEntry.Complete != 6 || liveEntry.Downloaded != 20 || liveEntry.Incomplete != 2 {
		t.Fatalf("live tracker entry = %+v, want Complete=6 Downloaded=20 Incomplete=2", liveEntry)
	}

	deadEntry, ok := byURL["http://127.0.0.1:1/announce"]
	if !ok {
		t.Fatalf("no entry for the dead tracker: %+v", got)
	}
	if deadEntry.Error == "" {
		t.Fatal("dead tracker entry has no error")
	}
}

func TestScrapeHandlerUnknownHashReturns404(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/torrents/{hash}/scrape", ScrapeHandler(e))

	unmanaged := "0000000000000000000000000000000000000000"
	rec := routedRequest(t, mux, http.MethodPost, "/api/v1/torrents/"+unmanaged+"/scrape")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
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
