package ws

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// testClient is a bare-bones real WebSocket client (the browser side),
// hand-rolled the same way this project's other test fixtures play a real
// peer on the wire (see internal/torrent's fakeSeeder) rather than using
// any library — it dials a real TCP connection, performs the real RFC 6455
// handshake by hand, and reads/writes real masked frames.
type testClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func dialTestClient(t *testing.T, url string) *testClient {
	t.Helper()
	conn, err := net.Dial("tcp", url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatalf("generating Sec-WebSocket-Key: %v", err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)

	req := "GET /events HTTP/1.1\r\n" +
		"Host: " + url + "\r\n" +
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
	wantAccept := acceptKey(key)
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != wantAccept {
		t.Fatalf("Sec-WebSocket-Accept = %q, want %q", got, wantAccept)
	}

	return &testClient{t: t, conn: conn, r: r}
}

// send writes a masked client frame - real client frames are always
// masked per RFC 6455 section 5.1.
func (c *testClient) send(opcode Opcode, payload []byte) {
	c.t.Helper()
	var buf bytes.Buffer
	writeMaskedFrame(c.t, &buf, true, opcode, payload)
	if _, err := c.conn.Write(buf.Bytes()); err != nil {
		c.t.Fatalf("write frame: %v", err)
	}
}

// read reads one unmasked server frame directly (bypassing this package's
// own Conn, which is exactly what's under test here).
func (c *testClient) read() frame {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	f, err := readServerFrame(c.r)
	if err != nil {
		c.t.Fatalf("read frame: %v", err)
	}
	return f
}

// readServerFrame parses a server (unmasked) frame - readFrame itself
// requires masking (it's the client-frame parser), so this test needs its
// own mirror for the other direction.
func readServerFrame(r *bufio.Reader) (frame, error) {
	var head [2]byte
	if _, err := readFull(r, head[:]); err != nil {
		return frame{}, err
	}
	fin := head[0]&0x80 != 0
	opcode := Opcode(head[0] & 0x0F)
	length := uint64(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := readFull(r, ext[:]); err != nil {
			return frame{}, err
		}
		length = uint64(ext[0])<<8 | uint64(ext[1])
	case 127:
		var ext [8]byte
		if _, err := readFull(r, ext[:]); err != nil {
			return frame{}, err
		}
		length = 0
		for _, b := range ext {
			length = length<<8 | uint64(b)
		}
	}
	payload := make([]byte, length)
	if _, err := readFull(r, payload); err != nil {
		return frame{}, err
	}
	return frame{fin: fin, opcode: opcode, payload: payload}, nil
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
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

func newTestServer(t *testing.T, handler func(*Conn)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r)
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		handler(conn)
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

func TestUpgradeCompletesRealHandshake(t *testing.T) {
	done := make(chan struct{})
	addr := newTestServer(t, func(conn *Conn) {
		defer conn.Close()
		close(done)
	})
	dialTestClient(t, addr)
	<-done
}

func TestConnRoundTripsTextMessage(t *testing.T) {
	addr := newTestServer(t, func(conn *Conn) {
		defer conn.Close()
		op, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("server ReadMessage: %v", err)
			return
		}
		if op != OpText {
			t.Errorf("server got opcode %#x, want OpText", op)
		}
		conn.WriteMessage(OpText, append([]byte("echo: "), payload...))
	})

	client := dialTestClient(t, addr)
	client.send(OpText, []byte("hello"))

	f := client.read()
	if f.opcode != OpText {
		t.Fatalf("client got opcode %#x, want OpText", f.opcode)
	}
	if string(f.payload) != "echo: hello" {
		t.Fatalf("payload = %q, want %q", f.payload, "echo: hello")
	}
}

func TestConnAnswersPingWithPong(t *testing.T) {
	addr := newTestServer(t, func(conn *Conn) {
		defer conn.Close()
		conn.ReadMessage() // drives the internal ping->pong handling, then blocks/errors on client close
	})

	client := dialTestClient(t, addr)
	client.send(OpPing, []byte("ping-payload"))

	f := client.read()
	if f.opcode != OpPong {
		t.Fatalf("opcode = %#x, want OpPong", f.opcode)
	}
	if string(f.payload) != "ping-payload" {
		t.Fatalf("pong payload = %q, want the ping's own payload echoed back", f.payload)
	}
}

func TestConnHandlesCloseHandshake(t *testing.T) {
	serverSawClose := make(chan struct{})
	addr := newTestServer(t, func(conn *Conn) {
		defer conn.Close()
		_, _, err := conn.ReadMessage()
		if err != ErrClosed {
			t.Errorf("server ReadMessage error = %v, want ErrClosed", err)
		}
		close(serverSawClose)
	})

	client := dialTestClient(t, addr)
	client.send(OpClose, nil)

	f := client.read()
	if f.opcode != OpClose {
		t.Fatalf("opcode = %#x, want OpClose (the required close handshake echo)", f.opcode)
	}
	<-serverSawClose
}

// TestSendReturnsErrQueueFullWhenQueueIsFull is a deterministic, white-box
// test of send's backpressure contract — a Conn built directly (bypassing
// newConn, so sendLoop is never started to drain it) rather than a real
// connection, since a real one's sendLoop drains into the OS socket buffer
// fast enough on loopback that a tight-loop test racing it to fill 32
// slots first would be unreliably flaky. ErrQueueFull is a sentinel
// (errors.Is), not a string a caller would have to match — added
// specifically so internal/api's LogsHandler can tell "momentarily full,
// worth retrying" apart from "the connection is actually gone."
func TestSendReturnsErrQueueFullWhenQueueIsFull(t *testing.T) {
	c := &Conn{outbound: make(chan []byte, outboundQueueSize)}
	for i := 0; i < outboundQueueSize; i++ {
		if err := c.send([]byte("x")); err != nil {
			t.Fatalf("send %d: unexpected error filling the queue: %v", i, err)
		}
	}
	if err := c.send([]byte("overflow")); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("send on a full queue: got %v, want ErrQueueFull", err)
	}
}

func TestUpgradeRejectsNonWebSocketRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := Upgrade(w, r); err == nil {
			t.Error("Upgrade succeeded on a plain GET request, want an error")
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("plain GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestDialClientRoundTripsAgainstARealServer is DialClient's own real
// fixture: a real Upgrade-backed httptest.Server on one side, a real
// DialClient connection on the other - the two halves of this package
// finally talking to each other, rather than each only ever being tested
// against a hand-rolled stand-in for the other side (newTestServer's own
// handlers so far, dialTestClient's own hand-rolled client). Proves both
// directions of the masking asymmetry actually round-trip correctly on
// real sockets, not just in the unit-level frame.go tests: the server
// receives and correctly unmasks what the client sent, and the client
// receives and correctly accepts what the (unmasked) server sent back.
func TestDialClientRoundTripsAgainstARealServer(t *testing.T) {
	addr := newTestServer(t, func(conn *Conn) {
		defer conn.Close()
		op, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("server ReadMessage: %v", err)
			return
		}
		if op != OpText {
			t.Errorf("server got opcode %#x, want OpText", op)
		}
		conn.WriteMessage(OpText, append([]byte("echo: "), payload...))
	})

	conn, err := DialClient(context.Background(), "ws://"+addr+"/events", nil)
	if err != nil {
		t.Fatalf("DialClient: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(OpText, []byte("hello")); err != nil {
		t.Fatalf("client WriteMessage: %v", err)
	}
	op, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("client ReadMessage: %v", err)
	}
	if op != OpText {
		t.Fatalf("client got opcode %#x, want OpText", op)
	}
	if string(payload) != "echo: hello" {
		t.Fatalf("payload = %q, want %q", payload, "echo: hello")
	}
}

// TestDialClientSendsCallerHeaders proves header actually reaches the
// handshake request - internal/tuiclient's whole reason for needing this
// parameter is to carry a real Authorization: Bearer token, which the
// control API's own RequireBearerToken middleware would otherwise reject
// the upgrade for.
func TestDialClientSendsCallerHeaders(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		conn, err := Upgrade(w, r)
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		conn.Close()
	}))
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	header := http.Header{"Authorization": {"Bearer test-token"}}
	conn, err := DialClient(context.Background(), "ws://"+addr+"/events", header)
	if err != nil {
		t.Fatalf("DialClient: %v", err)
	}
	conn.Close()

	if gotAuth != "Bearer test-token" {
		t.Fatalf("server saw Authorization = %q, want %q", gotAuth, "Bearer test-token")
	}
}

// TestDialClientRejectsNonUpgradeResponse proves a real non-WS HTTP
// response (a plain 200, no handshake) fails cleanly rather than DialClient
// misinterpreting it as a successful upgrade.
func TestDialClientRejectsNonUpgradeResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	if _, err := DialClient(context.Background(), "ws://"+addr+"/events", nil); err == nil {
		t.Fatal("DialClient succeeded against a plain 200 response, want an error")
	}
}
