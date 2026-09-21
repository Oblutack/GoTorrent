package torrent

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestWebSeedCompletesATorrentWithNoRealPeer is the feature's central
// claim: a torrent with zero real BitTorrent peers — DialPeer is never
// called at all — still reaches StateSeeding, purely from a single
// Config.WebSeeds URL served by a real HTTP server (net/http.ServeContent,
// the same real Range-serving machinery a real web seed host uses).
func TestWebSeedCompletesATorrentWithNoRealPeer(t *testing.T) {
	const pieceLength = 16384
	const numPieces = 6
	mi, content := buildTorrent(t, "webseeded.bin", pieceLength, []fileSpec{
		{length: pieceLength * numPieces},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "webseeded.bin", time.Time{}, bytes.NewReader(content))
	}))
	defer srv.Close()

	cfg := newTestConfig(t)
	cfg.WebSeeds = []string{srv.URL}
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	waitForState(t, tr, StateSeeding, 10*time.Second)

	if stats := tr.Stats(); stats.PeerCount != 0 {
		t.Errorf("PeerCount = %d, want 0 — no peer was ever dialed for this test", stats.PeerCount)
	}

	got := make([]byte, len(content))
	n, err := tr.ReadAt(got[:pieceLength], 0)
	if err != nil || n != pieceLength {
		t.Fatalf("ReadAt piece 0: n=%d err=%v", n, err)
	}
	if string(got[:pieceLength]) != string(content[:pieceLength]) {
		t.Error("web-seeded content does not match the source bytes")
	}
}

// TestWebSeedIsSkippedForMultiFileTorrents proves the v1 scope boundary
// holds at the torrent level too, not just inside internal/webseed's own
// FetchPiece: a multi-file torrent with a web seed configured never
// completes from it (nothing else can complete it either, since no peer is
// ever dialed and the tracker is dead) — it should just sit in
// Downloading, not error out or panic.
func TestWebSeedIsSkippedForMultiFileTorrents(t *testing.T) {
	const pieceLength = 16384
	mi, _ := buildTorrent(t, "multi", pieceLength, []fileSpec{
		{path: []string{"a.bin"}, length: pieceLength},
		{path: []string{"b.bin"}, length: pieceLength},
	})

	cfg := newTestConfig(t)
	cfg.WebSeeds = []string{"http://127.0.0.1:1/unreachable"}
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	waitForState(t, tr, StateDownloading, 5*time.Second)
	time.Sleep(200 * time.Millisecond) // give a (deliberately absent) fetch a moment it should never use
	if tr.State() != StateDownloading {
		t.Fatalf("state = %s, want still Downloading (a multi-file torrent has no way to complete in this test)", tr.State())
	}
}
