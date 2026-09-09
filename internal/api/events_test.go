package api

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// dialEventsClient is a bare-bones real WebSocket client (mirroring
// internal/ws's own test fixture, duplicated here rather than exported
// from that package purely for test wiring — see CLAUDE.md's testing
// section on this being an accepted, small-scale pattern already used
// elsewhere) — it performs the real RFC 6455 handshake and hands back a
// reader positioned right after it, for reading real unmasked server
// frames.
type eventsTestClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func dialEventsClient(t *testing.T, addr, path string) *eventsTestClient {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatalf("generating Sec-WebSocket-Key: %v", err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)

	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake request: %v", err)
	}

	r := bufio.NewReader(conn)
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d, want 101", resp.StatusCode)
	}
	return &eventsTestClient{t: t, conn: conn, r: r}
}

// readTextFrame reads one unmasked server text frame's payload - just
// enough frame parsing to drive this test, not a general client.
func (c *eventsTestClient) readTextFrame() []byte {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var head [2]byte
	if _, err := readFullBytes(c.r, head[:]); err != nil {
		c.t.Fatalf("read frame header: %v", err)
	}
	length := int(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := readFullBytes(c.r, ext[:]); err != nil {
			c.t.Fatalf("read extended length: %v", err)
		}
		length = int(ext[0])<<8 | int(ext[1])
	case 127:
		c.t.Fatalf("unexpectedly large frame")
	}
	payload := make([]byte, length)
	if _, err := readFullBytes(c.r, payload); err != nil {
		c.t.Fatalf("read payload: %v", err)
	}
	return payload
}

func readFullBytes(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// TestEventsHandlerStreamsRealEvents proves the whole chain end to end: a
// real WebSocket client connects, a torrent is really Added while it's
// listening, and the client receives that as a real, parseable
// torrentAdded JSON message on the wire.
func TestEventsHandlerStreamsRealEvents(t *testing.T) {
	e := newTestEngine(t)
	srv := httptest.NewServer(http.HandlerFunc(EventsHandler(e)))
	defer srv.Close()

	addr := srv.Listener.Addr().String()
	client := dialEventsClient(t, addr, "/api/v1/events")

	hash := addTestTorrent(t, e, "wsevent")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		payload := client.readTextFrame()
		var ev WSEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			t.Fatalf("decode event: %v (payload=%s)", err, payload)
		}
		if ev.Kind == "torrentAdded" {
			if ev.InfoHash != hash.String() {
				t.Fatalf("InfoHash = %q, want %q", ev.InfoHash, hash.String())
			}
			return
		}
	}
	t.Fatal("never received a torrentAdded event")
}

// TestEventsHandlerSendsSessionStatsPeriodically proves the 1Hz tick fires
// even with zero real Engine activity - a client should never sit in
// total silence.
func TestEventsHandlerSendsSessionStatsPeriodically(t *testing.T) {
	e := newTestEngine(t)
	srv := httptest.NewServer(http.HandlerFunc(EventsHandler(e)))
	defer srv.Close()

	client := dialEventsClient(t, srv.Listener.Addr().String(), "/api/v1/events")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		payload := client.readTextFrame()
		var ev WSEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		if ev.Kind == "sessionStats" {
			if ev.Session == nil {
				t.Fatal("sessionStats event carried a nil Session")
			}
			return
		}
	}
	t.Fatal("never received a sessionStats tick")
}

func TestEventsHandlerRejectsPlainHTTPRequest(t *testing.T) {
	e := newTestEngine(t)
	srv := httptest.NewServer(http.HandlerFunc(EventsHandler(e)))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("plain GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-WebSocket request", resp.StatusCode)
	}
}
