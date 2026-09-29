package torrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
)

// TestScrapeableURLsDedupsAcrossSources proves the flattened tracker list
// Scrape actually asks is deduplicated across all three sources it draws
// from (the metainfo's announce-list, a magnet-style Config.Trackers, and
// AddTracker's extraTrackers) — a torrent whose announce-list and tr=
// trackers happen to share a URL must be scraped once, not twice.
func TestScrapeableURLsDedupsAcrossSources(t *testing.T) {
	mi, _ := buildTorrent(t, "dedup", 16384, []fileSpec{{path: []string{"a.bin"}, length: 16384}})
	mi.AnnounceList = [][]string{{"http://a/announce"}, {"http://b/announce"}}

	cfg := newTestConfig(t)
	cfg.Trackers = []string{"http://a/announce", "http://c/announce"} // "a" duplicates the announce-list

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := tr.scrapeableURLs()
	want := map[string]bool{"http://a/announce": true, "http://b/announce": true, "http://c/announce": true}
	if len(got) != len(want) {
		t.Fatalf("scrapeableURLs = %v, want exactly the 3 distinct URLs in %v", got, want)
	}
	for _, u := range got {
		if !want[u] {
			t.Fatalf("scrapeableURLs contains unexpected URL %q", u)
		}
	}
}

func TestScrapeableURLsIsEmptyWithNoTrackersAtAll(t *testing.T) {
	mi, _ := buildTorrent(t, "no-trackers", 16384, []fileSpec{{path: []string{"a.bin"}, length: 16384}})
	mi.Announce = "" // buildTorrent's own fixture otherwise always sets one
	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := tr.scrapeableURLs(); len(got) != 0 {
		t.Fatalf("scrapeableURLs = %v, want none", got)
	}
	if got := tr.Scrape(context.Background()); got != nil {
		t.Fatalf("Scrape = %+v, want nil for a torrent with no trackers", got)
	}
}

// TestTorrentScrapeAsksEveryTrackerAndReportsFailuresIndependently drives
// Scrape against two real HTTP fixtures — one that answers with real swarm
// stats, one that answers with a failure reason — proving both that every
// tracker is actually asked (not just the first) and that one tracker
// failing doesn't lose the other's real result.
func TestTorrentScrapeAsksEveryTrackerAndReportsFailuresIndependently(t *testing.T) {
	type scrapeFileWire struct {
		Complete   int64 `bencode:"complete"`
		Downloaded int64 `bencode:"downloaded"`
		Incomplete int64 `bencode:"incomplete"`
	}
	type scrapeResponseWire struct {
		FailureReason string                    `bencode:"failure reason,omitempty"`
		Files         map[string]scrapeFileWire `bencode:"files"`
	}

	var gotHashMu sync.Mutex
	var gotHash string
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hashes := r.URL.Query()["info_hash"]
		if len(hashes) != 1 {
			t.Fatalf("scrape request to the good tracker carried %d info_hash params, want 1", len(hashes))
		}
		gotHashMu.Lock()
		gotHash = hashes[0]
		gotHashMu.Unlock()
		data, err := bencode.Marshal(scrapeResponseWire{
			Files: map[string]scrapeFileWire{hashes[0]: {Complete: 4, Downloaded: 10, Incomplete: 1}},
		})
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		w.Write(data)
	}))
	defer good.Close()

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := bencode.Marshal(scrapeResponseWire{FailureReason: "not registered"})
		w.Write(data)
	}))
	defer bad.Close()

	mi, _ := buildTorrent(t, "scrape-fleet", 16384, []fileSpec{{path: []string{"a.bin"}, length: 16384}})
	mi.AnnounceList = [][]string{{good.URL + "/announce"}, {bad.URL + "/announce"}}

	cfg := newTestConfig(t)
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := tr.Scrape(ctx)
	if len(results) != 2 {
		t.Fatalf("Scrape returned %d results, want 2: %+v", len(results), results)
	}

	byURL := make(map[string]TorrentScrapeResult, len(results))
	for _, r := range results {
		byURL[r.URL] = r
	}

	goodResult, ok := byURL[good.URL+"/announce"]
	if !ok {
		t.Fatalf("no result for the good tracker: %+v", results)
	}
	if goodResult.Err != nil {
		t.Fatalf("good tracker result has an error: %v", goodResult.Err)
	}
	if goodResult.Result == nil || goodResult.Result.Complete != 4 || goodResult.Result.Downloaded != 10 || goodResult.Result.Incomplete != 1 {
		t.Fatalf("good tracker result = %+v, want Complete=4 Downloaded=10 Incomplete=1", goodResult.Result)
	}
	gotHashMu.Lock()
	gotHashSnapshot := gotHash
	gotHashMu.Unlock()
	if gotHashSnapshot != string(tr.infoHash[:]) {
		t.Fatalf("good tracker received info_hash %x, want this torrent's own %x", gotHashSnapshot, tr.infoHash)
	}

	badResult, ok := byURL[bad.URL+"/announce"]
	if !ok {
		t.Fatalf("no result for the bad tracker: %+v", results)
	}
	if badResult.Err == nil {
		t.Fatal("bad tracker's failure reason did not surface as an error")
	}
	if badResult.Result != nil {
		t.Fatalf("bad tracker result unexpectedly carries data: %+v", badResult.Result)
	}
}
