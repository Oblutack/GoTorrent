// Package utp implements µTP (Micro Transport Protocol, BEP 29) — the
// UDP-based BitTorrent transport built on LEDBAT (RFC 6817), a delay-based
// congestion control algorithm deliberately designed to back off *before*
// it causes queuing delay for anything else sharing the link, rather than
// waiting for packet loss the way ordinary TCP congestion control does.
//
// There is no official test-vector suite for this (same situation as
// internal/mse) — every constant here was cross-checked during
// implementation against BEP 29's own spec text and RFC 6817's own text,
// plus a second real implementation's source read for the one detail
// BEP 29's prose alone left ambiguous (the connection-ID handshake
// direction). See CLAUDE.md for the full narrative, including one real
// discrepancy this caught: a summarized fetch of BEP 29 claimed
// connection_id is 32 bits, but the spec's own header diagram — and a
// second implementation's own uint16 fields — show it is 16.
//
// This package is a pure, self-contained protocol implementation,
// providing *Conn (a net.Conn) and *Socket (dial + accept, the UDP
// analogue of net.Listener) — internal/peer and internal/engine
// orchestrate it, the same shape internal/dht/internal/mse/internal/lsd
// already establish for their own protocols.
package utp
