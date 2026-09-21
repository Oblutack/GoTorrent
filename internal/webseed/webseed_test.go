package webseed

import (
	"bytes"
	"context"
	"crypto/sha1"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

func buildSingleFileTorrent(t *testing.T, name string, pieceLength int64, total int64) (*metainfo.MetaInfo, []byte) {
	t.Helper()
	content := make([]byte, total)
	rand.New(rand.NewSource(41)).Read(content)

	var hashes []byte
	for off := int64(0); off < total; off += pieceLength {
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
		Info bencode.RawMessage `bencode:"info"`
	}{Info: infoBytes})
	if err != nil {
		t.Fatalf("marshal torrent: %v", err)
	}
	mi, err := metainfo.Parse(torrentBytes)
	if err != nil {
		t.Fatalf("parse torrent: %v", err)
	}
	return mi, content
}

func buildMultiFileTorrent(t *testing.T) *metainfo.MetaInfo {
	t.Helper()
	type fileWire struct {
		Length int64    `bencode:"length"`
		Path   []string `bencode:"path"`
	}
	type infoWire struct {
		Files       []fileWire `bencode:"files"`
		Name        string     `bencode:"name"`
		PieceLength int64      `bencode:"piece length"`
		Pieces      []byte     `bencode:"pieces"`
	}
	sum := sha1.Sum(make([]byte, 16384))
	infoBytes, err := bencode.Marshal(infoWire{
		Files:       []fileWire{{Length: 16384, Path: []string{"a.bin"}}},
		Name:        "multi",
		PieceLength: 16384,
		Pieces:      sum[:],
	})
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	torrentBytes, err := bencode.Marshal(struct {
		Info bencode.RawMessage `bencode:"info"`
	}{Info: infoBytes})
	if err != nil {
		t.Fatalf("marshal torrent: %v", err)
	}
	mi, err := metainfo.Parse(torrentBytes)
	if err != nil {
		t.Fatalf("parse torrent: %v", err)
	}
	return mi
}

// TestFetchPieceReturnsCorrectBytesForEveryPiece proves the Range-request
// math is right at both ends: the first piece, a middle piece, and the
// final short piece (total isn't a multiple of pieceLength) all come back
// byte-exact, against a real HTTP server (net/http.ServeContent — the same
// real Range-serving machinery a real web seed host uses).
func TestFetchPieceReturnsCorrectBytesForEveryPiece(t *testing.T) {
	const pieceLength = 16384
	const total = pieceLength*3 + 777
	mi, content := buildSingleFileTorrent(t, "movie.bin", pieceLength, total)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "movie.bin", time.Time{}, bytes.NewReader(content))
	}))
	defer srv.Close()

	c := New(srv.URL, nil)
	for _, index := range []int{0, 1, 3} {
		got, err := c.FetchPiece(context.Background(), mi, index)
		if err != nil {
			t.Fatalf("FetchPiece(%d): %v", index, err)
		}
		start := int64(index) * pieceLength
		want := content[start : start+mi.PieceLen(index)]
		if string(got) != string(want) {
			t.Errorf("piece %d: got %d bytes not matching the source content (want %d bytes)", index, len(got), len(want))
		}
	}
}

// TestFetchPieceRejectsMultiFileTorrents proves the deliberate v1 scope
// boundary is enforced, not just documented.
func TestFetchPieceRejectsMultiFileTorrents(t *testing.T) {
	mi := buildMultiFileTorrent(t)
	c := New("http://example.invalid/", nil)
	_, err := c.FetchPiece(context.Background(), mi, 0)
	if err != ErrMultiFileNotSupported {
		t.Fatalf("FetchPiece on a multi-file torrent = %v, want ErrMultiFileNotSupported", err)
	}
}

// TestFetchPieceRejectsAServerThatIgnoresRange proves a 200 OK (a server
// that doesn't honor the Range header, which would otherwise silently hand
// back the wrong bytes for every piece but the first) is treated as a hard
// error rather than accepted.
func TestFetchPieceRejectsAServerThatIgnoresRange(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildSingleFileTorrent(t, "ignoresrange.bin", pieceLength, pieceLength*2)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	}))
	defer srv.Close()

	c := New(srv.URL, nil)
	if _, err := c.FetchPiece(context.Background(), mi, 1); err == nil {
		t.Fatal("FetchPiece against a Range-ignoring server returned nil error, want a failure")
	}
}
