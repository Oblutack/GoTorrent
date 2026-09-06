package tracker

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"
)

// UDP tracker protocol (BEP 15): a fixed 64-bit magic identifies the
// protocol on connect, actions distinguish the two request/response pairs,
// and a connection id (valid 60s) lets repeated announces skip the connect
// round trip.
const (
	udpProtocolMagic  = 0x41727101980
	udpActionConnect  = 0
	udpActionAnnounce = 1
	udpActionError    = 3

	udpConnectionIDTTL = 60 * time.Second
	// udpMaxRetries bounds BEP 15's specified backoff (15*2^n seconds,
	// n = 0..8) — beyond n=8 the spec says to give up.
	udpMaxRetries = 8
)

// udpConn caches one tracker's connection id.
type udpConn struct {
	id      uint64
	expires time.Time
}

func udpEventCode(e Event) uint32 {
	switch e {
	case EventCompleted:
		return 1
	case EventStarted:
		return 2
	case EventStopped:
		return 3
	default:
		return 0
	}
}

func randomUint32() (uint32, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

// announceUDP implements BEP 15 over the URL's host:port.
func (c *Client) announceUDP(ctx context.Context, target *url.URL, req AnnounceRequest) (*AnnounceResponse, error) {
	host := target.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		return nil, fmt.Errorf("tracker: udp announce URL %q has no port", target)
	}

	raddr, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		return nil, fmt.Errorf("tracker: resolving %s: %w", host, err)
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, fmt.Errorf("tracker: dialing %s: %w", host, err)
	}
	defer conn.Close()

	// UDP reads/writes have no per-call context support; unblock a pending
	// one the instant ctx is cancelled by forcing an immediate deadline.
	watcherDone := make(chan struct{})
	defer close(watcherDone)
	go func() {
		select {
		case <-ctx.Done():
			conn.SetDeadline(time.Now())
		case <-watcherDone:
		}
	}()

	connID, err := c.udpConnectionID(ctx, conn, host)
	if err != nil {
		return nil, err
	}

	txID, err := randomUint32()
	if err != nil {
		return nil, fmt.Errorf("tracker: generating transaction id: %w", err)
	}
	key, err := randomUint32()
	if err != nil {
		return nil, fmt.Errorf("tracker: generating key: %w", err)
	}

	numWant := int32(-1)
	if req.NumWant > 0 {
		numWant = int32(req.NumWant)
	}

	buf := make([]byte, 98)
	binary.BigEndian.PutUint64(buf[0:8], connID)
	binary.BigEndian.PutUint32(buf[8:12], udpActionAnnounce)
	binary.BigEndian.PutUint32(buf[12:16], txID)
	copy(buf[16:36], req.InfoHash[:])
	copy(buf[36:56], req.PeerID[:])
	binary.BigEndian.PutUint64(buf[56:64], uint64(req.Downloaded))
	binary.BigEndian.PutUint64(buf[64:72], uint64(req.Left))
	binary.BigEndian.PutUint64(buf[72:80], uint64(req.Uploaded))
	binary.BigEndian.PutUint32(buf[80:84], udpEventCode(req.Event))
	// buf[84:88] is the IP override; 0 means "use the packet's source".
	binary.BigEndian.PutUint32(buf[88:92], key)
	binary.BigEndian.PutUint32(buf[92:96], uint32(numWant))
	binary.BigEndian.PutUint16(buf[96:98], req.Port)

	resp, err := udpRoundTrip(ctx, conn, buf, txID)
	if err != nil {
		return nil, err
	}
	return parseUDPAnnounceResponse(resp)
}

// udpConnectionID returns a still-valid cached connection id for host, or
// performs the connect handshake and caches the result.
func (c *Client) udpConnectionID(ctx context.Context, conn *net.UDPConn, host string) (uint64, error) {
	c.udpMu.Lock()
	if cached, ok := c.udpConns[host]; ok && time.Now().Before(cached.expires) {
		id := cached.id
		c.udpMu.Unlock()
		return id, nil
	}
	c.udpMu.Unlock()

	txID, err := randomUint32()
	if err != nil {
		return 0, fmt.Errorf("tracker: generating transaction id: %w", err)
	}
	req := make([]byte, 16)
	binary.BigEndian.PutUint64(req[0:8], udpProtocolMagic)
	binary.BigEndian.PutUint32(req[8:12], udpActionConnect)
	binary.BigEndian.PutUint32(req[12:16], txID)

	resp, err := udpRoundTrip(ctx, conn, req, txID)
	if err != nil {
		return 0, err
	}
	if len(resp) < 16 {
		return 0, errors.New("tracker: udp connect response too short")
	}
	id := binary.BigEndian.Uint64(resp[8:16])

	c.udpMu.Lock()
	if c.udpConns == nil {
		c.udpConns = make(map[string]*udpConn)
	}
	c.udpConns[host] = &udpConn{id: id, expires: time.Now().Add(udpConnectionIDTTL)}
	c.udpMu.Unlock()
	return id, nil
}

// udpRoundTrip sends req and waits for a reply whose transaction id matches,
// retrying with BEP 15's 15*2^n second backoff until udpMaxRetries is
// exhausted or ctx is cancelled.
func udpRoundTrip(ctx context.Context, conn *net.UDPConn, req []byte, txID uint32) ([]byte, error) {
	buf := make([]byte, 2048)
	for n := 0; n <= udpMaxRetries; n++ {
		if _, err := conn.Write(req); err != nil {
			return nil, fmt.Errorf("tracker: udp write: %w", err)
		}

		timeout := time.Duration(15*(1<<uint(n))) * time.Second
		conn.SetReadDeadline(time.Now().Add(timeout))
		nRead, err := conn.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue // no reply within this backoff window; try again
			}
			return nil, fmt.Errorf("tracker: udp read: %w", err)
		}
		if nRead < 8 {
			continue // too short to even carry action+transaction_id
		}
		action := binary.BigEndian.Uint32(buf[0:4])
		gotTxID := binary.BigEndian.Uint32(buf[4:8])
		if gotTxID != txID {
			continue // stale or unrelated reply; keep waiting on this socket
		}
		if action == udpActionError {
			return nil, &ErrTrackerFailure{Reason: string(buf[8:nRead])}
		}

		out := make([]byte, nRead)
		copy(out, buf[:nRead])
		return out, nil
	}
	return nil, fmt.Errorf("tracker: udp request timed out after %d retries", udpMaxRetries)
}

// parseUDPAnnounceResponse decodes a BEP 15 announce reply (the 8-byte
// action+transaction_id header is already consumed by udpRoundTrip's
// caller — this starts at interval).
func parseUDPAnnounceResponse(body []byte) (*AnnounceResponse, error) {
	if len(body) < 20 {
		return nil, errors.New("tracker: udp announce response too short")
	}
	interval := binary.BigEndian.Uint32(body[8:12])
	leechers := binary.BigEndian.Uint32(body[12:16])
	seeders := binary.BigEndian.Uint32(body[16:20])

	const entry = 6 // 4-byte IPv4 + 2-byte port
	peerBytes := body[20:]
	if len(peerBytes)%entry != 0 {
		return nil, fmt.Errorf("tracker: udp peer list is %d bytes, not a multiple of %d", len(peerBytes), entry)
	}

	peers := make([]PeerInfo, 0, len(peerBytes)/entry)
	for off := 0; off+entry <= len(peerBytes); off += entry {
		ip := make(net.IP, 4)
		copy(ip, peerBytes[off:off+4])
		port := binary.BigEndian.Uint16(peerBytes[off+4 : off+6])
		if port == 0 || ip.IsUnspecified() {
			continue
		}
		peers = append(peers, PeerInfo{IP: ip, Port: port})
	}
	if len(peers) > maxPeersInResponse {
		peers = peers[:maxPeersInResponse]
	}

	return &AnnounceResponse{
		Interval:   time.Duration(interval) * time.Second,
		Complete:   int(seeders),
		Incomplete: int(leechers),
		Peers:      peers,
	}, nil
}
