package api

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Config assembles Handler's middleware chain. Token and AllowedHosts are
// required for Handler to be meaningfully secure - a zero Config still
// builds a working http.Handler (useful in tests that want to isolate one
// layer), it just is not one anyone should point a real bind address at.
type Config struct {
	// Token is the bearer token every request must present. See
	// LoadOrCreateToken.
	Token string
	// AllowedHosts is the exact set of Host header values (no port) a
	// request may carry. See DefaultAllowedHosts for the common case.
	AllowedHosts []string
	// MaxAuthFailures, AuthFailureWindow, and AuthFailureLockout configure
	// the brute-force guard (see AuthFailureLimiter). MaxAuthFailures <= 0
	// disables it.
	MaxAuthFailures    int
	AuthFailureWindow  time.Duration
	AuthFailureLockout time.Duration
}

// DefaultMaxAuthFailures etc. are gottrentd's out-of-the-box brute-force
// guard: 10 failed attempts from one address within a minute locks that
// address out for 5 minutes. Generous enough that a legitimate client
// retrying after briefly losing its stored token doesn't get needlessly
// locked out, tight enough that guessing a 256-bit token by brute force is
// not meaningfully helped by this daemon being reachable at all.
const (
	DefaultMaxAuthFailures    = 10
	DefaultAuthFailureWindow  = 1 * time.Minute
	DefaultAuthFailureLockout = 5 * time.Minute
)

// DefaultAllowedHosts is every Host header value a request legitimately
// reaching this daemon on bindAddr (a "host:port" or ":port" string, the
// same shape Config.APIAddress/http.Server.Addr use) should carry: the
// standard loopback names (so "http://localhost:PORT/..." and
// "http://127.0.0.1:PORT/..." both work regardless of which one a client
// happens to use) plus bindAddr's own host part, for a deliberately
// non-loopback bind (remote access, meant to be paired with TLS - see
// Config's own doc comment on why a bound API is not automatically safe).
func DefaultAllowedHosts(bindAddr string) []string {
	hosts := []string{"127.0.0.1", "localhost", "[::1]", "::1"}
	if h, _, err := net.SplitHostPort(bindAddr); err == nil && h != "" {
		hosts = append(hosts, h)
	}
	return hosts
}

// NewHandler wraps routes in the full security chain, outermost first:
// AllowHosts (rejects a DNS-rebound request before anything else runs) then
// RequireBearerToken (gated by the brute-force limiter Config configures).
// CORS gets no separate middleware here deliberately - this handler never
// sets an Access-Control-Allow-Origin header for any request, which is
// already "deny by default" as far as a browser enforcing CORS is
// concerned; adding an explicit no-op layer to say so would just be
// ceremony around the same fact.
func NewHandler(cfg Config, routes http.Handler) http.Handler {
	limiter := NewAuthFailureLimiter(cfg.MaxAuthFailures, cfg.AuthFailureWindow, cfg.AuthFailureLockout)
	h := RequireBearerToken(cfg.Token, limiter)(routes)
	h = AllowHosts(cfg.AllowedHosts)(h)
	return h
}

// LoadTLSConfig reads a certificate/key pair for Config's "optional TLS for
// remote binding" checklist item - a nil, nil-error return from a caller
// that never set TLSCertFile/TLSKeyFile is the expected default (plain
// HTTP, fine for a loopback bind; the Host allowlist and bearer token are
// what's actually load-bearing there, not transport encryption a same-box
// client doesn't need).
func LoadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("api: loading TLS certificate: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}
