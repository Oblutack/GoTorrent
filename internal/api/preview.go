package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// PreviewFile is one file in a previewed .torrent's file list - not
// FileEntry (4.2's GET .../files shape), deliberately: FileEntry carries a
// Priority, which is a managed torrent's own runtime state and has no
// meaning for a .torrent nothing has been Added from yet.
type PreviewFile struct {
	Path   []string `json:"path"`
	Length int64    `json:"length"`
	// Padding is true for a BEP 47 padding file - see FileEntry's own
	// field of the same name.
	Padding bool `json:"padding,omitempty"`
}

// PreviewResponse is POST /api/v1/torrents/preview's response body -
// exactly what a caller needs to build a real Add-torrent dialog's
// per-file selection (Stage 5), without ever calling engine.AddWithOptions.
type PreviewResponse struct {
	InfoHash    metainfo.Hash `json:"infoHash"`
	Name        string        `json:"name"`
	TotalLength int64         `json:"totalLength"`
	PieceLength int64         `json:"pieceLength"`
	Private     bool          `json:"private"`
	Files       []PreviewFile `json:"files"`
}

// PreviewRequest is POST /api/v1/torrents/preview's JSON body shape - used
// when the request is not a multipart upload, mirroring AddRequest's own
// "url" case. There is no magnet equivalent: a magnet has no file list
// until peers supply metadata, which previewing-before-adding can't wait
// for either way - see this item's own ROADMAP.md wording.
type PreviewRequest struct {
	URL string `json:"url"`
}

// PreviewTorrentHandler serves POST /api/v1/torrents/preview - parses a
// .torrent's real file list without ever adding it (never calls
// engine.AddWithOptions, never writes anything to uploadDir), so a caller
// can build a per-file selection UI before committing to a real Add.
// Dispatches on Content-Type the same way AddTorrentHandler does:
// multipart/form-data (field "torrent") or JSON with a "url".
func PreviewTorrentHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var mi *metainfo.MetaInfo

		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			if err := r.ParseMultipartForm(metainfo.MaxTorrentFileSize); err != nil {
				writeError(w, http.StatusBadRequest, "parsing multipart form: "+err.Error())
				return
			}
			file, _, err := r.FormFile("torrent")
			if err != nil {
				writeError(w, http.StatusBadRequest, `missing "torrent" file field`)
				return
			}
			defer file.Close()

			parsed, _, err := parseTorrentBytes(file)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			mi = parsed
		} else {
			var req PreviewRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "decoding request body: "+err.Error())
				return
			}
			if req.URL == "" {
				writeError(w, http.StatusBadRequest, `request must set "url" or upload a .torrent file`)
				return
			}
			data, err := fetchURLBytes(r.Context(), req.URL)
			if err != nil {
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
			parsed, err := metainfo.Parse(data)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid .torrent file: "+err.Error())
				return
			}
			mi = parsed
		}

		var files []PreviewFile
		if mi.Info.IsMultiFile() {
			files = make([]PreviewFile, len(mi.Info.Files))
			for i, f := range mi.Info.Files {
				files[i] = PreviewFile{Path: f.Path, Length: f.Length, Padding: f.IsPadding()}
			}
		} else {
			files = []PreviewFile{{Path: []string{mi.Info.Name}, Length: mi.Info.Length}}
		}

		writeJSON(w, http.StatusOK, PreviewResponse{
			InfoHash:    mi.InfoHash,
			Name:        mi.Info.Name,
			TotalLength: mi.TotalLength,
			PieceLength: mi.Info.PieceLength,
			Private:     mi.Info.Private,
			Files:       files,
		})
	}
}
