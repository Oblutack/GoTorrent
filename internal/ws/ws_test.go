package ws

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
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
