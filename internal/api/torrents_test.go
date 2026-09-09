package api

import (
	"crypto/sha1"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

func TestMain(m *testing.M) {
	logger.Init(false)
	os.Exit(m.Run())
}

// newTestEngine and writeTorrentFile mirror internal/engine's own
// unexported test fixtures of the same name (not reusable across packages)
// closely enough for this package's purposes: real add/list/detail
// behavior against a dead-tracker torrent that never needs a real
// transfer, since these tests exercise the HTTP layer over Engine, not
// Engine's own transfer logic.
func newTestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.New(t.TempDir(), engine.Defaults{
		DownloadDir: t.TempDir(),
		ResumeDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(e.Shutdown)
	return e
}

func writeTorrentFile(t *testing.T, dir, name string) (path string, hash metainfo.Hash) {
	t.Helper()

	const pieceLength = 16384
	const total = pieceLength*2 + 100
	content := make([]byte, total)
	rand.New(rand.NewSource(7)).Read(content)

	var hashes []byte
	for off := 0; off < total; off += pieceLength {
		end := off + pieceLength
		if end > total {
			end = total
		}
		sum := sha1.Sum(content[off:end])
		hashes = append(hashes, sum[:]...)
	}

	type infoWire struct {
		Length      int64  `bencode:"length"`
		Name        string `bencode:"name"`
		PieceLength int64  `bencode:"piece length"`
		Pieces      []byte `bencode:"pieces"`
	}
	infoBytes, err := bencode.Marshal(infoWire{Length: total, Name: name, PieceLength: pieceLength, Pieces: hashes})
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	torrentBytes, err := bencode.Marshal(struct {
		Announce string             `bencode:"announce"`
		Info     bencode.RawMessage `bencode:"info"`
	}{Announce: "http://127.0.0.1:1/announce", Info: infoBytes})
	if err != nil {
		t.Fatalf("marshal torrent: %v", err)
	}

	mi, err := metainfo.Parse(torrentBytes)
	if err != nil {
		t.Fatalf("parse torrent: %v", err)
	}

	path = filepath.Join(dir, name+".torrent")
	if err := os.WriteFile(path, torrentBytes, 0o644); err != nil {
		t.Fatalf("write torrent file: %v", err)
	}
	return path, mi.InfoHash
}

func addTestTorrent(t *testing.T, e *engine.Engine, name string) metainfo.Hash {
	t.Helper()
	path, hash := writeTorrentFile(t, t.TempDir(), name)
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get did not find the just-added torrent")
	}
	deadline := time.Now().Add(5 * time.Second)
	for tr.State() != torrent.StateDownloading {
		if time.Now().After(deadline) {
			t.Fatalf("torrent stuck at %s, want Downloading", tr.State())
		}
		time.Sleep(5 * time.Millisecond)
	}
	return hash
}

func doRequest(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestListTorrentsHandlerReturnsEveryManagedTorrent(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "one")

	rec := doRequest(t, ListTorrentsHandler(e), http.MethodGet, "/api/v1/torrents")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got []TorrentSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].InfoHash != hash {
		t.Fatalf("InfoHash = %s, want %s", got[0].InfoHash, hash)
	}
	if got[0].Name != "one" {
		t.Fatalf("Name = %q, want %q", got[0].Name, "one")
	}
	if got[0].State != torrent.StateDownloading {
		t.Fatalf("State = %v, want Downloading", got[0].State)
	}
}

func TestListTorrentsHandlerEmptyFleetReturnsEmptyArray(t *testing.T) {
	e := newTestEngine(t)
	rec := doRequest(t, ListTorrentsHandler(e), http.MethodGet, "/api/v1/torrents")

	if got := rec.Body.String(); got != "[]\n" {
		t.Fatalf("body = %q, want an empty JSON array, not null", got)
	}
}

func TestTorrentDetailHandlerReturnsMatchingTorrent(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "detailed")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/torrents/{hash}", TorrentDetailHandler(e))

	rec := doRequest(t, mux, http.MethodGet, "/api/v1/torrents/"+hash.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var got TorrentDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.InfoHash != hash {
		t.Fatalf("InfoHash = %s, want %s", got.InfoHash, hash)
	}
	if got.ContentPath == "" {
		t.Fatal("ContentPath was empty for a torrent with known metadata")
	}
}

func TestTorrentDetailHandlerUnknownHashReturns404(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/torrents/{hash}", TorrentDetailHandler(e))

	unmanaged := "0000000000000000000000000000000000000000"
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/torrents/"+unmanaged)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestTorrentDetailHandlerMalformedHashReturns400(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/torrents/{hash}", TorrentDetailHandler(e))

	rec := doRequest(t, mux, http.MethodGet, "/api/v1/torrents/not-a-hash")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestFilesHandlerReturnsSingleFileEntry(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "single")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/torrents/{hash}/files", FilesHandler(e))

	rec := doRequest(t, mux, http.MethodGet, "/api/v1/torrents/"+hash.String()+"/files")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var files []FileEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &files); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1 for a single-file torrent", len(files))
	}
	if files[0].Path[0] != "single" {
		t.Fatalf("Path = %v, want [single]", files[0].Path)
	}
	if files[0].Priority.String() != "normal" {
		t.Fatalf("Priority = %v, want normal (default)", files[0].Priority)
	}
}
