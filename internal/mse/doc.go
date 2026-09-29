// Package mse implements Message Stream Encryption (MSE), also known as
// Protocol Encryption (PE) — the widely-deployed de facto standard real
// BitTorrent clients use to obfuscate their wire protocol, mainly to get
// past DPI-based throttling/blocking of "BitTorrent protocol"-shaped
// traffic on some networks.
//
// There is no official BEP for this — it originates from an Azureus/Vuze
// wiki page that no longer resolves — so this package's behavior was
// cross-checked against two independent real-world sources during
// implementation (a detailed technical writeup, and a second,
// independently-authored, widely-deployed Go implementation's source, read
// carefully rather than copied) rather than a single formal spec, and
// every bit-level constant checked agreed exactly between them. See
// CLAUDE.md for the full narrative and the honest verification-gap this
// leaves: no real third-party BitTorrent client was available in this
// project's dev environment to interop-test against directly.
//
// The scheme is a Diffie-Hellman key exchange followed by an optional RC4
// encryption layer, negotiated per connection. This package is a pure,
// self-contained implementation of the wire protocol only — InitiateHandshake
// (the dialing/"A" side) and ReceiveHandshake (the accepting/"B" side) both
// return an ordinary net.Conn once negotiation completes, so callers
// (internal/peer, internal/engine) never need to know or care whether the
// connection ended up RC4-encrypted or plaintext.
package mse
