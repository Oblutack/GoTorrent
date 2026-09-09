// Package ws is a small, hand-rolled RFC 6455 WebSocket server
// implementation — no external dependency, consistent with the rest of
// this codebase (see CLAUDE.md): net/http has no WebSocket support at
// all, and there is no WebSocket package anywhere in the standard
// library, unlike the several other protocols this project has already
// implemented from their specs (bencode, KRPC, SOCKS5, the BitTorrent wire
// protocol itself).
//
// Scope is deliberately narrower than a general-purpose library: text and
// binary data frames, ping/pong, and the close handshake are all
// implemented; permessage-deflate and other RFC 6455 extensions are not
// (this package never advertises support for any `Sec-WebSocket-Extensions`
// value), and neither is fragmented message reassembly beyond what a
// single logical message needs (see Conn.ReadMessage's own doc comment).
// 4.2's control-API event stream is the only consumer, and it only ever
// needs to push small, complete JSON messages to a browser client — not
// stream arbitrarily large payloads across many frames.
package ws

import (
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// magicGUID is RFC 6455 section 1.3's fixed key-derivation constant.
const magicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Opcode identifies a frame's payload type (RFC 6455 section 5.2).
type Opcode byte

const (
	OpContinuation Opcode = 0x0
	OpText         Opcode = 0x1
	OpBinary       Opcode = 0x2
	OpClose        Opcode = 0x8
	OpPing         Opcode = 0x9
	OpPong         Opcode = 0xA
)

func (op Opcode) isControl() bool { return op >= OpClose }

// ErrNotHijackable is returned by Upgrade when the ResponseWriter's
// underlying connection cannot be taken over directly — true of nothing
// this project's own http.Server ever uses (plain TCP or TLS), but a
// legitimate failure mode for, say, an http2 connection, which does not
// support hijacking at all.
var ErrNotHijackable = errors.New("ws: response writer does not support hijacking")

// Upgrade validates r as a WebSocket handshake request and, if valid,
// completes it: sends the 101 Switching Protocols response and takes over
// the underlying connection via http.Hijacker, handing back a Conn ready
// for ReadMessage/WriteMessage. The caller owns the returned Conn's
// lifetime from this point on — net/http no longer manages the connection
// at all once Hijack succeeds, so a caller that returns from its handler
// without closing it (directly or via Conn.Close) leaks the socket.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if r.Method != http.MethodGet {
		return nil, fmt.Errorf("ws: handshake must be GET, got %s", r.Method)
	}
	if !headerContainsToken(r.Header, "Connection", "upgrade") {
		return nil, errors.New("ws: missing Connection: Upgrade header")
	}
	if !headerContainsToken(r.Header, "Upgrade", "websocket") {
		return nil, errors.New("ws: missing Upgrade: websocket header")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, fmt.Errorf("ws: unsupported Sec-WebSocket-Version %q, want 13", r.Header.Get("Sec-WebSocket-Version"))
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("ws: missing Sec-WebSocket-Key header")
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, ErrNotHijackable
	}
	netConn, buf, err := hijacker.Hijack()
	if err != nil {
		return nil, fmt.Errorf("ws: hijacking connection: %w", err)
	}

	accept := acceptKey(key)
	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := buf.WriteString(response); err != nil {
		netConn.Close()
		return nil, fmt.Errorf("ws: writing handshake response: %w", err)
	}
	if err := buf.Flush(); err != nil {
		netConn.Close()
		return nil, fmt.Errorf("ws: flushing handshake response: %w", err)
	}

	return newConn(netConn, buf.Reader), nil
}

// acceptKey computes Sec-WebSocket-Accept per RFC 6455 section 1.3:
// base64(SHA-1(key + magicGUID)).
func acceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(magicGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// headerContainsToken reports whether header name's value contains token
// as one comma-separated, case-insensitive entry — both Connection and
// Upgrade are allowed to carry a comma-separated list (e.g.
// "keep-alive, Upgrade"), not just the bare token some clients send.
func headerContainsToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// remoteAddrString is a small helper so Conn's fields can stay unexported
// while still letting a caller log something useful.
func remoteAddrString(c net.Conn) string {
	if c == nil {
		return ""
	}
	return c.RemoteAddr().String()
}
