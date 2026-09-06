// Package tracker talks to BitTorrent trackers, HTTP(S) and UDP (BEP 15)
// alike, behind one Client.Announce entry point that dispatches on the
// announce URL's scheme.
package tracker

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
)

const (
	// maxResponseBytes caps an announce response. Real responses are a few
	// kilobytes; anything approaching this is a broken or hostile tracker.
	maxResponseBytes = 4 << 20

	// maxPeersInResponse bounds how many peers we will take from one announce.
	maxPeersInResponse = 1000

	defaultTimeout = 45 * time.Second

	// peerIDPrefix identifies this client in the Azureus-style peer ID format:
	// two letters for the client, four digits for the version.
	peerIDPrefix = "-GT0001-"
)

// ErrTrackerFailure is returned when a tracker answers with a failure reason.
type ErrTrackerFailure struct {
	URL    string
	Reason string
}

func (e *ErrTrackerFailure) Error() string {
	return fmt.Sprintf("tracker %s refused the announce: %s", e.URL, e.Reason)
}

// Event is the announce event, per BEP 3.
type Event string

const (
	EventNone      Event = ""
	EventStarted   Event = "started"
	EventStopped   Event = "stopped"
	EventCompleted Event = "completed"
)

// PeerInfo is one peer address from a tracker.
type PeerInfo struct {
	IP   net.IP
	Port uint16
}

// Addr renders the peer as a dialable host:port.
func (p PeerInfo) Addr() string {
	return net.JoinHostPort(p.IP.String(), strconv.Itoa(int(p.Port)))
}

// AnnounceRequest is one announce to one tracker.
type AnnounceRequest struct {
	InfoHash   [20]byte
	PeerID     [20]byte
	Port       uint16
	Uploaded   int64
	Downloaded int64
	Left       int64
	Compact    bool
	NoPeerID   bool
	Event      Event
	NumWant    int
	Key        string
	TrackerID  string
}

// AnnounceResponse is a tracker's reply.
type AnnounceResponse struct {
	WarningMessage string
	Interval       time.Duration
	MinInterval    time.Duration
	TrackerID      string
	Complete       int
	Incomplete     int
	Peers          []PeerInfo
}

// announceWire mirrors the bencoded response. "peers" is either a packed
// string of 6-byte entries (BEP 23) or a list of dictionaries, so it is
// captured raw and decoded separately.
type announceWire struct {
	FailureReason  string             `bencode:"failure reason,omitempty"`
	WarningMessage string             `bencode:"warning message,omitempty"`
	Interval       int64              `bencode:"interval,omitempty"`
	MinInterval    int64              `bencode:"min interval,omitempty"`
	TrackerID      string             `bencode:"tracker id,omitempty"`
	Complete       int64              `bencode:"complete,omitempty"`
	Incomplete     int64              `bencode:"incomplete,omitempty"`
	Peers          bencode.RawMessage `bencode:"peers,omitempty"`
	Peers6         bencode.RawMessage `bencode:"peers6,omitempty"`
}

type peerDictWire struct {
	IP   string `bencode:"ip"`
	Port int64  `bencode:"port"`
	ID   []byte `bencode:"peer id,omitempty"`
}

// BuildURL renders the announce as a tracker URL.
func (r *AnnounceRequest) BuildURL(announceURL string) (string, error) {
	base, err := url.Parse(announceURL)
	if err != nil {
		return "", fmt.Errorf("tracker: bad announce URL %q: %w", announceURL, err)
	}

	params := url.Values{}
	// info_hash and peer_id are raw binary, url.Values percent-encodes them.
	params.Set("info_hash", string(r.InfoHash[:]))
	params.Set("peer_id", string(r.PeerID[:]))
	params.Set("port", strconv.Itoa(int(r.Port)))
	params.Set("uploaded", strconv.FormatInt(r.Uploaded, 10))
	params.Set("downloaded", strconv.FormatInt(r.Downloaded, 10))
	params.Set("left", strconv.FormatInt(r.Left, 10))
	if r.Compact {
		params.Set("compact", "1")
	} else {
		params.Set("compact", "0")
	}
	if r.NoPeerID {
		params.Set("no_peer_id", "1")
	}
	if r.Event != EventNone {
		params.Set("event", string(r.Event))
	}
	if r.NumWant > 0 {
		params.Set("numwant", strconv.Itoa(r.NumWant))
	}
	if r.Key != "" {
		params.Set("key", r.Key)
	}
	if r.TrackerID != "" {
		params.Set("trackerid", r.TrackerID)
	}

	// Preserve any query already on the announce URL; some trackers put a
	// passkey there.
	if base.RawQuery != "" {
		base.RawQuery += "&" + params.Encode()
	} else {
		base.RawQuery = params.Encode()
	}
	return base.String(), nil
}

// GeneratePeerID returns a fresh random peer ID with this client's prefix.
func GeneratePeerID() ([20]byte, error) {
	var id [20]byte
	copy(id[:], peerIDPrefix)
	if _, err := rand.Read(id[len(peerIDPrefix):]); err != nil {
		return id, fmt.Errorf("tracker: could not generate a peer ID: %w", err)
	}
	return id, nil
}

// Client announces to HTTP(S) and UDP trackers.
type Client struct {
	http *http.Client

	// udpMu guards udpConns, BEP 15's per-tracker connection-id cache. UDP
	// announces are infrequent enough (one per tracker per interval) that a
	// single mutex across every tracker this Client ever talks to is not a
	// contention concern.
	udpMu    sync.Mutex
	udpConns map[string]*udpConn
}

// NewClient returns a tracker client. Passing nil uses a default HTTP client
// with a 45 second timeout.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{http: httpClient}
}

// Announce sends one announce and parses the reply, dispatching to the UDP
// (BEP 15) or HTTP(S) implementation by the announce URL's scheme.
func (c *Client) Announce(ctx context.Context, announceURL string, req AnnounceRequest) (*AnnounceResponse, error) {
	u, err := url.Parse(announceURL)
	if err != nil {
		return nil, fmt.Errorf("tracker: invalid announce URL %q: %w", announceURL, err)
	}
	switch u.Scheme {
	case "udp":
		return c.announceUDP(ctx, u, req)
	case "http", "https":
		return c.announceHTTP(ctx, announceURL, req)
	default:
		return nil, fmt.Errorf("tracker: unsupported announce scheme %q", u.Scheme)
	}
}

// announceHTTP is the original HTTP(S) announce path.
func (c *Client) announceHTTP(ctx context.Context, announceURL string, req AnnounceRequest) (*AnnounceResponse, error) {
	target, err := req.BuildURL(announceURL)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("tracker: could not build request: %w", err)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tracker: announce to %s failed: %w", announceURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("tracker: %s returned status %d: %s", announceURL, resp.StatusCode, snippet)
	}

	// A tracker is not trusted to be well behaved: cap what we are willing to
	// read so a hostile or broken one cannot stream gigabytes at us.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("tracker: reading response from %s: %w", announceURL, err)
	}

	return parseAnnounceResponse(announceURL, body)
}

func parseAnnounceResponse(announceURL string, body []byte) (*AnnounceResponse, error) {
	var wire announceWire
	if err := bencode.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("tracker: could not decode the response from %s: %w", announceURL, err)
	}
	if wire.FailureReason != "" {
		return nil, &ErrTrackerFailure{URL: announceURL, Reason: wire.FailureReason}
	}

	resp := &AnnounceResponse{
		WarningMessage: wire.WarningMessage,
		TrackerID:      wire.TrackerID,
		Complete:       int(wire.Complete),
		Incomplete:     int(wire.Incomplete),
	}
	if wire.Interval > 0 {
		resp.Interval = time.Duration(wire.Interval) * time.Second
	}
	if wire.MinInterval > 0 {
		resp.MinInterval = time.Duration(wire.MinInterval) * time.Second
	}

	peers, err := decodePeers(wire.Peers, net.IPv4len)
	if err != nil {
		return nil, err
	}
	peers6, err := decodePeers(wire.Peers6, net.IPv6len)
	if err != nil {
		return nil, err
	}
	resp.Peers = append(peers, peers6...)
	if len(resp.Peers) > maxPeersInResponse {
		resp.Peers = resp.Peers[:maxPeersInResponse]
	}
	return resp, nil
}

// decodePeers handles both peer list encodings. ipLen selects the compact
// layout: 4 for "peers" (BEP 23) and 16 for "peers6" (BEP 7).
func decodePeers(raw bencode.RawMessage, ipLen int) ([]PeerInfo, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	// A bencoded list starts with 'l'; a string starts with a digit.
	if raw[0] == 'l' {
		var dicts []peerDictWire
		if err := bencode.Unmarshal(raw, &dicts); err != nil {
			return nil, fmt.Errorf("tracker: bad peer list: %w", err)
		}
		peers := make([]PeerInfo, 0, len(dicts))
		for _, d := range dicts {
			ip := net.ParseIP(d.IP)
			if ip == nil || d.Port <= 0 || d.Port > 65535 {
				continue // skip the bad entry rather than fail the announce
			}
			peers = append(peers, PeerInfo{IP: ip, Port: uint16(d.Port)})
		}
		return peers, nil
	}

	var packed []byte
	if err := bencode.Unmarshal(raw, &packed); err != nil {
		return nil, fmt.Errorf("tracker: bad compact peer list: %w", err)
	}
	entry := ipLen + 2
	if len(packed)%entry != 0 {
		return nil, fmt.Errorf("tracker: compact peer list is %d bytes, not a multiple of %d", len(packed), entry)
	}

	peers := make([]PeerInfo, 0, len(packed)/entry)
	for off := 0; off+entry <= len(packed); off += entry {
		ip := make(net.IP, ipLen)
		copy(ip, packed[off:off+ipLen])
		port := uint16(packed[off+ipLen])<<8 | uint16(packed[off+ipLen+1])
		if port == 0 || ip.IsUnspecified() {
			continue
		}
		peers = append(peers, PeerInfo{IP: ip, Port: port})
	}
	return peers, nil
}

// ErrNoPeers reports an announce that succeeded but returned nothing usable.
var ErrNoPeers = errors.New("tracker: announce returned no peers")
