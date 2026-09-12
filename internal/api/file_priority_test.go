package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/picker"
)

func TestPatchFilePriorityHandlerChangesPriority(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "patchpriority")
	tr, _ := e.Get(hash)

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/torrents/{hash}/files/{index}", PatchFilePriorityHandler(e))

	body, err := json.Marshal(PatchFilePriorityRequest{Priority: picker.PriorityHigh})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/torrents/"+hash.String()+"/files/0", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	got := tr.Stats().FilePriorities
	if len(got) != 1 || got[0] != picker.PriorityHigh {
		t.Fatalf("FilePriorities = %v, want [high]", got)
	}
}

func TestPatchFilePriorityHandlerUnknownHashReturns404(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/torrents/{hash}/files/{index}", PatchFilePriorityHandler(e))

	body, err := json.Marshal(PatchFilePriorityRequest{Priority: picker.PriorityHigh})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	unmanaged := "0000000000000000000000000000000000000000"
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/torrents/"+unmanaged+"/files/0", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestPatchFilePriorityHandlerOutOfRangeIndexReturns400(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "patchpriorityoor")

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/torrents/{hash}/files/{index}", PatchFilePriorityHandler(e))

	body, err := json.Marshal(PatchFilePriorityRequest{Priority: picker.PriorityHigh})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/torrents/"+hash.String()+"/files/7", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}
