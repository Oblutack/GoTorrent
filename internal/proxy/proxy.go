// Package proxy dials outbound TCP connections through a SOCKS5 (RFC 1928)
// or HTTP CONNECT proxy, hand-rolled like everything else in this client —
// no golang.org/x/net dependency.
package proxy

import (
	"context"
	"fmt"
	"net"
)

// Config configures a Dialer.
type Config struct {
	// Type selects the proxy protocol: "socks5" or "http". Any other
	// value (including empty) makes NewDialer return nil, meaning "dial
	// directly" — see Dialer's own doc comment.
	Type string
	// Address is the proxy's own "host:port".
	Address string
	// Username and Password authenticate to the proxy, if it requires it
	// (SOCKS5 RFC 1929, or HTTP's Proxy-Authorization). Leave both empty
	// for no authentication.
	Username string
	Password string
	// ProxyDNS asks the proxy to resolve a hostname itself (SOCKS5
	// domain-name addressing, RFC 1928 ATYP 0x03) rather than resolving
	// locally before connecting — the "proxy DNS too" option, so a DNS
	// query never leaves the machine outside the proxy. Only meaningful
	// for Type "socks5": an HTTP CONNECT request already sends the
	// hostname to the proxy verbatim, so it never resolves locally either
	// way.
	ProxyDNS bool
}

// Dialer dials outbound TCP connections through the configured proxy. A
// nil *Dialer dials directly — every method is nil-receiver-safe — so a
// caller never needs to nil-check an optional Dialer before using it, the
// same convenience ipfilter.Filter offers for the same reason.
type Dialer struct {
	cfg Config
}

// NewDialer returns a Dialer for cfg, or nil if cfg.Type does not name a
// supported proxy protocol — including the zero Config, so "no proxy
// configured" and "dial directly" are the same nil value throughout this
// package's callers.
func NewDialer(cfg Config) *Dialer {
	switch cfg.Type {
	case "socks5", "http":
		return &Dialer{cfg: cfg}
	default:
		return nil
	}
}

// DialContext dials addr, through the configured proxy if there is one.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if d == nil {
		var nd net.Dialer
		return nd.DialContext(ctx, network, addr)
	}
	switch d.cfg.Type {
	case "socks5":
		return d.dialSOCKS5(ctx, addr)
	case "http":
		return d.dialHTTPConnect(ctx, addr)
	default:
		// Unreachable via NewDialer, but Config can be embedded directly
		// too (tests do) — fail clearly rather than silently going direct.
		return nil, fmt.Errorf("proxy: unsupported type %q", d.cfg.Type)
	}
}
