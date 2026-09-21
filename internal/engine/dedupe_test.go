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
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// writeTorrentFileWithContent builds a real, valid single-file .torrent
// from caller-supplied content (rather than engine_test.go's own
// writeTorrentFile, which always generates its own fixed-seed random
// content) — this package's dedupe tests need two *different* torrents
// that deliberately share the exact same bytes, which is the whole point
// of the feature under test.
func writeTorrentFileWithContent(t *testing.T, dir, name string, pieceLength int64, content []byte) (path string, hash metainfo.Hash) {
	t.Helper()

	var hashes []byte
	for off := 0; off < len(content); off += int(pieceLength) {
		end := off + int(pieceLength)
		if end > len(content) {
			end = len(content)
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
	infoBytes, err := bencode.Marshal(infoWire{Length: int64(len(content)), Name: name, PieceLength: pieceLength, Pieces: hashes})
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

// TestDedupeCopiesPiecesAcrossTorrentsWithNoPeerInvolved is the feature's
// central claim, proven end to end: two different torrents (different
// names, hence different infohashes) built from the exact same content and
// piece length — the "same file, re-packaged under a different torrent"
// case ROADMAP.md describes. Torrent A is Added with the real file already
// correctly in place (verifies instantly, zero network). Torrent B is
// Added with an empty download directory and no peer ever dialed — if it
// reaches Seeding at all, every one of its bytes can only have come from
// local dedupe against A, since there is no other possible source.
func TestDedupeCopiesPiecesAcrossTorrentsWithNoPeerInvolved(t *testing.T) {
	const pieceLength = 16384
	const numPieces = 6
	content := make([]byte, pieceLength*numPieces)
	rand.New(rand.NewSource(31)).Read(content)

	torrentDir := t.TempDir()
	pathA, hashA := writeTorrentFileWithContent(t, torrentDir, "sourceA", pieceLength, content)
	pathB, hashB := writeTorrentFileWithContent(t, torrentDir, "sourceB", pieceLength, content)
	if hashA == hashB {
		t.Fatal("test setup bug: the two torrents must have different infohashes")
	}

	e := newTestEngine(t)

	dirA := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirA, "sourceA"), content, 0o644); err != nil {
		t.Fatalf("pre-placing torrent A's real content: %v", err)
	}
	if _, err := e.Add(pathA, dirA); err != nil {
		t.Fatalf("Add A: %v", err)
	}
	trA, _ := e.Get(hashA)
	waitForState(t, trA, torrent.StateSeeding, 5*time.Second)

	dirB := t.TempDir()
	if _, err := e.Add(pathB, dirB); err != nil {
		t.Fatalf("Add B: %v", err)
	}
	trB, _ := e.Get(hashB)

	// A generous deadline: dedupe fires from a detached goroutine on B's own
	// CheckingFiles -> Downloading transition, and every one of its 6 pieces
	// has to round-trip through B's own actor control channel — still
	// nowhere near as slow as waiting out a dead tracker or a real network
	// transfer would be, since it's all local reads and writes.
	waitForState(t, trB, torrent.StateSeeding, 5*time.Second)

	if stats := trB.Stats(); stats.PeerCount != 0 {
		t.Errorf("torrent B reports %d peers, want 0 — no peer was ever dialed, so any nonzero count means the test itself is wrong somewhere", stats.PeerCount)
	}

	got, err := os.ReadFile(filepath.Join(dirB, "sourceB"))
	if err != nil {
		t.Fatalf("reading torrent B's downloaded content: %v", err)
	}
	if string(got) != string(content) {
		t.Fatal("torrent B's on-disk content does not match the source bytes it should have deduped from A")
	}
}
