package torrent

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// fakeAnnounceTracker is a minimal HTTP tracker returning a single
// configured peer, for tests that need a real announce round trip rather
// than DialPeer directly — the whole point of AddTracker/tr= tests.
func fakeAnnounceTracker(t *testing.T, peerInfo tracker.PeerInfo) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		compact := append(append([]byte{}, peerInfo.IP.To4()...), byte(peerInfo.Port>>8), byte(peerInfo.Port))
		resp := struct {
			Interval int64  `bencode:"interval"`
			Peers    []byte `bencode:"peers"`
		}{Interval: 3600, Peers: compact}
		data, err := bencode.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal fake tracker response: %v", err)
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestAddTrackerReachesTheAnnounceLoop proves the whole feature end to end:
// a torrent started with zero trackers of its own discovers and connects to
// a peer purely because AddTracker told it about a tracker after the fact.
func TestAddTrackerReachesTheAnnounceLoop(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "addtracker.bin", pieceLength, []fileSpec{{length: pieceLength * 2}})
	seeder := newFakeSeeder(t, mi, content)
	srv := fakeAnnounceTracker(t, seeder.peerInfo())

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	if err := tr.AddTracker(srv.URL + "/announce"); err != nil {
		t.Fatalf("AddTracker: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if tr.Stats().PeerCount > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tr.Stats().PeerCount == 0 {
		t.Fatal("torrent never connected to the peer discovered via AddTracker")
	}
}

func TestAddTrackerRejectsEmptyURL(t *testing.T) {
	mi, _ := buildTorrent(t, "empty.bin", 16384, []fileSpec{{length: 16384}})
	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	if err := tr.AddTracker(""); err == nil {
		t.Fatal("AddTracker(\"\") accepted, want an error")
	}
}

// TestMagnetTrackersSurviveMetadataArrival is the regression test for a
// real bug: a magnet's tr= trackers (Config.Trackers) used to vanish from
// the announce loop the instant BEP 9 metadata arrived, since the loop
// switched entirely to mi.AnnounceList/Announce — which a magnet-sourced
// MetaInfo (built via metainfo.ParseInfo, never metainfo.Parse) never has.
// This drives a magnet-shaped torrent all the way through fetching real
// metadata over BEP 9, then proves its original tr= tracker is still being
// used afterward by having it hand back a *second*, distinct peer only
// discoverable through that tracker.
func TestMagnetTrackersSurviveMetadataArrival(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "survive.bin", pieceLength, []fileSpec{{length: pieceLength * 3}})
	metadataSeeder := newFakeSeederServingMetadata(t, mi, content)

	secondSeeder := newFakeSeeder(t, mi, content)
	srv := fakeAnnounceTracker(t, secondSeeder.peerInfo())

	cfg := newTestConfig(t)
	cfg.Trackers = []string{srv.URL + "/announce"}

	tr, err := NewFromInfoHash(mi.InfoHash, cfg)
	if err != nil {
		t.Fatalf("NewFromInfoHash: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateFetchingMetadata, 2*time.Second)

	tr.DialPeer(metadataSeeder.peerInfo())
	waitForState(t, tr, StateDownloading, 15*time.Second)
	if tr.Metadata() == nil {
		t.Fatal("torrent has no metadata after leaving FetchingMetadata")
	}

	// The tracker is only ever consulted by the announce loop, on its own
	// schedule (up to defaultAnnounceInterval before the bug fix's
	// restart-on-metadata behavior even existed) — poll rather than assume
	// a fixed delay.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tr.Stats().PeerCount >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := tr.Stats().PeerCount; got < 2 {
		t.Fatalf("PeerCount = %d after metadata arrived, want at least 2 (the magnet tracker's peer must still be reachable)", got)
	}
}

func TestExportTorrentFileReturnsErrNoMetadataForAMagnet(t *testing.T) {
	mi, _ := buildTorrent(t, "nometadata.bin", 16384, []fileSpec{{length: 16384}})
	tr, err := NewFromInfoHash(mi.InfoHash, newTestConfig(t))
	if err != nil {
		t.Fatalf("NewFromInfoHash: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateFetchingMetadata, 2*time.Second)

	if _, err := tr.ExportTorrentFile(); err != metainfo.ErrNoMetadata {
		t.Fatalf("ExportTorrentFile before metadata is known: got %v, want metainfo.ErrNoMetadata", err)
	}
}

// TestExportTorrentFileIncludesAddedTrackers proves ExportTorrentFile
// reflects AddTracker, not just whatever the torrent started with.
func TestExportTorrentFileIncludesAddedTrackers(t *testing.T) {
	mi, _ := buildTorrent(t, "export.bin", 16384, []fileSpec{{length: 16384}})
	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	if err := tr.AddTracker("http://added.example/announce"); err != nil {
		t.Fatalf("AddTracker: %v", err)
	}

	raw, err := tr.ExportTorrentFile()
	if err != nil {
		t.Fatalf("ExportTorrentFile: %v", err)
	}
	reparsed, err := metainfo.Parse(raw)
	if err != nil {
		t.Fatalf("Parse(ExportTorrentFile's output): %v", err)
	}
	if reparsed.InfoHash != mi.InfoHash {
		t.Fatal("ExportTorrentFile changed the infohash")
	}
	found := false
	for _, u := range reparsed.AnnounceURLs() {
		if u == "http://added.example/announce" {
			found = true
		}
	}
	if !found {
		t.Fatalf("exported AnnounceURLs() = %v, want it to include the AddTracker URL", reparsed.AnnounceURLs())
	}
}
