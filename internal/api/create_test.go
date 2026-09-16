package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

func createTorrentMux(e *engine.Engine, uploadDir string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/torrents/create", CreateTorrentHandler(e, uploadDir))
	return mux
}

// TestCreateTorrentHandlerBuildsFromASingleFile proves the route really
// wraps metainfo.Build/CollectFiles against a real file on disk - the
// returned TorrentFile bytes parse back to the same infohash the response
// itself reports, and nothing is added to the engine (StartSeeding was
// never set).
func TestCreateTorrentHandlerBuildsFromASingleFile(t *testing.T) {
	e := newTestEngine(t)
	mux := createTorrentMux(e, t.TempDir())

	dir := t.TempDir()
	content := bytes.Repeat([]byte{0x42}, 40000)
	sourcePath := filepath.Join(dir, "solo.bin")
	if err := os.WriteFile(sourcePath, content, 0o644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	body, _ := json.Marshal(CreateTorrentRequest{SourcePath: sourcePath, PieceLength: 16384})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var got CreateTorrentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "solo.bin" {
		t.Fatalf("Name = %q, want %q", got.Name, "solo.bin")
	}
	if got.TotalLength != int64(len(content)) {
		t.Fatalf("TotalLength = %d, want %d", got.TotalLength, len(content))
	}

	mi, err := metainfo.Parse(got.TorrentFile)
	if err != nil {
		t.Fatalf("TorrentFile did not parse back as a real .torrent: %v", err)
	}
	if mi.InfoHash != got.InfoHash {
		t.Fatalf("parsed TorrentFile's InfoHash = %s, want %s (the response's own reported hash)", mi.InfoHash, got.InfoHash)
	}

	if _, ok := e.Get(got.InfoHash); ok {
		t.Fatal("torrent was added to the engine despite StartSeeding never being set")
	}
}

// TestCreateTorrentHandlerBuildsFromADirectory proves the multi-file path
// (CollectFiles walking a real directory) also works over the API.
func TestCreateTorrentHandlerBuildsFromADirectory(t *testing.T) {
	e := newTestEngine(t)
	mux := createTorrentMux(e, t.TempDir())

	sourceDir := t.TempDir() + string(filepath.Separator) + "myfiles"
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "a.bin"), bytes.Repeat([]byte{0x01}, 20000), 0o644); err != nil {
		t.Fatalf("write a.bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "b.bin"), bytes.Repeat([]byte{0x02}, 20000), 0o644); err != nil {
		t.Fatalf("write b.bin: %v", err)
	}

	body, _ := json.Marshal(CreateTorrentRequest{SourcePath: sourceDir, PieceLength: 16384})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var got CreateTorrentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "myfiles" {
		t.Fatalf("Name = %q, want %q (the directory's own base name)", got.Name, "myfiles")
	}
	if got.TotalLength != 40000 {
		t.Fatalf("TotalLength = %d, want 40000 (two 20000-byte files)", got.TotalLength)
	}
}

// TestCreateTorrentHandlerStartSeedingAddsToEngine proves the
// create-and-seed convenience path: the built torrent is added straight
// from SourcePath in place, and — since the real content is already
// exactly where the torrent describes it — verifies immediately to
// Seeding with no download at all.
func TestCreateTorrentHandlerStartSeedingAddsToEngine(t *testing.T) {
	e := newTestEngine(t)
	mux := createTorrentMux(e, t.TempDir())

	dir := t.TempDir()
	content := bytes.Repeat([]byte{0x77}, 40000)
	sourcePath := filepath.Join(dir, "seedme.bin")
	if err := os.WriteFile(sourcePath, content, 0o644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	body, _ := json.Marshal(CreateTorrentRequest{
		SourcePath:   sourcePath,
		PieceLength:  16384,
		StartSeeding: true,
		Category:     "created",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var got CreateTorrentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	tr, ok := e.Get(got.InfoHash)
	if !ok {
		t.Fatal("StartSeeding was set but the torrent was never added to the engine")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && tr.State() != torrent.StateSeeding {
		time.Sleep(10 * time.Millisecond)
	}
	if tr.State() != torrent.StateSeeding {
		t.Fatalf("state = %s, want Seeding (real content already in place)", tr.State())
	}

	summary, ok := e.GetSummary(got.InfoHash)
	if !ok || summary.Category != "created" {
		t.Fatalf("Category = %q, want %q", summary.Category, "created")
	}
}

// TestCreateTorrentHandlerRejectsMissingSourcePath proves the required
// field is actually enforced.
func TestCreateTorrentHandlerRejectsMissingSourcePath(t *testing.T) {
	e := newTestEngine(t)
	mux := createTorrentMux(e, t.TempDir())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/create", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateTorrentHandlerRejectsNonexistentSourcePath proves a bad path
// is a clean 400, not a 500 or a panic.
func TestCreateTorrentHandlerRejectsNonexistentSourcePath(t *testing.T) {
	e := newTestEngine(t)
	mux := createTorrentMux(e, t.TempDir())

	body, _ := json.Marshal(CreateTorrentRequest{SourcePath: filepath.Join(t.TempDir(), "does-not-exist")})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}
