package engine

import (
	"context"
	"crypto/sha1"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/torrent"
	"github.com/Oblutack/GoTorrent/internal/tracker"
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

// TestListDuringPieceVerificationDoesNotDeadlock is the regression test for
// a real deadlock found live: a genuinely downloading torrent, with
// Desktop's own 2s poll / 1s WS sessionStats tick hammering List()/
// GetSummary() concurrently, could (and reliably did) deadlock the whole
// engine forever. The cycle: List/GetSummary hold e.mu while calling
// summaryLocked -> tr.Stats(), a real control-channel round trip serviced
// only by that torrent's own actor goroutine; meanwhile
// recordDedupeSource — called synchronously from OnPieceVerified, which
// fires ON that exact actor goroutine — used to re-derive its own *Torrent
// via e.Get(owner), which needs that same e.mu. Whichever one landed first
// froze both: the List()/GetSummary() caller waiting forever on a Stats()
// reply from an actor that's itself waiting forever on the lock the caller
// is holding. Fixed by having the OnPieceVerified hook pass its own
// already-in-scope *torrent.Torrent straight to recordDedupeSource,
// removing its need for e.Get (and therefore e.mu) entirely.
//
// This test drives a real seed/leech transfer over real TCP loopback — the
// same real-world shape that surfaced the bug live, not a synthetic race —
// while hammering List()/GetSummary() in a tight, unthrottled loop for the
// whole transfer, the most aggressive real caller of either method this
// codebase has (Desktop's own timers are 1-2s apart). waitForState's own
// bounded timeout means a real deadlock fails this test cleanly rather
// than hanging the suite - confirmed load-bearing by reverting the fix and
// watching this exact test genuinely hang under `go test -timeout 25s`
// (a real "test timed out" panic with every goroutine's stack, matching
// the live debugger session that found this bug byte for byte) before
// restoring it.
func TestListDuringPieceVerificationDoesNotDeadlock(t *testing.T) {
	const pieceLength = 16384
	const numPieces = 40
	content := make([]byte, pieceLength*numPieces)
	rand.New(rand.NewSource(7)).Read(content)

	torrentDir := t.TempDir()
	path, hash := writeTorrentFileWithContent(t, torrentDir, "racey", pieceLength, content)

	seederDownloadDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(seederDownloadDir, "racey"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}
	seeder, err := New(t.TempDir(), Defaults{DownloadDir: seederDownloadDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New (seeder): %v", err)
	}
	t.Cleanup(seeder.Shutdown)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probing for a free port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	seeder.defaults.ListenPort = uint16(port)
	if err := seeder.Listen(context.Background()); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if _, err := seeder.Add(path, ""); err != nil {
		t.Fatalf("seeder Add: %v", err)
	}
	seederTr, ok := seeder.Get(hash)
	if !ok {
		t.Fatal("seeder torrent missing right after Add")
	}
	waitForState(t, seederTr, torrent.StateSeeding, 5*time.Second)

	leecher := newTestEngine(t)
	if _, err := leecher.Add(path, ""); err != nil {
		t.Fatalf("leecher Add: %v", err)
	}
	leecherTr, ok := leecher.Get(hash)
	if !ok {
		t.Fatal("leecher torrent missing right after Add")
	}

	// Hammer List()/GetSummary() as fast as possible for the whole
	// transfer — every call takes e.mu and, for this torrent, calls
	// tr.Stats() while still holding it, exactly the shape that raced
	// against a real piece verification in production. pollCount is an
	// atomic, not a plain int — written by this goroutine, read by the
	// main test goroutine after close(stop) with no other synchronization
	// between the two, exactly the kind of access `go test -race` is
	// built to catch (and did: a real, if ironic, data race in this
	// deadlock test's own bookkeeping, caught by CI before it ever landed).
	stop := make(chan struct{})
	var pollCount atomic.Int64
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				leecher.List()
				leecher.GetSummary(hash)
				pollCount.Add(1)
			}
		}
	}()

	leecherTr.DialPeer(tracker.PeerInfo{IP: net.ParseIP("127.0.0.1"), Port: uint16(port)})
	waitForState(t, leecherTr, torrent.StateSeeding, 30*time.Second)
	close(stop)

	if pollCount.Load() == 0 {
		t.Fatal("test setup bug: the List()/GetSummary() hammer goroutine never ran")
	}

	got, err := os.ReadFile(filepath.Join(leecher.defaults.DownloadDir, "racey"))
	if err != nil {
		t.Fatalf("reading leecher's downloaded content: %v", err)
	}
	if string(got) != string(content) {
		t.Fatal("leecher's downloaded content does not match the source bytes")
	}
}
