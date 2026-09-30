package tuiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newTestServer builds a real httptest.Server so every test below drives
// the client against real HTTP round trips (real headers, real status
// codes, real JSON encoding) rather than a hand-rolled RoundTripper -
// the same "real fixtures over mocks" discipline every Go package in this
// project already follows, mirrored here for its first Go-side client.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(srv.URL, "test-token"), srv
}

func TestListTorrentsDecodesRealJSON(t *testing.T) {
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q, want Bearer test-token", got)
		}
		if r.URL.Path != "/api/v1/torrents" || r.Method != http.MethodGet {
			t.Errorf("request = %s %s, want GET /api/v1/torrents", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// Hand-written JSON matching internal/api's real TorrentSummary
		// shape, the same regression guard Desktop's own EngineClientTests
		// use against the two drifting apart.
		w.Write([]byte(`[{"infoHash":"abc123","name":"a torrent","state":"Downloading","downloaded":100,"totalLength":200,"left":100,"peerCount":3,"seedRatio":0.5}]`))
	})

	got, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatalf("ListTorrents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	s := got[0]
	if s.InfoHash != "abc123" || s.Name != "a torrent" || s.State != "Downloading" ||
		s.Downloaded != 100 || s.TotalLength != 200 || s.Left != 100 ||
		s.PeerCount != 3 || s.SeedRatio != 0.5 {
		t.Fatalf("got %+v", s)
	}
	if p := got[0].Progress(); p != 0.5 {
		t.Fatalf("Progress() = %v, want 0.5", p)
	}
}

func TestGetPiecesDecodesABase64BitfieldAndHasReadsItMSBFirst(t *testing.T) {
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/torrents/abc/pieces" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// 0b10100000, 0b01000000 -> pieces 0, 2 and 9 are have. base64 of {0xA0, 0x40}.
		w.Write([]byte(`{"numPieces":10,"haveCount":3,"bitfield":"oEA="}`))
	})

	got, err := c.GetPieces(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetPieces: %v", err)
	}
	if got.NumPieces != 10 || got.HaveCount != 3 {
		t.Fatalf("got %+v", got)
	}
	for i, want := range map[int]bool{0: true, 1: false, 2: true, 8: false, 9: true, 10: false, -1: false} {
		if got.Has(i) != want {
			t.Errorf("Has(%d) = %v, want %v", i, got.Has(i), want)
		}
	}
}

func TestRequestErrorCarriesTheRealServerMessage(t *testing.T) {
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(errorBody{Error: "already added"})
	})

	_, err := c.AddMagnet(context.Background(), "magnet:?xt=urn:btih:abc")
	if err == nil {
		t.Fatal("AddMagnet succeeded, want an error")
	}
	reqErr, ok := err.(*RequestError)
	if !ok {
		t.Fatalf("error type = %T, want *RequestError", err)
	}
	if reqErr.StatusCode != http.StatusConflict || reqErr.Message != "already added" {
		t.Fatalf("got %+v, want StatusCode=409 Message=%q", reqErr, "already added")
	}
	if reqErr.Error() != "already added" {
		t.Fatalf("Error() = %q, want the real server message, not a generic one", reqErr.Error())
	}
}

func TestGetDetailDecodesEmbeddedSummary(t *testing.T) {
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/torrents/abc123" {
			t.Errorf("path = %s, want /api/v1/torrents/abc123", r.URL.Path)
		}
		w.Write([]byte(`{"infoHash":"abc123","name":"a torrent","state":"Seeding","downloadDir":"/downloads","source":"/x.torrent"}`))
	})

	got, err := c.GetDetail(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("GetDetail: %v", err)
	}
	if got.Name != "a torrent" || got.State != "Seeding" || got.DownloadDir != "/downloads" {
		t.Fatalf("got %+v", got)
	}
}

func TestPauseSendsTheRealRoute(t *testing.T) {
	var gotMethod, gotPath string
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.Pause(context.Background(), "abc123"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/torrents/abc123/pause" {
		t.Fatalf("request = %s %s, want POST /api/v1/torrents/abc123/pause", gotMethod, gotPath)
	}
}

func TestDeleteHonorsDeleteDataQueryParam(t *testing.T) {
	var gotQuery string
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.Delete(context.Background(), "abc123", true); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if gotQuery != "deleteData=true" {
		t.Fatalf("query = %q, want deleteData=true", gotQuery)
	}
}

func TestAddTorrentFileUploadsRealMultipartContent(t *testing.T) {
	var gotFieldName, gotFilename string
	var gotContent []byte
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("server ParseMultipartForm: %v", err)
		}
		file, header, err := r.FormFile("torrent")
		if err != nil {
			t.Fatalf("server FormFile: %v", err)
		}
		defer file.Close()
		gotFieldName = "torrent"
		gotFilename = header.Filename
		gotContent = make([]byte, header.Size)
		if _, err := file.Read(gotContent); err != nil && err.Error() != "EOF" {
			t.Fatalf("reading uploaded content: %v", err)
		}
		_ = json.NewEncoder(w).Encode(AddResponse{InfoHash: "abc123"})
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "real.torrent")
	if err := os.WriteFile(path, []byte("d8:announce...e"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := c.AddTorrentFile(context.Background(), path)
	if err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}
	if got.InfoHash != "abc123" {
		t.Fatalf("InfoHash = %q, want abc123", got.InfoHash)
	}
	if gotFieldName != "torrent" || gotFilename != "real.torrent" {
		t.Fatalf("field=%q filename=%q, want torrent/real.torrent", gotFieldName, gotFilename)
	}
	if string(gotContent) != "d8:announce...e" {
		t.Fatalf("uploaded content = %q, want the real file's own bytes", gotContent)
	}
}
