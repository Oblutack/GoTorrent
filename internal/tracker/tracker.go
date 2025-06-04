package tracker

import (
	"crypto/rand" // For generating random part of PeerID
	"errors"
	"fmt"
	"io/ioutil" // For http.Response.Body reading (older, consider io.ReadAll)
	"net"       // For net.IP
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Oblutack/GoTorrent/internal/gobencode" // Naš bencode paket
	// "github.com/Oblutack/GoTorrent/internal/metainfo" // Ako treba da pristupimo MetaInfo direktno
)

// TrackerRequest holds the parameters to be sent to the tracker.
type TrackerRequest struct {
	InfoHash   [20]byte // The info_hash of the torrent
	PeerID     [20]byte // Our 20-byte peer ID
	Port       uint16   // The port we are listening on for peer connections
	Uploaded   int64    // Total bytes uploaded so far
	Downloaded int64    // Total bytes downloaded so far
	Left       int64    // Bytes remaining to download
	Compact    int      // 1 for compact peer list, 0 otherwise (usually 1)
	NoPeerID   int      // 1 if tracker should not send peer_ids in peer list
	Event      string   // "started", "completed", "stopped", or empty
	NumWant    int      // Optional: Number of peers we want (usually around 50)
	Key        string   // Optional: An additional identification that is not shared with any other peers
	TrackerID  string   // Optional: If a previous announce contained a tracker id, it should be set here
}

// PeerInfo represents a single peer from the tracker's response.
type PeerInfo struct {
	IP   net.IP
	Port uint16
	// PeerID [20]byte // Optional, not always present, especially in compact mode
}

// TrackerResponse holds the data parsed from a tracker's bencoded response.
type TrackerResponse struct {
	FailureReason  string     `bencode:"failure reason"`  // If present, the request failed
	WarningMessage string     `bencode:"warning message"` // Optional
	Interval       int        `bencode:"interval"`        // seconds client should wait before next regular re-announce
	MinInterval    int        `bencode:"min interval"`    // Optional: minimum announce interval
	TrackerID      string     `bencode:"tracker id"`      // Optional
	Complete       int        `bencode:"complete"`        // Number of seeders
	Incomplete     int        `bencode:"incomplete"`      // Number of leechers
	Peers          []PeerInfo // Parsed list of peers
	// RawPeers can be string (compact format) or []interface{} (list of dicts)
	// We will parse this into Peers.
}

// BuildURL constructs the tracker announce URL with all necessary query parameters.
func (tr *TrackerRequest) BuildURL(announceURL string) (string, error) {
	base, err := url.Parse(announceURL)
	if err != nil {
		return "", err
	}

	params := url.Values{}
	params.Add("info_hash", string(tr.InfoHash[:])) // Must be URL-encoded correctly by .Encode()
	params.Add("peer_id", string(tr.PeerID[:]))
	params.Add("port", strconv.Itoa(int(tr.Port)))
	params.Add("uploaded", strconv.FormatInt(tr.Uploaded, 10))
	params.Add("downloaded", strconv.FormatInt(tr.Downloaded, 10))
	params.Add("left", strconv.FormatInt(tr.Left, 10))
	params.Add("compact", strconv.Itoa(tr.Compact))

	if tr.NoPeerID != 0 {
		params.Add("no_peer_id", strconv.Itoa(tr.NoPeerID))
	}
	if tr.Event != "" {
		params.Add("event", tr.Event)
	}
	if tr.NumWant > 0 { // Only add if NumWant is specified and positive
		params.Add("numwant", strconv.Itoa(tr.NumWant))
	}
	if tr.Key != "" {
		params.Add("key", tr.Key)
	}
	if tr.TrackerID != "" {
		params.Add("trackerid", tr.TrackerID)
	}

	base.RawQuery = params.Encode() // This handles URL encoding of parameters
	return base.String(), nil
}

// GeneratePeerID creates a 20-byte peer ID for our client.
// Example format: -GT0001-<12 random alphanumeric chars>
func GeneratePeerID() ([20]byte, error) {
	var id [20]byte
	// Client identifier and version
	copy(id[:], "-GT0001-") // 8 bytes
	// Generate 12 random bytes for the rest
	_, err := rand.Read(id[8:])
	if err != nil {
		return id, fmt.Errorf("failed to generate random peer id suffix: %w", err)
	}
	// Ensure random part is somewhat printable/URL-safe if desired,
	// though raw random bytes are fine for the protocol.
	// For simplicity, raw random bytes are okay.
	return id, nil
}


// Announce sends an announce request to the tracker and parses the response.
func Announce(announceURL string, req TrackerRequest) (*TrackerResponse, error) {
	url, err := req.BuildURL(announceURL)
	if err != nil {
		return nil, fmt.Errorf("tracker: failed to build announce URL: %w", err)
	}

	// Make the HTTP GET request
	// TODO: Consider custom client with timeout
	httpClient := http.Client{
		Timeout: 45 * time.Second, // Example timeout
	}
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("tracker: HTTP GET request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("tracker: request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Decode the bencoded response
	decodedResponse, err := gobencode.Decode(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tracker: failed to decode tracker response: %w", err)
	}

	responseMap, ok := decodedResponse.(map[string]interface{})
	if !ok {
		return nil, errors.New("tracker: tracker response is not a dictionary")
	}

	// Parse the map into TrackerResponse struct
	trackerResp := &TrackerResponse{}
	if failure, ok := responseMap["failure reason"].(string); ok {
		trackerResp.FailureReason = failure
		return trackerResp, fmt.Errorf("tracker error: %s", failure) // Return immediately on failure
	}
	if warning, ok := responseMap["warning message"].(string); ok {
		trackerResp.WarningMessage = warning
		// Log warning: fmt.Println("Tracker warning:", warning)
	}
	if interval, ok := responseMap["interval"].(int64); ok {
		trackerResp.Interval = int(interval)
	} else {
		// Interval is usually required
		// return nil, errors.New("tracker: 'interval' missing or not an integer in response")
        // Let's be a bit lenient, some trackers might omit if there's an error elsewhere
	}
	if minInterval, ok := responseMap["min interval"].(int64); ok {
		trackerResp.MinInterval = int(minInterval)
	}
	if tid, ok := responseMap["tracker id"].(string); ok {
		trackerResp.TrackerID = tid
	}
	if complete, ok := responseMap["complete"].(int64); ok {
		trackerResp.Complete = int(complete)
	}
	if incomplete, ok := responseMap["incomplete"].(int64); ok {
		trackerResp.Incomplete = int(incomplete)
	}

	// Parse peers
	if peersData, ok := responseMap["peers"]; ok {
		if peersStr, okStr := peersData.(string); okStr {
			// Compact format (byte string)
			// Each peer is 6 bytes: 4 bytes IP, 2 bytes Port (big-endian)
			if len(peersStr)%6 != 0 {
				return nil, errors.New("tracker: compact peers string length is not a multiple of 6")
			}
			numPeers := len(peersStr) / 6
			trackerResp.Peers = make([]PeerInfo, numPeers)
			for i := 0; i < numPeers; i++ {
				offset := i * 6
				ipBytes := []byte(peersStr[offset : offset+4])
				portBytes := []byte(peersStr[offset+4 : offset+6])
				trackerResp.Peers[i].IP = net.IP(ipBytes)
				trackerResp.Peers[i].Port = (uint16(portBytes[0]) << 8) | uint16(portBytes[1])
			}
		} else if peersList, okList := peersData.([]interface{}); okList {
			// Non-compact format (list of dictionaries)
			trackerResp.Peers = make([]PeerInfo, 0, len(peersList))
			for _, peerEntry := range peersList {
				if peerMap, okMap := peerEntry.(map[string]interface{}); okMap {
					var pi PeerInfo
					if ipStr, ipOk := peerMap["ip"].(string); ipOk {
						pi.IP = net.ParseIP(ipStr)
					}
					if portInt, portOk := peerMap["port"].(int64); portOk {
						pi.Port = uint16(portInt)
					}
					// if peerIDStr, idOk := peerMap["peer id"].(string); idOk {
					// 	copy(pi.PeerID[:], []byte(peerIDStr))
					// }
					if pi.IP != nil && pi.Port > 0 {
						trackerResp.Peers = append(trackerResp.Peers, pi)
					}
				}
			}
		}
	}

	return trackerResp, nil
}