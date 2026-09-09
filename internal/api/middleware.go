// Package api is gottrentd's control-API layer: 4.3's security hardening
// today (bearer-token auth, a Host-header allowlist against DNS rebinding,
// and a brute-force lockout on repeated auth failures), with the real
// REST/WebSocket routes of 4.2 meant to land in this same package once
// they exist, wrapped by the same middleware chain rather than bolted on
// after the fact.
//
// The threat model this package defends against (see Handler's doc
// comment for the full chain) is spelled out in ROADMAP.md's 4.3 section:
// a daemon bound to 127.0.0.1 is not automatically private. Any web page
// the user's browser visits can still have JavaScript issue a request to
// http://127.0.0.1:<port>/..., and a malicious DNS record resolving to
// 127.0.0.1 defeats a same-origin check based on hostname alone (DNS
// rebinding) - Transmission shipped CVEs in exactly this class. CORS
// headers alone do not close this: they only stop a browser from letting
// page JavaScript *read* the response, not from the request reaching the
// daemon and taking effect in the first place, which is why the Host
// header allowlist below - not CORS - is this package's primary defense.
package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// AllowHosts rejects any request whose Host header does not match one of
// allowed exactly (after stripping a port, since a browser-originated
// request's Host header always carries one). This is checked before
// anything else in the chain - including the bearer token - since its
// entire point is to stop a DNS-rebound request from ever reaching
// token-checking logic that a rebinding attack could otherwise probe.
func AllowHosts(allowed []string) func(http.Handler) http.Handler {
	set := make(map[string]bool, len(allowed))
	for _, h := range allowed {
		set[strings.ToLower(h)] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if !set[strings.ToLower(host)] {
				http.Error(w, "host not allowed", http.StatusBadRequest)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireBearerToken rejects any request whose token does not match,
// comparing in constant time (crypto/subtle) so a timing side-channel
// cannot help an attacker recover the token one byte at a time. The token
// normally comes from an "Authorization: Bearer <token>" header; a
// "?token=" query parameter is accepted too, since a browser's native
// WebSocket constructor cannot set custom headers on the handshake
// request at all — GET /api/v1/events has no other way to authenticate a
// real browser client. limiter, if non-nil, gates the check per source
// IP: an address already serving out a lockout is rejected before the
// token comparison even runs, and every failure/success is recorded back
// into it.
func RequireBearerToken(token string, limiter *AuthFailureLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			addr := remoteHost(r)

			if limiter != nil && !limiter.Allowed(addr) {
				http.Error(w, "too many failed attempts, try again later", http.StatusTooManyRequests)
				return
			}

			presented := bearerFromHeader(r)
			if presented == "" {
				presented = r.URL.Query().Get("token")
			}
			ok := presented != "" &&
				subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1

			if !ok {
				if limiter != nil {
					limiter.RecordFailure(addr)
				}
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if limiter != nil {
				limiter.RecordSuccess(addr)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bearerFromHeader extracts the token from a well-formed "Authorization:
// Bearer <token>" header, or "" if the header is absent or malformed.
func bearerFromHeader(r *http.Request) string {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return auth[len(prefix):]
}

// remoteHost strips the port off r.RemoteAddr, falling back to the whole
// string if it has no port (should not happen for a real net/http
// request, but a fallback here is cheaper than a panic if it ever does).
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
