package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// newEchoServer is the "real destination" every test dials through a
// proxy — it just bounces back whatever it receives, which is enough to
// prove the whole tunnel (handshake + relayed bytes) actually works.
func newEchoServer(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln
}

func roundTrip(t *testing.T, conn net.Conn, msg string) string {
	t.Helper()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(buf)
}

// --- SOCKS5 fixture --------------------------------------------------------

type fakeSOCKS5Server struct {
	ln                 net.Listener
	requireAuth        bool
	username, password string
}

func newFakeSOCKS5Server(t *testing.T, requireAuth bool, username, password string) *fakeSOCKS5Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSOCKS5Server{ln: ln, requireAuth: requireAuth, username: username, password: password}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *fakeSOCKS5Server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSOCKS5Server) handle(conn net.Conn) {
	defer conn.Close()

	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}

	method := byte(socks5AuthNone)
	if s.requireAuth {
		method = socks5AuthUsernamePass
	}
	if _, err := conn.Write([]byte{socks5Version, method}); err != nil {
		return
	}

	if s.requireAuth {
		b := make([]byte, 2)
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		u := make([]byte, b[1])
		if _, err := io.ReadFull(conn, u); err != nil {
			return
		}
		pl := make([]byte, 1)
		if _, err := io.ReadFull(conn, pl); err != nil {
			return
		}
		p := make([]byte, pl[0])
		if _, err := io.ReadFull(conn, p); err != nil {
			return
		}
		if string(u) != s.username || string(p) != s.password {
			conn.Write([]byte{0x01, 0x01})
			return
		}
		conn.Write([]byte{0x01, 0x00})
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case socks5ATYPIPv4:
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case socks5ATYPIPv6:
		b := make([]byte, 16)
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case socks5ATYPDomain:
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = string(b)
	default:
		return
	}
	portB := make([]byte, 2)
	if _, err := io.ReadFull(conn, portB); err != nil {
		return
	}
	port := int(portB[0])<<8 | int(portB[1])
	target := net.JoinHostPort(host, strconv.Itoa(port))

	targetConn, err := net.Dial("tcp", target)
	if err != nil {
		conn.Write([]byte{socks5Version, 0x04, 0x00, socks5ATYPIPv4, 0, 0, 0, 0, 0, 0})
		return
	}
	defer targetConn.Close()
	conn.Write([]byte{socks5Version, 0x00, 0x00, socks5ATYPIPv4, 0, 0, 0, 0, 0, 0})

	done := make(chan struct{}, 2)
	go func() { io.Copy(targetConn, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, targetConn); done <- struct{}{} }()
	<-done
}

func TestDialSOCKS5NoAuth(t *testing.T) {
	echo := newEchoServer(t)
	srv := newFakeSOCKS5Server(t, false, "", "")

	d := NewDialer(Config{Type: "socks5", Address: srv.ln.Addr().String()})
	conn, err := d.DialContext(context.Background(), "tcp", echo.Addr().String())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()

	if got := roundTrip(t, conn, "hello through socks5"); got != "hello through socks5" {
		t.Fatalf("echo returned %q", got)
	}
}

func TestDialSOCKS5WithCorrectAuth(t *testing.T) {
	echo := newEchoServer(t)
	srv := newFakeSOCKS5Server(t, true, "alice", "s3cret")

	d := NewDialer(Config{Type: "socks5", Address: srv.ln.Addr().String(), Username: "alice", Password: "s3cret"})
	conn, err := d.DialContext(context.Background(), "tcp", echo.Addr().String())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()

	if got := roundTrip(t, conn, "authenticated"); got != "authenticated" {
		t.Fatalf("echo returned %q", got)
	}
}

func TestDialSOCKS5WithWrongPasswordFails(t *testing.T) {
	srv := newFakeSOCKS5Server(t, true, "alice", "s3cret")

	d := NewDialer(Config{Type: "socks5", Address: srv.ln.Addr().String(), Username: "alice", Password: "wrong"})
	if _, err := d.DialContext(context.Background(), "tcp", "127.0.0.1:1"); err == nil {
		t.Fatal("DialContext with a wrong password: want an error")
	}
}

func TestDialSOCKS5ProxyDNSUsesDomainAddressing(t *testing.T) {
	echo := newEchoServer(t)
	_, echoPort, err := net.SplitHostPort(echo.Addr().String())
	if err != nil {
		t.Fatalf("split echo addr: %v", err)
	}
	srv := newFakeSOCKS5Server(t, false, "", "")

	d := NewDialer(Config{Type: "socks5", Address: srv.ln.Addr().String(), ProxyDNS: true})
	// "localhost" is a real hostname, not an IP - with ProxyDNS the client
	// must send it as-is (ATYP domain) rather than resolving it itself;
	// the fake server resolves it on its own end via a plain net.Dial.
	conn, err := d.DialContext(context.Background(), "tcp", net.JoinHostPort("localhost", echoPort))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()

	if got := roundTrip(t, conn, "dns via proxy"); got != "dns via proxy" {
		t.Fatalf("echo returned %q", got)
	}
}

// --- HTTP CONNECT fixture ---------------------------------------------------

func newFakeHTTPProxy(t *testing.T, username, password string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "expected CONNECT", http.StatusMethodNotAllowed)
			return
		}
		if username != "" {
			want := "Basic " + basicAuth(username, password)
			if r.Header.Get("Proxy-Authorization") != want {
				w.WriteHeader(http.StatusProxyAuthRequired)
				return
			}
		}
		target, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer target.Close()

		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack not supported", http.StatusInternalServerError)
			return
		}
		clientConn, _, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer clientConn.Close()
		clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

		done := make(chan struct{}, 2)
		go func() { io.Copy(target, clientConn); done <- struct{}{} }()
		go func() { io.Copy(clientConn, target); done <- struct{}{} }()
		<-done
	}))
	t.Cleanup(srv.Close)
	return srv
}

func httpProxyAddr(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing httptest URL: %v", err)
	}
	return u.Host
}

func TestDialHTTPConnectNoAuth(t *testing.T) {
	echo := newEchoServer(t)
	srv := newFakeHTTPProxy(t, "", "")

	d := NewDialer(Config{Type: "http", Address: httpProxyAddr(t, srv)})
	conn, err := d.DialContext(context.Background(), "tcp", echo.Addr().String())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()

	if got := roundTrip(t, conn, "hello through http connect"); got != "hello through http connect" {
		t.Fatalf("echo returned %q", got)
	}
}

func TestDialHTTPConnectWithAuth(t *testing.T) {
	echo := newEchoServer(t)
	srv := newFakeHTTPProxy(t, "bob", "hunter2")

	d := NewDialer(Config{Type: "http", Address: httpProxyAddr(t, srv), Username: "bob", Password: "hunter2"})
	conn, err := d.DialContext(context.Background(), "tcp", echo.Addr().String())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()

	if got := roundTrip(t, conn, "authenticated too"); got != "authenticated too" {
		t.Fatalf("echo returned %q", got)
	}
}

func TestDialHTTPConnectMissingAuthFails(t *testing.T) {
	srv := newFakeHTTPProxy(t, "bob", "hunter2")

	d := NewDialer(Config{Type: "http", Address: httpProxyAddr(t, srv)})
	if _, err := d.DialContext(context.Background(), "tcp", "127.0.0.1:1"); err == nil {
		t.Fatal("DialContext without required auth: want an error")
	}
}

// --- Dialer / NewDialer basics ---------------------------------------------

func TestNilDialerDialsDirectly(t *testing.T) {
	echo := newEchoServer(t)
	var d *Dialer
	conn, err := d.DialContext(context.Background(), "tcp", echo.Addr().String())
	if err != nil {
		t.Fatalf("nil *Dialer DialContext: %v", err)
	}
	defer conn.Close()
	if got := roundTrip(t, conn, "direct"); got != "direct" {
		t.Fatalf("echo returned %q", got)
	}
}

func TestNewDialerRejectsUnknownType(t *testing.T) {
	if d := NewDialer(Config{Type: "wireguard"}); d != nil {
		t.Fatalf("NewDialer with an unsupported type returned %v, want nil", d)
	}
}

func TestNewDialerEmptyTypeIsNil(t *testing.T) {
	if d := NewDialer(Config{}); d != nil {
		t.Fatalf("NewDialer(Config{}) returned %v, want nil (dial directly)", d)
	}
}
