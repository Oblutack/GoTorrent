package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func previewMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/torrents/preview", PreviewTorrentHandler())
	return mux
}

// TestPreviewTorrentHandlerAcceptsFileUpload proves a preview parses a
// real .torrent's file list without ever adding it - no *engine.Engine is
// even reachable from PreviewTorrentHandler, so there is structurally
// nothing for it to add to.
func TestPreviewTorrentHandlerAcceptsFileUpload(t *testing.T) {
	mux := previewMux()

	path, hash := writeTorrentFile(t, t.TempDir(), "previewed")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture .torrent: %v", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("torrent", "previewed.torrent")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write file field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/preview", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got PreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.InfoHash != hash {
		t.Fatalf("InfoHash = %s, want %s", got.InfoHash, hash)
	}
	if got.Name != "previewed" {
		t.Fatalf("Name = %q, want %q", got.Name, "previewed")
	}
	if len(got.Files) != 1 || got.Files[0].Path[0] != "previewed" {
		t.Fatalf("Files = %v, want a single entry named previewed", got.Files)
	}
	if got.Files[0].Length != got.TotalLength {
		t.Fatalf("single-file Length = %d, want it to equal TotalLength %d", got.Files[0].Length, got.TotalLength)
	}
}

// TestPreviewTorrentHandlerAcceptsURL mirrors AddTorrentHandler's own URL
// test - a preview fetches and parses exactly the same way a real Add
// would, just without ever calling engine.AddWithOptions.
func TestPreviewTorrentHandlerAcceptsURL(t *testing.T) {
	mux := previewMux()

	path, hash := writeTorrentFile(t, t.TempDir(), "previewedviaurl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture .torrent: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	body, err := json.Marshal(PreviewRequest{URL: srv.URL + "/previewedviaurl.torrent"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got PreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.InfoHash != hash {
		t.Fatalf("InfoHash = %s, want %s", got.InfoHash, hash)
	}
}

// TestPreviewTorrentHandlerRejectsNonHTTPURL mirrors AddTorrentHandler's
// own file:// rejection - the same reasoning applies identically here.
func TestPreviewTorrentHandlerRejectsNonHTTPURL(t *testing.T) {
	mux := previewMux()

	body, err := json.Marshal(PreviewRequest{URL: "file:///etc/passwd"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body=%s", rec.Code, rec.Body.String())
	}
}

// TestPreviewTorrentHandlerRejectsEmptyRequest mirrors AddTorrentHandler's
// own empty-request rejection.
func TestPreviewTorrentHandlerRejectsEmptyRequest(t *testing.T) {
	mux := previewMux()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/preview", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}
