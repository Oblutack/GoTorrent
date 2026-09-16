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

// TestPatchSessionHandlerTogglesAltSpeed proves the runtime alt-speed
// toggle (Stage 5) reaches the real engine, both directions - reads the
// applied state back from the response itself rather than a separate
// GET, since PatchSessionHandler's own response already reports it.
func TestPatchSessionHandlerTogglesAltSpeed(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/session", PatchSessionHandler(e))

	body, _ := json.Marshal(PatchSessionRequest{AltSpeedEnabled: boolPtr(true)})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/session", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var got SessionPatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.AltSpeedEnabled {
		t.Fatal("AltSpeedEnabled = false after PATCHing true")
	}
	if !e.AltSpeedEnabled() {
		t.Fatal("engine's own AltSpeedEnabled() = false after PATCHing true")
	}

	body, _ = json.Marshal(PatchSessionRequest{AltSpeedEnabled: boolPtr(false)})
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/session", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.AltSpeedEnabled {
		t.Fatal("AltSpeedEnabled = true after PATCHing false")
	}
}

// TestPatchSessionHandlerLeavesAltSpeedAloneWhenAbsent proves the absent
// (nil) case: a request that only touches rate limits must not disturb
// whatever alt-speed state was already in effect.
func TestPatchSessionHandlerLeavesAltSpeedAloneWhenAbsent(t *testing.T) {
	e := newTestEngine(t)
	e.SetAltSpeedEnabled(true)

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/session", PatchSessionHandler(e))

	body, _ := json.Marshal(PatchSessionRequest{DownLimitKB: int64Ptr(500)})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/session", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var got SessionPatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.AltSpeedEnabled {
		t.Fatal("AltSpeedEnabled = false after a request that never mentioned it, want it to stay true")
	}
}

// TestPatchSessionHandlerReportsNormalNotLiveRateWhileAltSpeedActive is a
// real regression guard: caught live, not by a test, before this fix — the
// handler used to read the rate limits back via GlobalRateLimit (the live
// limiter, which setAltSpeed had just overwritten with the alt rate) into
// the very same response that also reports AltSpeedEnabled: true, so a
// client would see e.g. "100 KB/s, alt speed on" instead of the real
// normal cap "1000 KB/s, alt speed on" — and a naive client that later
// resubmitted that number unchanged would silently replace the normal cap
// with the alt one. DownLimitKB/UpLimitKB must always reflect
// NormalRateLimit, never the transiently-overridden live rate.
func TestPatchSessionHandlerReportsNormalNotLiveRateWhileAltSpeedActive(t *testing.T) {
	e := newTestEngine(t)
	e.SetGlobalRateLimit(1000*1024, 2000*1024)

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/session", PatchSessionHandler(e))

	body, _ := json.Marshal(PatchSessionRequest{AltSpeedEnabled: boolPtr(true)})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/session", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var got SessionPatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.AltSpeedEnabled {
		t.Fatal("AltSpeedEnabled = false right after PATCHing true")
	}
	if got.DownLimitKB != 1000 || got.UpLimitKB != 2000 {
		t.Fatalf("DownLimitKB/UpLimitKB = %d/%d, want the normal rate 1000/2000 preserved even while alt-speed is active", got.DownLimitKB, got.UpLimitKB)
	}

	down, up := e.GlobalRateLimit()
	if down == 1000*1024 || up == 2000*1024 {
		t.Fatalf("GlobalRateLimit (the live rate) = %d/%d, want it to actually have switched to the alt rate, not stayed at normal", down, up)
	}
}
