package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// AddRequest is POST /api/v1/torrents's JSON body shape - used when the
// request is not a multipart upload (see AddTorrentHandler). Exactly one
// of Magnet or URL should be set.
type AddRequest struct {
	Magnet      string   `json:"magnet,omitempty"`
	URL         string   `json:"url,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	DownloadDir string   `json:"downloadDir,omitempty"`
}

// AddResponse is POST /api/v1/torrents's success body.
type AddResponse struct {
	InfoHash metainfo.Hash `json:"infoHash"`
}

// fetchTimeout bounds how long AddTorrentHandler waits on a "url" add
// fetching the .torrent file itself - independent of the request's own
// context, since a slow or hanging remote server shouldn't be able to tie
// up this handler indefinitely just because the client that asked for it
// is still connected.
const fetchTimeout = 30 * time.Second

// AddTorrentHandler serves POST /api/v1/torrents - ROADMAP.md's ".torrent
// upload | magnet | URL" in one route, dispatched by Content-Type:
// multipart/form-data (a real file upload, field name "torrent", with
// "category"/"tags"/"downloadDir" as ordinary form fields) or anything
// else, decoded as AddRequest JSON. uploadDir is where an uploaded or
// URL-fetched .torrent file is written before engine.AddWithOptions (which
// only accepts a source path or magnet URI, never raw bytes) ever sees it
// — see saveTorrentBytes for why that file has to persist past this
// request, not just live in a temp directory.
func AddTorrentHandler(e *engine.Engine, uploadDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var source string
		var opts engine.AddOptions
		var downloadDir string

		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			s, o, d, ok := parseMultipartAdd(w, r, uploadDir)
			if !ok {
				return
			}
			source, opts, downloadDir = s, o, d
		} else {
			s, o, d, ok := parseJSONAdd(w, r, uploadDir)
			if !ok {
				return
			}
			source, opts, downloadDir = s, o, d
		}

		hash, err := e.AddWithOptions(source, downloadDir, opts)
		if err != nil {
			if errors.Is(err, engine.ErrAlreadyAdded) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, AddResponse{InfoHash: hash})
	}
}

// parseMultipartAdd handles the file-upload half of AddTorrentHandler,
// writing the appropriate error response and returning ok=false itself on
// any failure.
func parseMultipartAdd(w http.ResponseWriter, r *http.Request, uploadDir string) (source string, opts engine.AddOptions, downloadDir string, ok bool) {
	if err := r.ParseMultipartForm(metainfo.MaxTorrentFileSize); err != nil {
		writeError(w, http.StatusBadRequest, "parsing multipart form: "+err.Error())
		return "", opts, "", false
	}
	file, _, err := r.FormFile("torrent")
	if err != nil {
		writeError(w, http.StatusBadRequest, `missing "torrent" file field`)
		return "", opts, "", false
	}
	defer file.Close()

	path, err := saveTorrentBytes(uploadDir, io.LimitReader(file, metainfo.MaxTorrentFileSize+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", opts, "", false
	}

	opts.Category = r.FormValue("category")
	if tags := r.FormValue("tags"); tags != "" {
		opts.Tags = strings.Split(tags, ",")
	}
	return path, opts, r.FormValue("downloadDir"), true
}

// parseJSONAdd handles the magnet/URL half of AddTorrentHandler.
func parseJSONAdd(w http.ResponseWriter, r *http.Request, uploadDir string) (source string, opts engine.AddOptions, downloadDir string, ok bool) {
	var req AddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decoding request body: "+err.Error())
		return "", opts, "", false
	}
	opts.Category = req.Category
	opts.Tags = req.Tags

	switch {
	case req.Magnet != "":
		return req.Magnet, opts, req.DownloadDir, true
	case req.URL != "":
		path, err := fetchTorrentFile(r.Context(), uploadDir, req.URL)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return "", opts, "", false
		}
		return path, opts, req.DownloadDir, true
	default:
		writeError(w, http.StatusBadRequest, `request must set "magnet", "url", or upload a .torrent file`)
		return "", opts, "", false
	}
}

// saveTorrentBytes validates r as a real .torrent file and writes it to
// uploadDir, named by its own infohash - both so re-adding the same file
// twice overwrites rather than accumulating garbage (engine.AddWithOptions
// already rejects the resulting duplicate-infohash Add on its own), and
// because engine.AddWithOptions only ever takes a source *path*, never raw
// bytes, and — critically — keeps that same path as Summary.Source, which
// Load() re-reads from disk on every future process restart. A temp file
// that gets cleaned up after this request would silently break every
// uploaded or URL-fetched torrent's ability to survive a restart.
func saveTorrentBytes(uploadDir string, r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("reading .torrent data: %w", err)
	}
	if len(data) > metainfo.MaxTorrentFileSize {
		return "", fmt.Errorf(".torrent file exceeds the %d byte limit", metainfo.MaxTorrentFileSize)
	}
	mi, err := metainfo.Parse(data)
	if err != nil {
		return "", fmt.Errorf("invalid .torrent file: %w", err)
	}

	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return "", fmt.Errorf("creating upload directory: %w", err)
	}
	path := filepath.Join(uploadDir, mi.InfoHash.String()+".torrent")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("writing .torrent file: %w", err)
	}
	return path, nil
}

// fetchTorrentFile downloads url and hands its body to saveTorrentBytes.
// Only http/https is accepted - there is no legitimate use for gottrentd
// fetching a "url" add via any other scheme (file://, etc. would let a
// caller with API access read arbitrary local files back out through the
// resulting Add error messages).
func fetchTorrentFile(ctx context.Context, uploadDir, url string) (string, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("url must be http:// or https://")
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("building request for %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching %s: unexpected status %s", url, resp.Status)
	}
	return saveTorrentBytes(uploadDir, io.LimitReader(resp.Body, metainfo.MaxTorrentFileSize+1))
}
