package engine

import (
	"crypto/sha1"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/storage"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// writeSingleFileTorrentWithContent mirrors writeTorrentFile but also
// returns the exact bytes the torrent describes, so a test can pre-seed a
// real, verifiable file on disk — writeTorrentFile alone doesn't expose
// this, since none of its existing callers need real data (they only test
// add/list/remove/persist against a dead tracker).
func writeSingleFileTorrentWithContent(t *testing.T, dir, name string) (path string, hash metainfo.Hash, content []byte) {
	t.Helper()

	const pieceLength = 16384
	const total = pieceLength*2 + 100
	content = make([]byte, total)
	rand.New(rand.NewSource(13)).Read(content)

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
	return path, mi.InfoHash, content
}

// writeMultiFileTorrentFile builds a minimal two-file .torrent (no real
// data needed by its callers — see TestMoveDataRefusesNoSubfolderMultiFileTorrent,
// which fails before ever touching disk content).
func writeMultiFileTorrentFile(t *testing.T, dir, name string) (path string, hash metainfo.Hash) {
	t.Helper()

	const pieceLength = 16384
	total := int64(pieceLength * 2)
	content := make([]byte, total)
	rand.New(rand.NewSource(17)).Read(content)

	var hashes []byte
	for off := int64(0); off < total; off += pieceLength {
		end := off + pieceLength
		if end > total {
			end = total
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
		Files:       []fileWire{{Length: total, Path: []string{"a.bin"}}},
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

// TestMoveDataRelocatesFilesAndResumesWithoutRedownloading proves the whole
// feature end to end: real content, a real move, and the restarted torrent
// picking up the moved data via trusted resume rather than re-verifying
// from scratch or (worse) re-downloading it.
func TestMoveDataRelocatesFilesAndResumesWithoutRedownloading(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()
	torrentDir := t.TempDir()

	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "movable")
	if err := os.WriteFile(filepath.Join(oldDir, "movable"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}

	e, err := New(t.TempDir(), Defaults{DownloadDir: oldDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing right after Add")
	}
	waitForState(t, tr, torrent.StateSeeding, 5*time.Second)

	if err := e.MoveData(hash, newDir); err != nil {
		t.Fatalf("MoveData: %v", err)
	}

	if _, err := os.Stat(filepath.Join(oldDir, "movable")); !os.IsNotExist(err) {
		t.Fatalf("old content still exists at %s (stat err = %v)", oldDir, err)
	}
	got, err := os.ReadFile(filepath.Join(newDir, "movable"))
	if err != nil {
		t.Fatalf("reading moved content: %v", err)
	}
	if string(got) != string(content) {
		t.Fatal("moved file's content does not match the original")
	}

	// The restarted Torrent (a new *torrent.Torrent under the hood, per
	// MoveData's own doc comment) must reach Seeding again quickly, via
	// trusted resume data rather than a full re-verify or a real
	// re-download racing a dead tracker.
	tr2, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing after MoveData")
	}
	waitForState(t, tr2, torrent.StateSeeding, 5*time.Second)

	list := e.List()
	if len(list) != 1 || list[0].DownloadDir != newDir {
		t.Fatalf("List()[0].DownloadDir = %q, want %q", list[0].DownloadDir, newDir)
	}
}

// TestMoveDataRefusesNoSubfolderMultiFileTorrent proves the guard against
// moving a shared, unwrapped directory — see MoveData's own doc comment.
func TestMoveDataRefusesNoSubfolderMultiFileTorrent(t *testing.T) {
	e, err := New(t.TempDir(), Defaults{
		DownloadDir:   t.TempDir(),
		ResumeDir:     t.TempDir(),
		ContentLayout: storage.LayoutNoSubfolder,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	torrentDir := t.TempDir()
	path, hash := writeMultiFileTorrentFile(t, torrentDir, "multi")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := e.MoveData(hash, t.TempDir()); err == nil {
		t.Fatal("MoveData on a no-subfolder multi-file torrent: want an error, got nil")
	}
}
