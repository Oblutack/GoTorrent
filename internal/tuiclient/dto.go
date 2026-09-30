// Package tuiclient is a Go client for gottrentd's control API
// (internal/api) — the terminal UI's own equivalent of Desktop's C#
// IEngineClient/EngineClient, talking the exact same REST+WS surface Phase
// 4 already built and Desktop already proved out. Its DTOs are this
// package's own hand-written structs, field-for-field matching
// internal/api's real JSON responses, rather than importing
// internal/metainfo.Hash/internal/torrent.State/internal/picker.Priority
// directly — the same "a public API response is a contract, not an
// internal type" reasoning internal/api's own doc comment already gives
// for not reusing engine.Summary/torrent.Stats, applied one hop further
// out: gottrentd is free to add a new torrent.State value before this
// client knows about it, and a plain string field never throws decoding
// one the way a strict Go enum type would (the identical reasoning
// Desktop's own TorrentSummary.State doc comment states for the same
// choice, in C#).
package tuiclient

import "time"

// TorrentSummary is GET /api/v1/torrents' per-entry shape (and the
// embedded base of TorrentDetail) — see internal/api/torrents.go's own
// TorrentSummary for the authoritative field list this mirrors.
type TorrentSummary struct {
	InfoHash        string    `json:"infoHash"`
	Name            string    `json:"name"`
	State           string    `json:"state"`
	Downloaded      int64     `json:"downloaded"`
	Uploaded        int64     `json:"uploaded"`
	Left            int64     `json:"left"`
	TotalLength     int64     `json:"totalLength"`
	NumPieces       int       `json:"numPieces"`
	HavePieces      int       `json:"havePieces"`
	PeerCount       int       `json:"peerCount"`
	SeedCount       int       `json:"seedCount"`
	LeechCount      int       `json:"leechCount"`
	MinAvailability int       `json:"minAvailability"`
	SeedRatio       float64   `json:"seedRatio"`
	Private         bool      `json:"private"`
	Category        string    `json:"category,omitempty"`
	Tags            []string  `json:"tags,omitempty"`
	QueuePosition   int       `json:"queuePosition"`
	ForceStart      bool      `json:"forceStart"`
	AddedOn         time.Time `json:"addedOn"`
}

// Progress is downloaded/total as a 0-1 fraction, 0 when nothing is known
// yet (TotalLength is 0 before metadata arrives) — the same shape every
// client of this API ends up computing itself, so it lives here once.
func (s TorrentSummary) Progress() float64 {
	if s.TotalLength <= 0 {
		return 0
	}
	return float64(s.TotalLength-s.Left) / float64(s.TotalLength)
}

// TorrentDetail is GET /api/v1/torrents/{hash}'s response.
type TorrentDetail struct {
	TorrentSummary
	Source                 string     `json:"source"`
	DownloadDir            string     `json:"downloadDir"`
	ContentPath            string     `json:"contentPath,omitempty"`
	InEndgame              bool       `json:"inEndgame"`
	SeedingDurationSeconds float64    `json:"seedingDurationSeconds"`
	Comment                string     `json:"comment,omitempty"`
	CreatedBy              string     `json:"createdBy,omitempty"`
	CreationDate           *time.Time `json:"creationDate,omitempty"`
	PieceLength            int64      `json:"pieceLength,omitempty"`
}

// FileEntry is one entry of GET /api/v1/torrents/{hash}/files.
type FileEntry struct {
	Path     []string `json:"path"`
	Length   int64    `json:"length"`
	Priority string   `json:"priority"`
	Padding  bool     `json:"padding,omitempty"`
}

// PeerEntry is one entry of GET /api/v1/torrents/{hash}/peers.
type PeerEntry struct {
	Addr           string  `json:"addr"`
	Outbound       bool    `json:"outbound"`
	Downloaded     int64   `json:"downloaded"`
	Uploaded       int64   `json:"uploaded"`
	AmChoking      bool    `json:"amChoking"`
	AmInterested   bool    `json:"amInterested"`
	PeerChoking    bool    `json:"peerChoking"`
	PeerInterested bool    `json:"peerInterested"`
	Progress       float64 `json:"progress"`
	PeerID         string  `json:"peerId,omitempty"`
}

// TrackerEntry is one entry of GET /api/v1/torrents/{hash}/trackers.
type TrackerEntry struct {
	URL          string    `json:"url"`
	LastAnnounce time.Time `json:"lastAnnounce"`
	LastError    string    `json:"lastError,omitempty"`
	Seeders      int       `json:"seeders"`
	Leechers     int       `json:"leechers"`
}

// PiecesResponse is GET /api/v1/torrents/{hash}/pieces' response. Bitfield is
// BEP 3's packed layout (one bit per piece, most-significant bit first);
// encoding/json decodes the API's base64 string into the []byte directly.
type PiecesResponse struct {
	NumPieces int    `json:"numPieces"`
	HaveCount int    `json:"haveCount"`
	Bitfield  []byte `json:"bitfield"`
}

// Has reports whether piece i is marked have in the bitfield.
func (p PiecesResponse) Has(i int) bool {
	if i < 0 || i >= p.NumPieces || i/8 >= len(p.Bitfield) {
		return false
	}
	return p.Bitfield[i/8]&(0x80>>(uint(i)%8)) != 0
}

// SessionStats is GET /api/v1/session's response.
type SessionStats struct {
	TorrentCount     int    `json:"torrentCount"`
	DownloadingCount int    `json:"downloadingCount"`
	SeedingCount     int    `json:"seedingCount"`
	PausedCount      int    `json:"pausedCount"`
	ErrorCount       int    `json:"errorCount"`
	TotalDownloaded  int64  `json:"totalDownloaded"`
	TotalUploaded    int64  `json:"totalUploaded"`
	TotalPeerCount   int    `json:"totalPeerCount"`
	AltSpeedEnabled  bool   `json:"altSpeedEnabled"`
	ListenPort       uint16 `json:"listenPort"`
	ExternalPort     uint16 `json:"externalPort"`
	PortMapped       bool   `json:"portMapped"`
	DHTRunning       bool   `json:"dhtRunning"`
	DHTNodeCount     int    `json:"dhtNodeCount"`
	LSDRunning       bool   `json:"lsdRunning"`
	PEXEnabled       bool   `json:"pexEnabled"`
	FreeDiskBytes    int64  `json:"freeDiskBytes"`
}

// WSEvent is one message off GET /api/v1/events.
type WSEvent struct {
	Kind       string        `json:"kind"`
	Time       time.Time     `json:"time"`
	InfoHash   string        `json:"infoHash,omitempty"`
	State      string        `json:"state,omitempty"`
	PeerAddr   string        `json:"peerAddr,omitempty"`
	PieceIndex *int          `json:"pieceIndex,omitempty"`
	Session    *SessionStats `json:"session,omitempty"`
}

// AddRequest is POST /api/v1/torrents' JSON body (the magnet/URL path —
// file upload is multipart, handled separately by AddTorrentFile).
type AddRequest struct {
	Magnet      string   `json:"magnet,omitempty"`
	URL         string   `json:"url,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	DownloadDir string   `json:"downloadDir,omitempty"`
}

// AddResponse is POST /api/v1/torrents' response.
type AddResponse struct {
	InfoHash string `json:"infoHash"`
}

// errorBody mirrors internal/api's own {"error": "..."} envelope — every
// non-2xx response from this API uses it, so decodeError (client.go) only
// ever needs to try one shape.
type errorBody struct {
	Error string `json:"error"`
}
