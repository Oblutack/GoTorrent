package tuiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RequestError is returned by every Client method for a non-2xx response —
// wrapping gottrentd's real {"error": "..."} message (internal/api's own
// errorBody) rather than a bare "409 Conflict", the same reasoning
// Desktop's own EngineRequestException doc comment gives for not just
// using EnsureSuccessStatusCode.
type RequestError struct {
	StatusCode int
	Message    string
}

func (e *RequestError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("gottrentd returned %d", e.StatusCode)
}

// Client is a typed HTTP client for gottrentd's control API
// (internal/api), plus Subscribe for its WebSocket event stream — the Go
// side of what Desktop's C# EngineClient already is. A plain *http.Client
// with a real timeout, not a resilience-wrapped one (same reasoning
// Desktop's own EngineClient doc comment gives: a local daemon on the
// same machine has much less need for retry/circuit-breaker machinery
// than a call across a real network).
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New builds a Client against baseURL (e.g. "http://127.0.0.1:6880") using
// token as the bearer credential on every request.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// eventsURL returns the ws://.../api/v1/events URL Subscribe dials —
// exported as its own method (rather than inlined in Subscribe) so
// internal/tui can log or display where it's connecting without
// duplicating the http->ws scheme rewrite.
func (c *Client) eventsURL() string {
	u := c.baseURL + "/api/v1/events"
	if strings.HasPrefix(u, "https://") {
		return "wss://" + strings.TrimPrefix(u, "https://")
	}
	return "ws://" + strings.TrimPrefix(u, "http://")
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var eb errorBody
		data, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(data, &eb)
		return &RequestError{StatusCode: resp.StatusCode, Message: eb.Error}
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	return c.do(ctx, method, path, reader, "application/json", out)
}

// ListTorrents calls GET /api/v1/torrents.
func (c *Client) ListTorrents(ctx context.Context) ([]TorrentSummary, error) {
	var out []TorrentSummary
	if err := c.do(ctx, http.MethodGet, "/api/v1/torrents", nil, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetDetail calls GET /api/v1/torrents/{hash}.
func (c *Client) GetDetail(ctx context.Context, hash string) (TorrentDetail, error) {
	var out TorrentDetail
	err := c.do(ctx, http.MethodGet, "/api/v1/torrents/"+hash, nil, "", &out)
	return out, err
}

// GetFiles calls GET /api/v1/torrents/{hash}/files.
func (c *Client) GetFiles(ctx context.Context, hash string) ([]FileEntry, error) {
	var out []FileEntry
	err := c.do(ctx, http.MethodGet, "/api/v1/torrents/"+hash+"/files", nil, "", &out)
	return out, err
}

// GetPeers calls GET /api/v1/torrents/{hash}/peers.
func (c *Client) GetPeers(ctx context.Context, hash string) ([]PeerEntry, error) {
	var out []PeerEntry
	err := c.do(ctx, http.MethodGet, "/api/v1/torrents/"+hash+"/peers", nil, "", &out)
	return out, err
}

// GetTrackers calls GET /api/v1/torrents/{hash}/trackers.
func (c *Client) GetTrackers(ctx context.Context, hash string) ([]TrackerEntry, error) {
	var out []TrackerEntry
	err := c.do(ctx, http.MethodGet, "/api/v1/torrents/"+hash+"/trackers", nil, "", &out)
	return out, err
}

// GetSession calls GET /api/v1/session.
func (c *Client) GetSession(ctx context.Context) (SessionStats, error) {
	var out SessionStats
	err := c.do(ctx, http.MethodGet, "/api/v1/session", nil, "", &out)
	return out, err
}

// AddMagnet calls POST /api/v1/torrents with a magnet URI.
func (c *Client) AddMagnet(ctx context.Context, magnet string) (AddResponse, error) {
	var out AddResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/torrents", AddRequest{Magnet: magnet}, &out)
	return out, err
}

// AddURL calls POST /api/v1/torrents with a .torrent URL (http/https only
// — see internal/api/add.go's own doc comment for why file:// is refused
// server-side; this client has no reason to special-case that here).
func (c *Client) AddURL(ctx context.Context, torrentURL string) (AddResponse, error) {
	var out AddResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/torrents", AddRequest{URL: torrentURL}, &out)
	return out, err
}

// AddTorrentFile calls POST /api/v1/torrents with a real local .torrent
// file, multipart/form-data field "torrent" — mirroring
// internal/api/add.go's own parseMultipartAdd exactly, the same shape
// Desktop's AddTorrentFileAsync already uses.
func (c *Client) AddTorrentFile(ctx context.Context, path string) (AddResponse, error) {
	var out AddResponse
	data, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("torrent", filepath.Base(path))
	if err != nil {
		return out, err
	}
	if _, err := part.Write(data); err != nil {
		return out, err
	}
	if err := mw.Close(); err != nil {
		return out, err
	}

	err = c.do(ctx, http.MethodPost, "/api/v1/torrents", &body, mw.FormDataContentType(), &out)
	return out, err
}

// Pause calls POST /api/v1/torrents/{hash}/pause.
func (c *Client) Pause(ctx context.Context, hash string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/torrents/"+hash+"/pause", nil, "", nil)
}

// Resume calls POST /api/v1/torrents/{hash}/resume.
func (c *Client) Resume(ctx context.Context, hash string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/torrents/"+hash+"/resume", nil, "", nil)
}

// Verify calls POST /api/v1/torrents/{hash}/verify.
func (c *Client) Verify(ctx context.Context, hash string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/torrents/"+hash+"/verify", nil, "", nil)
}

// Reannounce calls POST /api/v1/torrents/{hash}/reannounce.
func (c *Client) Reannounce(ctx context.Context, hash string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/torrents/"+hash+"/reannounce", nil, "", nil)
}

// Delete calls DELETE /api/v1/torrents/{hash}, honoring ?deleteData=true
// exactly like internal/api/actions.go's own DeleteTorrentHandler.
func (c *Client) Delete(ctx context.Context, hash string, deleteData bool) error {
	path := "/api/v1/torrents/" + hash
	if deleteData {
		path += "?deleteData=true"
	}
	return c.do(ctx, http.MethodDelete, path, nil, "", nil)
}
