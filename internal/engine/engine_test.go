package engine

import (
	"crypto/sha1"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

func TestMain(m *testing.M) {
	logger.Init(false)
	os.Exit(m.Run())
}

// writeTorrentFile builds a small, valid, single-file .torrent with a dead
// announce URL (nobody needs to actually seed it: these tests only exercise
// add/list/remove/persist, not a real transfer) and returns its path and
// infohash.
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

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(t.TempDir(), Defaults{
		DownloadDir: t.TempDir(),
		ResumeDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)
	return e
}

func waitForState(t *testing.T, tr *torrent.Torrent, want torrent.State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if tr.State() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("torrent did not reach %s within %s (stuck at %s)", want, timeout, tr.State())
}

func TestAddStartsAndListsTheTorrent(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "one")

	got, err := e.Add(path, "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got != hash {
		t.Fatalf("Add returned %s, want %s", got, hash)
	}

	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get did not find the added torrent")
	}
	// No real content exists on disk, so verification finds every piece
	// missing and the torrent settles into Downloading (with a dead tracker,
	// nothing will ever complete it) rather than Seeding.
	waitForState(t, tr, torrent.StateDownloading, 10*time.Second)

	list := e.List()
	if len(list) != 1 {
		t.Fatalf("List() has %d entries, want 1", len(list))
	}
	if list[0].InfoHash != hash {
		t.Fatalf("List()[0].InfoHash = %s, want %s", list[0].InfoHash, hash)
	}
	if list[0].TorrentPath != path {
		t.Fatalf("List()[0].TorrentPath = %q, want %q", list[0].TorrentPath, path)
	}
}

func TestAddRejectsDuplicateInfoHash(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, _ := writeTorrentFile(t, torrentDir, "dup")

	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if _, err := e.Add(path, ""); err == nil {
		t.Fatal("second Add of the same torrent succeeded, want an error")
	}
	if len(e.List()) != 1 {
		t.Fatalf("List() has %d entries after a rejected duplicate, want 1", len(e.List()))
	}
}

func TestAddRequiresADownloadDirectory(t *testing.T) {
	e, err := New(t.TempDir(), Defaults{}) // no default DownloadDir
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	path, _ := writeTorrentFile(t, t.TempDir(), "no-dir")
	if _, err := e.Add(path, ""); err == nil {
		t.Fatal("Add with no download directory anywhere succeeded, want an error")
	}
	if len(e.List()) != 0 {
		t.Fatal("a rejected Add left an entry behind")
	}
}

func TestRemoveStopsAndUnlistsTheTorrent(t *testing.T) {
	e := newTestEngine(t)
	path, hash := writeTorrentFile(t, t.TempDir(), "remove-me")

	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Remove blocks on Torrent.Stop, which blocks on Run returning; if
	// Remove ever stopped doing that, this call would hang and the test
	// would time out rather than fail cleanly, but that is still a real
	// signal something regressed.
	if err := e.Remove(hash); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, ok := e.Get(hash); ok {
		t.Fatal("Get still finds a removed torrent")
	}
	if len(e.List()) != 0 {
		t.Fatalf("List() has %d entries after Remove, want 0", len(e.List()))
	}
}

func TestRemoveUnknownHashErrors(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Remove(metainfo.Hash{}); err == nil {
		t.Fatal("Remove of an unmanaged hash succeeded, want an error")
	}
}

// TestLoadReconstructsTheFleet proves the manifest survives across two
// independent Engine instances pointed at the same state directory — the
// "persist across torrents" half of the fleet-manager requirement.
func TestLoadReconstructsTheFleet(t *testing.T) {
	stateDir := t.TempDir()
	downloadDir := t.TempDir()
	torrentDir := t.TempDir()

	pathA, hashA := writeTorrentFile(t, torrentDir, "a")
	pathB, hashB := writeTorrentFile(t, torrentDir, "b")

	e1, err := New(stateDir, Defaults{DownloadDir: downloadDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := e1.Add(pathA, ""); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	if _, err := e1.Add(pathB, ""); err != nil {
		t.Fatalf("Add b: %v", err)
	}
	e1.Shutdown()

	e2, err := New(stateDir, Defaults{DownloadDir: downloadDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New (second engine): %v", err)
	}
	t.Cleanup(e2.Shutdown)
	if err := e2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	list := e2.List()
	if len(list) != 2 {
		t.Fatalf("List() after Load has %d entries, want 2", len(list))
	}
	if _, ok := e2.Get(hashA); !ok {
		t.Fatal("Load did not reconstruct torrent a")
	}
	if _, ok := e2.Get(hashB); !ok {
		t.Fatal("Load did not reconstruct torrent b")
	}
}

// TestLoadWithNoManifestIsNotAnError covers the first-run case: nothing has
// ever been added, so there is no manifest file yet.
func TestLoadWithNoManifestIsNotAnError(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Load(); err != nil {
		t.Fatalf("Load with no manifest: %v", err)
	}
	if len(e.List()) != 0 {
		t.Fatal("Load with no manifest fabricated an entry")
	}
}
