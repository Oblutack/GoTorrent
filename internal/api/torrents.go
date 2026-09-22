package api

import (
	"net/http"
	"time"

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
	InfoHash        metainfo.Hash `json:"infoHash"`
	Name            string        `json:"name"`
	State           torrent.State `json:"state"`
	Downloaded      int64         `json:"downloaded"`
	Uploaded        int64         `json:"uploaded"`
	Left            int64         `json:"left"`
	TotalLength     int64         `json:"totalLength"`
	NumPieces       int           `json:"numPieces"`
	HavePieces      int           `json:"havePieces"`
	PeerCount       int           `json:"peerCount"`
	SeedCount       int           `json:"seedCount"`
	LeechCount      int           `json:"leechCount"`
	MinAvailability int           `json:"minAvailability"`
	SeedRatio       float64       `json:"seedRatio"`
	Private         bool          `json:"private"`
	Category        string        `json:"category,omitempty"`
	Tags            []string      `json:"tags,omitempty"`
	QueuePosition   int           `json:"queuePosition"`
	ForceStart      bool          `json:"forceStart"`
	// QueueHeld is true only when the queue itself is why this torrent is
	// Paused (a slot will free up once another torrent finishes or is
	// paused) - never for a direct user Pause or a seed-limit auto-pause.
	// Stage 6's "why is this slow?" diagnostics panel needs this to tell
	// "queue-held, nothing actually wrong" apart from every other reason.
	QueueHeld bool `json:"queueHeld"`
	// AddedOn is when this torrent was first added - persisted in the
	// manifest (see engine.Summary.AddedAt), so it survives a restart
	// rather than resetting to "now" on every reload.
	AddedOn time.Time `json:"addedOn"`
	// CompletedOn is when this torrent first reached Seeding, or nil if it
	// hasn't yet (or completed before this field existed - the manifest
	// only started recording it once this field was added, so an
	// already-complete torrent reloaded from an older manifest reports nil
	// rather than a guessed value).
	CompletedOn *time.Time `json:"completedOn,omitempty"`
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
	// Comment, CreatedBy, CreationDate, and PieceLength come straight from
	// the torrent's own metainfo.MetaInfo - all zero-valued until metadata
	// is known (a magnet still FetchingMetadata). CreationDate is BEP 3's
	// own unix-seconds convention, round-tripped as-is rather than
	// reinterpreted.
	Comment      string     `json:"comment,omitempty"`
	CreatedBy    string     `json:"createdBy,omitempty"`
	CreationDate *time.Time `json:"creationDate,omitempty"`
	PieceLength  int64      `json:"pieceLength,omitempty"`
}

func summaryDTO(s engine.Summary) TorrentSummary {
	summary := TorrentSummary{
		InfoHash:        s.InfoHash,
		Name:            s.Name,
		State:           s.Stats.State,
		Downloaded:      s.Stats.Downloaded,
		Uploaded:        s.Stats.Uploaded,
		Left:            s.Stats.Left,
		TotalLength:     s.Stats.TotalLength,
		NumPieces:       s.Stats.NumPieces,
		HavePieces:      s.Stats.HavePieces,
		PeerCount:       s.Stats.PeerCount,
		SeedCount:       s.Stats.SeedCount,
		LeechCount:      s.Stats.LeechCount,
		MinAvailability: s.Stats.MinAvailability,
		SeedRatio:       s.Stats.SeedRatio,
		Private:         s.Private,
		Category:        s.Category,
		Tags:            s.Tags,
		QueuePosition:   s.QueuePosition,
		ForceStart:      s.ForceStart,
		QueueHeld:       s.QueueHeld,
		AddedOn:         s.AddedAt,
	}
	if !s.CompletedAt.IsZero() {
		completedAt := s.CompletedAt
		summary.CompletedOn = &completedAt
	}
	return summary
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
		detail := TorrentDetail{
			TorrentSummary:         summaryDTO(summary),
			Source:                 summary.Source,
			DownloadDir:            summary.DownloadDir,
			ContentPath:            tr.ContentPath(),
			InEndgame:              summary.Stats.InEndgame,
			SeedingDurationSeconds: summary.Stats.SeedingDuration.Seconds(),
		}
		if mi := tr.Metadata(); mi != nil {
			detail.Comment = mi.Comment
			detail.CreatedBy = mi.CreatedBy
			if mi.CreationDate != 0 {
				creationDate := time.Unix(mi.CreationDate, 0).UTC()
				detail.CreationDate = &creationDate
			}
			detail.PieceLength = mi.Info.PieceLength
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

// FileEntry is one file of a torrent's file tree.
type FileEntry struct {
	// Path is the file's path segments relative to the torrent's own root -
	// a single-file torrent reports exactly one entry, [name].
	Path     []string        `json:"path"`
	Length   int64           `json:"length"`
	Priority picker.Priority `json:"priority"`
	// Padding is true for a BEP 47 padding file - always false for a
	// single-file torrent, which has no per-file attr at all. A caller can
	// use this to grey one out or hide it, the way this client's own
	// picker already defaults it to PrioritySkip.
	Padding bool `json:"padding,omitempty"`
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
				files[i] = FileEntry{Path: f.Path, Length: f.Length, Priority: priorityFor(i), Padding: f.IsPadding()}
			}
		} else {
			files = []FileEntry{{Path: []string{mi.Info.Name}, Length: mi.Info.Length, Priority: priorityFor(0)}}
		}
		writeJSON(w, http.StatusOK, files)
	}
}
