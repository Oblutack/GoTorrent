package engine

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/torrent"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// waitForEvent polls ch for an event of kind, ignoring anything else, up
// to a bounded timeout - events fire from asynchronous actor callbacks, so
// a test can never assume the very next receive is the one it wants.
func waitForEvent(t *testing.T, ch <-chan Event, kind EventKind, timeout time.Duration) Event {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		select {
		case ev := <-ch:
			if ev.Kind == kind {
				return ev
			}
		case <-time.After(time.Until(deadline)):
			t.Fatalf("no %s event within %s", kind, timeout)
		}
	}
}

func TestSubscribeReceivesTorrentAddedAndRemoved(t *testing.T) {
	e := newTestEngine(t)
	ch, cancel := e.Subscribe()
	defer cancel()

	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "eventadd")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	added := waitForEvent(t, ch, EventTorrentAdded, 5*time.Second)
	if added.InfoHash != hash {
		t.Fatalf("EventTorrentAdded.InfoHash = %s, want %s", added.InfoHash, hash)
	}

	if err := e.Remove(hash); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	removed := waitForEvent(t, ch, EventTorrentRemoved, 5*time.Second)
	if removed.InfoHash != hash {
		t.Fatalf("EventTorrentRemoved.InfoHash = %s, want %s", removed.InfoHash, hash)
	}
}

func TestSubscribeReceivesTorrentStateChanged(t *testing.T) {
	e := newTestEngine(t)
	ch, cancel := e.Subscribe()
	defer cancel()

	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "eventstate")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ev := waitForEvent(t, ch, EventTorrentStateChanged, 5*time.Second)
	if ev.InfoHash != hash {
		t.Fatalf("InfoHash = %s, want %s", ev.InfoHash, hash)
	}
	// A dead-tracker torrent verifies straight to Downloading (nothing on
	// disk), so this is the one state transition guaranteed to happen.
	if ev.State != torrent.StateDownloading && ev.State != torrent.StateCheckingFiles {
		t.Fatalf("State = %v, want CheckingFiles or Downloading", ev.State)
	}

	// Let the torrent settle into Downloading before this test returns and
	// e's Cleanup-registered Shutdown fires - returning right after the
	// first (possibly still-transient CheckingFiles) event risks Shutdown's
	// context cancellation racing an in-flight storage.Allocate/Verify call
	// on this same torrent, which on Windows can still be holding a file
	// handle when TempDir's own cleanup tries to remove it.
	tr, ok := e.Get(hash)
	if ok {
		waitForState(t, tr, torrent.StateDownloading, 5*time.Second)
	}
}

// TestSubscribeReceivesPeerAndPieceEvents drives a real download between
// two real Engines - a pre-seeded seeder (real content on disk, verifies
// straight to Seeding) and a leecher that dials it - and proves the
// leecher's own subscription sees both a real peer connect and a real
// piece verify, mirroring the pre-seed + Listen + DialPeer pattern
// seedlimit_test.go and engine_test.go's own inbound-routing test already
// use.
func TestSubscribeReceivesPeerAndPieceEvents(t *testing.T) {
	seederDownloadDir := t.TempDir()
	torrentDir := t.TempDir()
	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "eventpeer")
	if err := os.WriteFile(filepath.Join(seederDownloadDir, "eventpeer"), content, 0o644); err != nil {
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
	ch, cancel := leecher.Subscribe()
	defer cancel()

	if _, err := leecher.Add(path, ""); err != nil {
		t.Fatalf("leecher Add: %v", err)
	}
	leecherTr, ok := leecher.Get(hash)
	if !ok {
		t.Fatal("leecher torrent missing right after Add")
	}
	leecherTr.DialPeer(tracker.PeerInfo{IP: net.ParseIP("127.0.0.1"), Port: uint16(port)})

	peerEv := waitForEvent(t, ch, EventPeerConnected, 5*time.Second)
	if peerEv.InfoHash != hash || peerEv.PeerAddr == "" {
		t.Fatalf("EventPeerConnected = %+v", peerEv)
	}

	// The seeder's choker only makes its first unchoke decision on its own
	// 10s ticker (torrent.chokeInterval) - nothing unchokes a brand-new
	// connection immediately on Run start - so real data doesn't start
	// flowing until close to that mark. 30s here matches the timeout every
	// other full-download-shaped test in this codebase already uses for
	// exactly this reason (e.g. internal/torrent's TestFullDownload).
	pieceEv := waitForEvent(t, ch, EventPieceVerified, 30*time.Second)
	if pieceEv.InfoHash != hash {
		t.Fatalf("EventPieceVerified.InfoHash = %s, want %s", pieceEv.InfoHash, hash)
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	e := newTestEngine(t)
	ch, cancel := e.Subscribe()
	cancel()

	torrentDir := t.TempDir()
	path, _ := writeTorrentFile(t, torrentDir, "eventcancel")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("received an event after cancel: %+v", ev)
		}
		// ok == false: the channel was closed by cancel, which is correct.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("channel was neither closed nor did it stay silent - cancel had no effect")
	}
}
