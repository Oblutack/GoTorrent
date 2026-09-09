package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestAllowHostsAcceptsListedHost(t *testing.T) {
	h := AllowHosts([]string{"127.0.0.1", "localhost"})(okHandler())

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:6880/healthz", nil)
	req.Host = "127.0.0.1:6880"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an allowed host", rec.Code)
	}
}

// TestAllowHostsRejectsUnlistedHost is the actual DNS-rebinding defense
// under test: a request whose Host header names something other than
// what's configured (e.g. an attacker-controlled domain that resolves to
// 127.0.0.1) must never reach the wrapped handler.
func TestAllowHostsRejectsUnlistedHost(t *testing.T) {
	h := AllowHosts([]string{"127.0.0.1"})(okHandler())

	req := httptest.NewRequest(http.MethodGet, "http://evil.example.com/healthz", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a disallowed host", rec.Code)
	}
}

func TestAllowHostsStripsPortBeforeComparing(t *testing.T) {
	h := AllowHosts([]string{"localhost"})(okHandler())

	req := httptest.NewRequest(http.MethodGet, "http://localhost:6880/healthz", nil)
	req.Host = "localhost:6880"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 - the allowlist entry has no port, the request's Host does", rec.Code)
	}
}

func TestRequireBearerTokenAcceptsCorrectToken(t *testing.T) {
	h := RequireBearerToken("secret", nil)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for the correct token", rec.Code)
	}
}

func TestRequireBearerTokenRejectsWrongOrMissingToken(t *testing.T) {
	h := RequireBearerToken("secret", nil)(okHandler())

	cases := []struct {
		name string
		auth string
	}{
		{"wrong token", "Bearer nope"},
		{"missing header", ""},
		{"wrong scheme", "Basic secret"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.auth != "" {
				req.Header.Set("Authorization", c.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

// TestRequireBearerTokenHonorsLockout proves the limiter is actually wired
// in, not just accepted as a parameter: enough wrong-token attempts from
// one address must get 429s even for a request that never reaches the
// token comparison at all.
func TestRequireBearerTokenHonorsLockout(t *testing.T) {
	limiter := NewAuthFailureLimiter(2, time.Minute, time.Hour)
	h := RequireBearerToken("secret", limiter)(okHandler())

	req := func(auth string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "9.9.9.9:12345"
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		return r
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req("Bearer wrong"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i, rec.Code)
		}
	}

	// Third attempt, even with the CORRECT token, must be locked out.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req("Bearer secret"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 after tripping the lockout threshold", rec.Code)
	}
}

func TestNewHandlerChainsHostAllowlistBeforeToken(t *testing.T) {
	h := NewHandler(Config{
		Token:        "secret",
		AllowedHosts: []string{"127.0.0.1"},
	}, okHandler())

	// A disallowed host must be rejected even with zero Authorization
	// header at all - proves AllowHosts really does run before, not after,
	// RequireBearerToken (order matters: a rebinding attack should never
	// even reach token-checking logic).
	req := httptest.NewRequest(http.MethodGet, "http://evil.example.com/", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (Host allowlist) before any token check", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	req2.Host = "127.0.0.1"
	req2.Header.Set("Authorization", "Bearer secret")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an allowed host with the correct token", rec2.Code)
	}
}
