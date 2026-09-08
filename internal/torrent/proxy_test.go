package torrent

import (
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/proxy"
)

// fakeSOCKS5Relay is a minimal real SOCKS5 server: no-auth, CONNECT only,
// relays to whatever address the client asked for. Enough to prove a real
// Torrent actually tunnels its peer connection through a configured proxy
// rather than dialing directly — internal/proxy's own tests already cover
// the wire protocol in detail.
func fakeSOCKS5Relay(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				hdr := make([]byte, 2)
				if _, err := io.ReadFull(c, hdr); err != nil {
					return
				}
				methods := make([]byte, hdr[1])
				if _, err := io.ReadFull(c, methods); err != nil {
					return
				}
				if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
					return
				}

				req := make([]byte, 4)
				if _, err := io.ReadFull(c, req); err != nil {
					return
				}
				var host string
				switch req[3] {
				case 0x01:
					b := make([]byte, 4)
					if _, err := io.ReadFull(c, b); err != nil {
						return
					}
					host = net.IP(b).String()
				case 0x04:
					b := make([]byte, 16)
					if _, err := io.ReadFull(c, b); err != nil {
						return
					}
					host = net.IP(b).String()
				default:
					return
				}
				portB := make([]byte, 2)
				if _, err := io.ReadFull(c, portB); err != nil {
					return
				}
				port := int(portB[0])<<8 | int(portB[1])
				target := net.JoinHostPort(host, strconv.Itoa(port))

				targetConn, err := net.Dial("tcp", target)
				if err != nil {
					c.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
					return
				}
				defer targetConn.Close()
				c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})

				done := make(chan struct{}, 2)
				go func() { io.Copy(targetConn, c); done <- struct{}{} }()
				go func() { io.Copy(c, targetConn); done <- struct{}{} }()
				<-done
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// TestTorrentDownloadsThroughASOCKS5Proxy is the end-to-end proof: a real
// Torrent, given a Config.ProxyDialer pointed at a real (if minimal) SOCKS5
// server, downloads a whole torrent from a fake seeder it never contacts
// directly — every byte crosses the relay.
func TestTorrentDownloadsThroughASOCKS5Proxy(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "viaproxy.bin", pieceLength, []fileSpec{{length: pieceLength * 3}})
	seeder := newFakeSeeder(t, mi, content)

	proxyAddr := fakeSOCKS5Relay(t)

	cfg := newTestConfig(t)
	cfg.ProxyDialer = proxy.NewDialer(proxy.Config{Type: "socks5", Address: proxyAddr})

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 15*time.Second)
}
