package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func patchRequest(t *testing.T, mux *http.ServeMux, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(data))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPatchTorrentHandlerSetsCategoryAndTags(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "patchable")

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/torrents/{hash}", PatchTorrentHandler(e))

	rec := patchRequest(t, mux, "/api/v1/torrents/"+hash.String(), PatchTorrentRequest{
		Category: strPtr("movies"),
		Tags:     &[]string{"a", "b"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var got TorrentSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Category != "movies" {
		t.Fatalf("Category = %q, want movies", got.Category)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "a" || got.Tags[1] != "b" {
		t.Fatalf("Tags = %v, want [a b]", got.Tags)
	}
}

func TestPatchTorrentHandlerSetsOneRateLimitDirectionAtATime(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "ratepatched")

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/torrents/{hash}", PatchTorrentHandler(e))

	// Set only the download limit first.
	rec := patchRequest(t, mux, "/api/v1/torrents/"+hash.String(), PatchTorrentRequest{
		DownLimitKB: int64Ptr(100),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	down, up, ok := e.TorrentRateLimit(hash)
	if !ok || down != 100*1024 || up != 0 {
		t.Fatalf("after setting only DownLimitKB: (%d, %d, %v), want (102400, 0, true)", down, up, ok)
	}

	// Now set only the upload limit - the download limit set above must
	// survive untouched, proving the read-then-merge in the handler works.
	rec = patchRequest(t, mux, "/api/v1/torrents/"+hash.String(), PatchTorrentRequest{
		UpLimitKB: int64Ptr(50),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	down, up, ok = e.TorrentRateLimit(hash)
	if !ok || down != 100*1024 || up != 50*1024 {
		t.Fatalf("after setting only UpLimitKB: (%d, %d, %v), want (102400, 51200, true) - DownLimitKB must survive", down, up, ok)
	}
}

func TestPatchTorrentHandlerSetsForceStartAndQueuePosition(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "queuepatched")

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/torrents/{hash}", PatchTorrentHandler(e))

	rec := patchRequest(t, mux, "/api/v1/torrents/"+hash.String(), PatchTorrentRequest{
		ForceStart: boolPtr(true),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got TorrentSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.ForceStart {
		t.Fatal("ForceStart was not applied")
	}
}

func TestPatchTorrentHandlerUnknownHashReturns404(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/torrents/{hash}", PatchTorrentHandler(e))

	unmanaged := "0000000000000000000000000000000000000000"
	rec := patchRequest(t, mux, "/api/v1/torrents/"+unmanaged, PatchTorrentRequest{Category: strPtr("x")})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func strPtr(s string) *string { return &s }
func int64Ptr(i int64) *int64 { return &i }
func boolPtr(b bool) *bool    { return &b }
