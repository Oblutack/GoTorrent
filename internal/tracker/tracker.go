package tracker

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Oblutack/GoTorrent/internal/gobencode"
)

type TrackerRequest struct {
	InfoHash   [20]byte
	PeerID     [20]byte
	Port       uint16
	Uploaded   int64
	Downloaded int64
	Left       int64
	Compact    int
	NoPeerID   int
	Event      string
	NumWant    int
	Key        string
	TrackerID  string
}

type PeerInfo struct {
	IP   net.IP
	Port uint16
}

type TrackerResponse struct {
	FailureReason  string `bencode:"failure reason"`
	WarningMessage string `bencode:"warning message"`
	Interval       int    `bencode:"interval"`
	MinInterval    int    `bencode:"min interval"`
	TrackerID      string `bencode:"tracker id"`
	Complete       int    `bencode:"complete"`
	Incomplete     int    `bencode:"incomplete"`
	Peers          []PeerInfo
}

func (tr *TrackerRequest) BuildURL(announceURL string) (string, error) {
	base, err := url.Parse(announceURL)
	if err != nil {
		return "", err
	}

	params := url.Values{}
	params.Add("info_hash", string(tr.InfoHash[:]))
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
	if tr.NumWant > 0 {
		params.Add("numwant", strconv.Itoa(tr.NumWant))
	}
	if tr.Key != "" {
		params.Add("key", tr.Key)
	}
	if tr.TrackerID != "" {
		params.Add("trackerid", tr.TrackerID)
	}

	base.RawQuery = params.Encode()
	return base.String(), nil
}

func GeneratePeerID() ([20]byte, error) {
	var id [20]byte

	copy(id[:], "-GT0001-")

	_, err := rand.Read(id[8:])
	if err != nil {
		return id, fmt.Errorf("failed to generate random peer id suffix: %w", err)
	}

	return id, nil
}

func Announce(announceURL string, req TrackerRequest) (*TrackerResponse, error) {
	url, err := req.BuildURL(announceURL)
	if err != nil {
		return nil, fmt.Errorf("tracker: failed to build announce URL: %w", err)
	}

	httpClient := http.Client{
		Timeout: 45 * time.Second,
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

	decodedResponse, err := gobencode.Decode(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tracker: failed to decode tracker response: %w", err)
	}

	responseMap, ok := decodedResponse.(map[string]interface{})
	if !ok {
		return nil, errors.New("tracker: tracker response is not a dictionary")
	}

	trackerResp := &TrackerResponse{}
	if failure, ok := responseMap["failure reason"].(string); ok {
		trackerResp.FailureReason = failure
		return trackerResp, fmt.Errorf("tracker error: %s", failure)
	}
	if warning, ok := responseMap["warning message"].(string); ok {
		trackerResp.WarningMessage = warning

	}
	if interval, ok := responseMap["interval"].(int64); ok {
		trackerResp.Interval = int(interval)
	} else {

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

	if peersData, ok := responseMap["peers"]; ok {
		if peersStr, okStr := peersData.(string); okStr {

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

					if pi.IP != nil && pi.Port > 0 {
						trackerResp.Peers = append(trackerResp.Peers, pi)
					}
				}
			}
		}
	}

	return trackerResp, nil
}
