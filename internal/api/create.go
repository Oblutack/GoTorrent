package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// CreateTorrentRequest is POST /api/v1/torrents/create's body. SourcePath
// is a file or directory on the machine gottrentd itself runs on — the
// same thing gottrent create's single positional argument names, since
// this route wraps the identical metainfo.Build/CollectFiles, not
// something that accepts uploaded content (there is nothing to upload:
// the whole point of creating a torrent is that the data already exists
// somewhere real).
type CreateTorrentRequest struct {
	SourcePath string `json:"sourcePath"`
	// Name defaults to SourcePath's own base name, same as gottrent create.
	Name        string `json:"name,omitempty"`
	PieceLength int64  `json:"pieceLength,omitempty"`
	Private     bool   `json:"private,omitempty"`
	Comment     string `json:"comment,omitempty"`
	CreatedBy   string `json:"createdBy,omitempty"`
	// Trackers each become their own announce-list tier, same as gottrent
	// create's repeatable -tracker flag.
	Trackers []string `json:"trackers,omitempty"`
	WebSeeds []string `json:"webSeeds,omitempty"`
	// StartSeeding also adds the freshly-built torrent to this engine,
	// seeding directly from SourcePath in place rather than requiring a
	// separate upload of the very file this route just built - the
	// download directory is derived as SourcePath's own parent directory,
	// since the created torrent's file paths are already relative to
	// SourcePath itself (the same layout gottrent create's own -out .torrent
	// describes, just added instead of only written to disk).
	StartSeeding bool     `json:"startSeeding,omitempty"`
	Category     string   `json:"category,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// CreateTorrentResponse is POST /api/v1/torrents/create's success body.
// TorrentFile is the raw .torrent bytes (base64 in JSON, encoding/json's
// standard treatment of a []byte field) - the caller's only way to get a
// local copy of what was built, whether or not StartSeeding also added it
// here.
type CreateTorrentResponse struct {
	InfoHash    metainfo.Hash `json:"infoHash"`
	Name        string        `json:"name"`
	TotalLength int64         `json:"totalLength"`
	PieceLength int64         `json:"pieceLength"`
	NumPieces   int           `json:"numPieces"`
	TorrentFile []byte        `json:"torrentFile"`
}

// CreateTorrentHandler serves POST /api/v1/torrents/create - wraps
// metainfo.Build/CollectFiles, the same machinery gottrent create's CLI
// command already uses, so a Desktop client can offer a Create Torrent
// dialog without gottrentd growing a second implementation of "walk a
// directory and hash its pieces." uploadDir is only used when
// StartSeeding is set - see saveTorrentBytes's own doc comment for why
// the resulting .torrent has to persist there, not a temp file, if this
// torrent is going to survive a future restart.
func CreateTorrentHandler(e *engine.Engine, uploadDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateTorrentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "decoding request body: "+err.Error())
			return
		}
		if req.SourcePath == "" {
			writeError(w, http.StatusBadRequest, `"sourcePath" is required`)
			return
		}

		name := req.Name
		if name == "" {
			name = filepath.Base(filepath.Clean(req.SourcePath))
		}
		files, err := metainfo.CollectFiles(req.SourcePath)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("reading %s: %v", req.SourcePath, err))
			return
		}

		opts := metainfo.CreateOptions{
			Name:        name,
			PieceLength: req.PieceLength,
			Private:     req.Private,
			Comment:     req.Comment,
			CreatedBy:   req.CreatedBy,
			UrlList:     req.WebSeeds,
			Files:       files,
		}
		if len(req.Trackers) > 0 {
			opts.Announce = req.Trackers[0]
			for _, u := range req.Trackers {
				opts.AnnounceList = append(opts.AnnounceList, []string{u})
			}
		}

		raw, mi, err := metainfo.Build(opts)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		if req.StartSeeding {
			path, err := saveTorrentBytes(uploadDir, bytes.NewReader(raw))
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			downloadDir := filepath.Dir(filepath.Clean(req.SourcePath))
			addOpts := engine.AddOptions{Category: req.Category, Tags: req.Tags}
			if _, err := e.AddWithOptions(path, downloadDir, addOpts); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}

		writeJSON(w, http.StatusCreated, CreateTorrentResponse{
			InfoHash:    mi.InfoHash,
			Name:        mi.Info.Name,
			TotalLength: mi.TotalLength,
			PieceLength: mi.Info.PieceLength,
			NumPieces:   mi.NumPieces(),
			TorrentFile: raw,
		})
	}
}
