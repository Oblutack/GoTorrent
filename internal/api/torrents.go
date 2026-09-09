package api

import (
	"net/http"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/picker"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// TorrentSummary is one torrent's list-view shape - deliberately not
// engine.Summary reused directly, even though the fields are a close
// match: an API response type is a public contract a client depends on,
// while engine.Summary is free to grow fields serving purely internal
// needs (as it already has, e.g. QueuePosition/ForceStart's persistence
// caveats) without that becoming a breaking API change.
type TorrentSummary struct {
	InfoHash      metainfo.Hash `json:"infoHash"`
	Name          string        `json:"name"`
	State         torrent.State `json:"state"`
	Downloaded    int64         `json:"downloaded"`
	Uploaded      int64         `json:"uploaded"`
	Left          int64         `json:"left"`
	TotalLength   int64         `json:"totalLength"`
	NumPieces     int           `json:"numPieces"`
	HavePieces    int           `json:"havePieces"`
	PeerCount     int           `json:"peerCount"`
	SeedRatio     float64       `json:"seedRatio"`
	Private       bool          `json:"private"`
	Category      string        `json:"category,omitempty"`
	Tags          []string      `json:"tags,omitempty"`
	QueuePosition int           `json:"queuePosition"`
	ForceStart    bool          `json:"forceStart"`
}

// TorrentDetail is the single-torrent view - TorrentSummary plus the
// fields not worth carrying on every entry of a potentially-large list.
type TorrentDetail struct {
	TorrentSummary
	Source                 string  `json:"source"`
	DownloadDir            string  `json:"downloadDir"`
	ContentPath            string  `json:"contentPath,omitempty"`
	InEndgame              bool    `json:"inEndgame"`
	SeedingDurationSeconds float64 `json:"seedingDurationSeconds"`
}

func summaryDTO(s engine.Summary) TorrentSummary {
	return TorrentSummary{
		InfoHash:      s.InfoHash,
		Name:          s.Name,
		State:         s.Stats.State,
		Downloaded:    s.Stats.Downloaded,
		Uploaded:      s.Stats.Uploaded,
		Left:          s.Stats.Left,
		TotalLength:   s.Stats.TotalLength,
		NumPieces:     s.Stats.NumPieces,
		HavePieces:    s.Stats.HavePieces,
		PeerCount:     s.Stats.PeerCount,
		SeedRatio:     s.Stats.SeedRatio,
		Private:       s.Private,
		Category:      s.Category,
		Tags:          s.Tags,
		QueuePosition: s.QueuePosition,
		ForceStart:    s.ForceStart,
	}
}

// ListTorrentsHandler serves GET /api/v1/torrents.
func ListTorrentsHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list := e.List()
		out := make([]TorrentSummary, len(list))
		for i, s := range list {
			out[i] = summaryDTO(s)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// parseHashParam decodes the {hash} path value, writing a 400 and
// returning ok=false itself on failure so every route needing it can just
// `if !ok { return }`.
func parseHashParam(w http.ResponseWriter, r *http.Request) (metainfo.Hash, bool) {
	hash, err := metainfo.ParseHash(r.PathValue("hash"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return metainfo.Hash{}, false
	}
	return hash, true
}

// getManagedTorrent resolves {hash} to both its Summary and its
// *torrent.Torrent (Get and GetSummary can momentarily disagree about
// whether hash is still managed - engine.mu is released between the two
// lock/unlock cycles inside them - so a route needing both fetches
// GetSummary first and only proceeds to Get if that succeeded, treating
// Get then failing as the same 404 rather than a distinct error case).
// Writes the appropriate error response and returns ok=false on any
// failure.
func getManagedTorrent(w http.ResponseWriter, e *engine.Engine, hash metainfo.Hash) (engine.Summary, *torrent.Torrent, bool) {
	summary, ok := e.GetSummary(hash)
	if !ok {
		writeError(w, http.StatusNotFound, "torrent not managed by this engine")
		return engine.Summary{}, nil, false
	}
	tr, ok := e.Get(hash)
	if !ok {
		writeError(w, http.StatusNotFound, "torrent not managed by this engine")
		return engine.Summary{}, nil, false
	}
	return summary, tr, true
}

// TorrentDetailHandler serves GET /api/v1/torrents/{hash}.
func TorrentDetailHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash, ok := parseHashParam(w, r)
		if !ok {
			return
		}
		summary, tr, ok := getManagedTorrent(w, e, hash)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, TorrentDetail{
			TorrentSummary:         summaryDTO(summary),
			Source:                 summary.Source,
			DownloadDir:            summary.DownloadDir,
			ContentPath:            tr.ContentPath(),
			InEndgame:              summary.Stats.InEndgame,
			SeedingDurationSeconds: summary.Stats.SeedingDuration.Seconds(),
		})
	}
}

// FileEntry is one file of a torrent's file tree.
type FileEntry struct {
	// Path is the file's path segments relative to the torrent's own root -
	// a single-file torrent reports exactly one entry, [name].
	Path     []string        `json:"path"`
	Length   int64           `json:"length"`
	Priority picker.Priority `json:"priority"`
}

// FilesHandler serves GET /api/v1/torrents/{hash}/files. A magnet-shaped
// torrent whose metadata has not arrived yet reports an empty list rather
// than an error - "no files known yet" is a legitimate, temporary state,
// not a client mistake.
func FilesHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash, ok := parseHashParam(w, r)
		if !ok {
			return
		}
		summary, tr, ok := getManagedTorrent(w, e, hash)
		if !ok {
			return
		}

		mi := tr.Metadata()
		if mi == nil {
			writeJSON(w, http.StatusOK, []FileEntry{})
			return
		}

		priorities := summary.Stats.FilePriorities
		priorityFor := func(i int) picker.Priority {
			if i < len(priorities) {
				return priorities[i]
			}
			return picker.PriorityNormal
		}

		var files []FileEntry
		if mi.Info.IsMultiFile() {
			files = make([]FileEntry, len(mi.Info.Files))
			for i, f := range mi.Info.Files {
				files[i] = FileEntry{Path: f.Path, Length: f.Length, Priority: priorityFor(i)}
			}
		} else {
			files = []FileEntry{{Path: []string{mi.Info.Name}, Length: mi.Info.Length, Priority: priorityFor(0)}}
		}
		writeJSON(w, http.StatusOK, files)
	}
}
