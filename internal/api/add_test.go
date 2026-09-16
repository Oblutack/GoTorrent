package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

func addTorrentMux(e *engine.Engine, uploadDir string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/torrents", AddTorrentHandler(e, uploadDir))
	return mux
}

func TestAddTorrentHandlerAcceptsMagnet(t *testing.T) {
	e := newTestEngine(t)
	mux := addTorrentMux(e, t.TempDir())

	_, hash := writeTorrentFile(t, t.TempDir(), "magnetadd")
	body, err := json.Marshal(AddRequest{Magnet: "magnet:?xt=urn:btih:" + hash.String() + "&dn=magnetadd"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var got AddResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.InfoHash != hash {
		t.Fatalf("InfoHash = %s, want %s", got.InfoHash, hash)
	}
	if _, ok := e.Get(hash); !ok {
		t.Fatal("torrent was not actually added to the engine")
	}
}

func TestAddTorrentHandlerAcceptsFileUpload(t *testing.T) {
	e := newTestEngine(t)
	uploadDir := t.TempDir()
	mux := addTorrentMux(e, uploadDir)

	path, hash := writeTorrentFile(t, t.TempDir(), "uploaded")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture .torrent: %v", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("torrent", "uploaded.torrent")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write file field: %v", err)
	}
	if err := mw.WriteField("category", "movies"); err != nil {
		t.Fatalf("write category field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var got AddResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.InfoHash != hash {
		t.Fatalf("InfoHash = %s, want %s", got.InfoHash, hash)
	}

	summary, ok := e.GetSummary(hash)
	if !ok {
		t.Fatal("torrent was not actually added to the engine")
	}
	if summary.Category != "movies" {
		t.Fatalf("Category = %q, want movies", summary.Category)
	}

	// The uploaded bytes must have been persisted under uploadDir, named by
	// infohash - not left in a temp file that vanishes after the request
	// (see saveTorrentBytes's own doc comment for why that matters: Load()
	// re-reads this exact path on every future restart).
	if _, err := os.Stat(filepath.Join(uploadDir, hash.String()+".torrent")); err != nil {
		t.Fatalf("uploaded .torrent was not persisted under uploadDir: %v", err)
	}
}

func TestAddTorrentHandlerAcceptsURL(t *testing.T) {
	e := newTestEngine(t)
	mux := addTorrentMux(e, t.TempDir())

	path, hash := writeTorrentFile(t, t.TempDir(), "viaurl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture .torrent: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	body, err := json.Marshal(AddRequest{URL: srv.URL + "/viaurl.torrent"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var got AddResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.InfoHash != hash {
		t.Fatalf("InfoHash = %s, want %s", got.InfoHash, hash)
	}
}

func TestAddTorrentHandlerRejectsEmptyRequest(t *testing.T) {
	e := newTestEngine(t)
	mux := addTorrentMux(e, t.TempDir())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a request with neither magnet, url, nor upload", rec.Code)
	}
}

func TestAddTorrentHandlerRejectsNonHTTPURL(t *testing.T) {
	e := newTestEngine(t)
	mux := addTorrentMux(e, t.TempDir())

	body, _ := json.Marshal(AddRequest{URL: "file:///etc/passwd"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 for a non-http(s) url", rec.Code)
	}
}

func TestAddTorrentHandlerDuplicateReturns409(t *testing.T) {
	e := newTestEngine(t)
	mux := addTorrentMux(e, t.TempDir())

	_, hash := writeTorrentFile(t, t.TempDir(), "dupe")
	body, _ := json.Marshal(AddRequest{Magnet: "magnet:?xt=urn:btih:" + hash.String()})

	for i, wantCode := range []int{http.StatusCreated, http.StatusConflict} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != wantCode {
			t.Fatalf("attempt %d: status = %d, want %d, body=%s", i, rec.Code, wantCode, rec.Body.String())
		}
	}
}

// TestAddTorrentHandlerJSONFilePrioritiesReachesTheEngine proves
// AddRequest.FilePriorities (Stage 5) reaches a real managed torrent
// through the JSON add path - picker.Priority round-trips through JSON as
// its own name via MarshalText/UnmarshalText, so the wire body just uses
// plain strings. Uses the "url" add path (real metadata known
// immediately), not a magnet: Stats().FilePriorities only reflects real
// values once openMetadata has run, which a magnet with no real peers
// never reaches.
func TestAddTorrentHandlerJSONFilePrioritiesReachesTheEngine(t *testing.T) {
	e := newTestEngine(t)
	mux := addTorrentMux(e, t.TempDir())

	path, hash := writeTorrentFile(t, t.TempDir(), "jsonfilepriorities")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture .torrent: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	body, _ := json.Marshal(AddRequest{
		URL:            srv.URL + "/jsonfilepriorities.torrent",
		FilePriorities: []picker.Priority{picker.PriorityHigh},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}

	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("torrent was not actually added to the engine")
	}
	got := tr.Stats().FilePriorities
	if len(got) != 1 || got[0] != picker.PriorityHigh {
		t.Fatalf("FilePriorities = %v, want [high]", got)
	}
}

// TestAddTorrentHandlerMultipartFilePrioritiesReachesTheEngine proves the
// same field also works on the file-upload path, as a comma-separated
// form field the same shape "tags" already uses.
func TestAddTorrentHandlerMultipartFilePrioritiesReachesTheEngine(t *testing.T) {
	e := newTestEngine(t)
	uploadDir := t.TempDir()
	mux := addTorrentMux(e, uploadDir)

	path, hash := writeTorrentFile(t, t.TempDir(), "multipartfilepriorities")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture .torrent: %v", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("torrent", "multipartfilepriorities.torrent")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write file field: %v", err)
	}
	if err := mw.WriteField("filePriorities", "skip"); err != nil {
		t.Fatalf("write filePriorities field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}

	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("torrent was not actually added to the engine")
	}
	got := tr.Stats().FilePriorities
	if len(got) != 1 || got[0] != picker.PrioritySkip {
		t.Fatalf("FilePriorities = %v, want [skip]", got)
	}
}

// TestAddTorrentHandlerMultipartFilePrioritiesRejectsInvalidName proves an
// unrecognized priority name in the form field is a clean 400, not a
// silently-ignored value or a panic.
func TestAddTorrentHandlerMultipartFilePrioritiesRejectsInvalidName(t *testing.T) {
	e := newTestEngine(t)
	uploadDir := t.TempDir()
	mux := addTorrentMux(e, uploadDir)

	path, _ := writeTorrentFile(t, t.TempDir(), "badpriorityname")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture .torrent: %v", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("torrent", "badpriorityname.torrent")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write file field: %v", err)
	}
	if err := mw.WriteField("filePriorities", "urgent"); err != nil {
		t.Fatalf("write filePriorities field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unrecognized priority name, body=%s", rec.Code, rec.Body.String())
	}
}
