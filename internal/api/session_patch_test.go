package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPatchSessionHandlerSetsGlobalRateLimit(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/session", PatchSessionHandler(e))

	body, err := json.Marshal(PatchSessionRequest{DownLimitKB: int64Ptr(500)})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/session", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got SessionPatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.DownLimitKB != 500 {
		t.Fatalf("DownLimitKB = %d, want 500", got.DownLimitKB)
	}

	down, _ := e.GlobalRateLimit()
	if down != 500*1024 {
		t.Fatalf("engine's actual GlobalRateLimit down = %d, want %d", down, 500*1024)
	}
}

func TestPatchSessionHandlerLeavesUnsetDirectionAlone(t *testing.T) {
	e := newTestEngine(t)
	e.SetGlobalRateLimit(100*1024, 200*1024)

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/session", PatchSessionHandler(e))

	body, _ := json.Marshal(PatchSessionRequest{UpLimitKB: int64Ptr(999)})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/session", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var got SessionPatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.DownLimitKB != 100 {
		t.Fatalf("DownLimitKB = %d, want 100 (untouched)", got.DownLimitKB)
	}
	if got.UpLimitKB != 999 {
		t.Fatalf("UpLimitKB = %d, want 999", got.UpLimitKB)
	}
}

func TestPatchSessionHandlerEmptyBodyReportsCurrentLimits(t *testing.T) {
	e := newTestEngine(t)
	e.SetGlobalRateLimit(1024, 2048)

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/session", PatchSessionHandler(e))

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/session", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var got SessionPatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.DownLimitKB != 1 || got.UpLimitKB != 2 {
		t.Fatalf("got %+v, want the unchanged current limits (1, 2)", got)
	}
}
