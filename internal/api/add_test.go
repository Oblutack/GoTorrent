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
