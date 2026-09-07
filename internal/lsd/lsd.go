// Package lsd implements BEP 14 (Local Service Discovery): a multicast UDP
// announce on the LAN, so two peers on the same local network find each
// other instantly without a tracker or DHT round trip at all — the roadmap
// called this "makes demos instant". IPv6 (the [ff15::efc0:988f]:6771 group)
// is not implemented, matching the IPv4-only gap already documented for the
// UDP tracker (2.4) and the DHT (2.5).
package lsd

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	multicastAddr = "239.192.152.143:6771"

	// AnnounceInterval is BEP 14's own floor: implementations SHOULD NOT
	// announce the same infohash more often than this. Exported so
	// internal/engine's announce loop uses the same cadence rather than
	// inventing its own.
	AnnounceInterval = 5 * time.Minute

	maxDatagramSize = 512
)

// PeerFound is one BT-SEARCH announce heard from another local peer — never
// our own; New filters those out by a per-instance cookie.
type PeerFound struct {
	InfoHash [20]byte
	Addr     *net.UDPAddr
}

// LSD is one multicast socket: it can announce this node's own torrents and
// hears everyone else's on the same LAN, including other instances of this
// same client.
type LSD struct {
	conn   *net.UDPConn
	group  *net.UDPAddr
	cookie string

	found chan PeerFound
	done  chan struct{}
	wg    sync.WaitGroup
}

// New joins the LSD multicast group and starts listening in the background.
// Callers announce their own torrents explicitly via Announce — New does
// not send anything on its own.
func New() (*LSD, error) {
	group, err := net.ResolveUDPAddr("udp4", multicastAddr)
	if err != nil {
		return nil, fmt.Errorf("lsd: resolving multicast group: %w", err)
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return nil, fmt.Errorf("lsd: joining multicast group %s: %w", multicastAddr, err)
	}
	conn.SetReadBuffer(maxDatagramSize * 16)

	cookie, err := randomCookie()
	if err != nil {
		conn.Close()
		return nil, err
	}

	l := &LSD{
		conn:   conn,
		group:  group,
		cookie: cookie,
		found:  make(chan PeerFound, 64),
		done:   make(chan struct{}),
	}
	l.wg.Add(1)
	go l.readLoop()
	return l, nil
}

func randomCookie() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("lsd: generating cookie: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Announce sends one BT-SEARCH for infoHash, advertising port as where to
// reach this node for it. BEP 14 rate-limits this to at most once per
// AnnounceInterval per infohash; pacing that is the caller's job
// (internal/engine's announce loop) — Announce itself sends immediately,
// every time.
func (l *LSD) Announce(infoHash [20]byte, port uint16) error {
	msg := "BT-SEARCH * HTTP/1.1\r\n" +
		"Host: " + multicastAddr + "\r\n" +
		"Port: " + strconv.Itoa(int(port)) + "\r\n" +
		"Infohash: " + hex.EncodeToString(infoHash[:]) + "\r\n" +
		"cookie: " + l.cookie + "\r\n" +
		"\r\n\r\n"
	if _, err := l.conn.WriteToUDP([]byte(msg), l.group); err != nil {
		return fmt.Errorf("lsd: sending announce: %w", err)
	}
	return nil
}

// Found is where discovered peers arrive — closed when the read loop exits
// (Close, or the socket otherwise dying). Draining it is the caller's job.
func (l *LSD) Found() <-chan PeerFound { return l.found }

// Close stops listening and unblocks readLoop.
func (l *LSD) Close() error {
	close(l.done)
	err := l.conn.Close()
	l.wg.Wait()
	return err
}

func (l *LSD) readLoop() {
	defer l.wg.Done()
	defer close(l.found)

	buf := make([]byte, maxDatagramSize)
	for {
		n, addr, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-l.done:
				return
			default:
				continue
			}
		}
		infoHash, port, cookie, ok := parseBTSearch(buf[:n])
		if !ok || cookie == l.cookie {
			continue // malformed, or our own announce echoed back to us
		}
		select {
		case l.found <- PeerFound{InfoHash: infoHash, Addr: &net.UDPAddr{IP: addr.IP, Port: port}}:
		case <-l.done:
			return
		}
	}
}

// parseBTSearch decodes a BT-SEARCH datagram. The port comes from the
// message body, not the UDP packet's source port: every participant sends
// from the shared multicast socket's own bound port (6771), so the source
// port observed on receipt is never the sender's actual BitTorrent
// listening port — that is exactly what the Port: header is for, the same
// trust model a tracker announce's own port parameter uses.
func parseBTSearch(data []byte) (infoHash [20]byte, port int, cookie string, ok bool) {
	lines := strings.Split(string(data), "\r\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "BT-SEARCH") {
		return infoHash, 0, "", false
	}

	var haveInfoHash, havePort bool
	for _, line := range lines[1:] {
		idx := strings.IndexByte(line, ':')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		switch strings.ToLower(key) {
		case "infohash":
			raw, err := hex.DecodeString(val)
			if err != nil || len(raw) != 20 {
				continue
			}
			copy(infoHash[:], raw)
			haveInfoHash = true
		case "port":
			p, err := strconv.Atoi(val)
			if err != nil || p <= 0 || p > 65535 {
				continue
			}
			port = p
			havePort = true
		case "cookie":
			cookie = val
		}
	}
	return infoHash, port, cookie, haveInfoHash && havePort
}
