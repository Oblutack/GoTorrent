package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSessionHandlerAggregatesAcrossTorrents(t *testing.T) {
	e := newTestEngine(t)
	addTestTorrent(t, e, "a")
	addTestTorrent(t, e, "b")

	rec := doRequest(t, SessionHandler(e), http.MethodGet, "/api/v1/session")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got SessionStats
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.TorrentCount != 2 {
		t.Fatalf("TorrentCount = %d, want 2", got.TorrentCount)
	}
	// Both torrents point at a dead tracker and have no peers, so they sit
	// in Downloading (verified-missing state) rather than Seeding.
	if got.DownloadingCount != 2 {
		t.Fatalf("DownloadingCount = %d, want 2", got.DownloadingCount)
	}
	if got.SeedingCount != 0 || got.PausedCount != 0 || got.ErrorCount != 0 {
		t.Fatalf("unexpected non-zero bucket: %+v", got)
	}
}

func TestSessionHandlerEmptyFleet(t *testing.T) {
	e := newTestEngine(t)
	rec := doRequest(t, SessionHandler(e), http.MethodGet, "/api/v1/session")

	var got SessionStats
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.TorrentCount != 0 {
		t.Fatalf("TorrentCount = %d, want 0", got.TorrentCount)
	}
}

// TestSessionHandlerReportsAltSpeedEnabled proves GET /api/v1/session
// reflects the engine's real alt-speed state on the very first load, not
// just PatchSessionHandler's own response - a status bar has to be able
// to show the toggle's current state before it has ever PATCHed anything.
func TestSessionHandlerReportsAltSpeedEnabled(t *testing.T) {
	e := newTestEngine(t)
	e.SetAltSpeedEnabled(true)

	rec := doRequest(t, SessionHandler(e), http.MethodGet, "/api/v1/session")
	var got SessionStats
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.AltSpeedEnabled {
		t.Fatal("AltSpeedEnabled = false, want true (set directly on the engine before the request)")
	}
}
