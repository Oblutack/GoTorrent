package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

func (d *Dialer) dialHTTPConnect(ctx context.Context, addr string) (net.Conn, error) {
	var nd net.Dialer
	conn, err := nd.DialContext(ctx, "tcp", d.cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("proxy: connecting to HTTP proxy %s: %w", d.cfg.Address, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if d.cfg.Username != "" {
		req.Header.Set("Proxy-Authorization", "Basic "+basicAuth(d.cfg.Username, d.cfg.Password))
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxy: writing CONNECT request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxy: reading CONNECT response: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("proxy: HTTP CONNECT to %s failed: %s", addr, resp.Status)
	}

	conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		// http.ReadResponse's bufio.Reader may have read past the header
		// block into the start of the tunneled data; wrap conn so those
		// bytes are not lost.
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

func basicAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}

// bufferedConn serves Read from a bufio.Reader that may already hold bytes
// read past an HTTP CONNECT response's headers, falling through to the
// underlying net.Conn once that buffer is drained.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
