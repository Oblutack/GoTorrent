package engine

import (
	"crypto/sha1"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

// writeTwoFileTorrentFile builds a real, genuinely two-file .torrent (dead
// announce URL, no peers needed — same reasoning as writeTorrentFile) so
// AddOptions.FilePriorities has more than one file to actually distinguish
// between. writeMultiFileTorrentFile (movedata_test.go) exercises the
// multi-file storage layout but only ever describes a single file within
// it, so it can't stand in here.
func writeTwoFileTorrentFile(t *testing.T, dir, name string) (path string, hash metainfo.Hash) {
	t.Helper()

	const pieceLength = 16384
	const perFile = pieceLength * 2
	content := make([]byte, perFile*2)
	rand.New(rand.NewSource(29)).Read(content)

	var hashes []byte
	for off := 0; off < len(content); off += pieceLength {
		end := off + pieceLength
		if end > len(content) {
			end = len(content)
		}
		sum := sha1.Sum(content[off:end])
		hashes = append(hashes, sum[:]...)
	}

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
	infoBytes, err := bencode.Marshal(infoWire{
		Files: []fileWire{
			{Length: perFile, Path: []string{"a.bin"}},
			{Length: perFile, Path: []string{"b.bin"}},
		},
		Name:        name,
		PieceLength: pieceLength,
		Pieces:      hashes,
	})
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

// TestAddWithOptionsFilePrioritiesReachesTheManagedTorrent proves
// AddOptions.FilePriorities (Stage 5) reaches a real managed torrent's
// Config at construction, through the engine's own public AddWithOptions,
// not just torrent.Config directly.
func TestAddWithOptionsFilePrioritiesReachesTheManagedTorrent(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, hash := writeTwoFileTorrentFile(t, torrentDir, "addfilepriorities")

	opts := AddOptions{FilePriorities: []picker.Priority{picker.PrioritySkip, picker.PriorityHigh}}
	if _, err := e.AddWithOptions(path, "", opts); err != nil {
		t.Fatalf("AddWithOptions: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing right after Add")
	}

	got := tr.Stats().FilePriorities
	want := []picker.Priority{picker.PrioritySkip, picker.PriorityHigh}
	if len(got) != len(want) {
		t.Fatalf("FilePriorities = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FilePriorities = %v, want %v", got, want)
		}
	}
}

// TestAddWithOptionsNoFilePrioritiesDefaultsToNormal proves the absent
// (nil) case — an Add with no FilePriorities set at all must not leave
// every file at some other default, matching Config.FilePriorities' own
// "nil means every file is Normal" contract.
func TestAddWithOptionsNoFilePrioritiesDefaultsToNormal(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, hash := writeTwoFileTorrentFile(t, torrentDir, "addnofilepriorities")

	if _, err := e.AddWithOptions(path, "", AddOptions{}); err != nil {
		t.Fatalf("AddWithOptions: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing right after Add")
	}

	for i, p := range tr.Stats().FilePriorities {
		if p != picker.PriorityNormal {
			t.Fatalf("file %d priority = %v, want Normal (no FilePriorities given)", i, p)
		}
	}
}
