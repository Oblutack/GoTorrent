package torrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// peerRespondingHandler builds a minimal fake tracker that always answers
// with one compact peer entry and a fixed interval.
func peerRespondingHandler(t *testing.T, compactPeer []byte, intervalSeconds int64) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			Interval int64  `bencode:"interval"`
			Peers    []byte `bencode:"peers"`
		}{Interval: intervalSeconds, Peers: compactPeer}
		data, err := bencode.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal fake tracker response: %v", err)
		}
		w.Write(data)
	}
}

func TestBuildTiersShufflesWithinATierButKeepsMembership(t *testing.T) {
	in := [][]string{{"http://a", "http://b", "http://c"}}
	tiers := buildTiers(in, "")
	if len(tiers) != 1 || len(tiers[0].urls) != 3 {
		t.Fatalf("got %+v, want one tier of 3", tiers)
	}
	want := map[string]bool{"http://a": true, "http://b": true, "http://c": true}
	for _, u := range tiers[0].urls {
		if !want[u] {
			t.Fatalf("tier contains unexpected URL %q", u)
		}
		delete(want, u)
	}
	if len(want) != 0 {
		t.Fatalf("tier is missing URLs: %v", want)
	}
}

func TestBuildTiersDropsTiersWithNoSupportedScheme(t *testing.T) {
	tiers := buildTiers([][]string{
		{"ftp://unsupported", "http://a"},
		{"ftp://onlybad"},
		{"udp://b:6969"},
	}, "")
	if len(tiers) != 2 {
		t.Fatalf("got %d tiers, want 2 (the all-ftp tier dropped): %+v", len(tiers), tiers)
	}
}

func TestBuildTiersFallsBackToAnnounceWhenListIsEmpty(t *testing.T) {
	tiers := buildTiers(nil, "http://only")
	if len(tiers) != 1 || len(tiers[0].urls) != 1 || tiers[0].urls[0] != "http://only" {
		t.Fatalf("got %+v, want a single tier with just the announce URL", tiers)
	}
}

func TestBuildTiersIgnoresAnnounceWhenListIsPresent(t *testing.T) {
	tiers := buildTiers([][]string{{"http://a"}}, "http://ignored-per-bep-12")
	if len(tiers) != 1 || len(tiers[0].urls) != 1 || tiers[0].urls[0] != "http://a" {
		t.Fatalf("got %+v, want only the announce-list tier", tiers)
	}
}

func TestAnnounceTierPromotesTheTrackerThatAnswered(t *testing.T) {
	var badHits, goodHits int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&badHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	innerHandler := peerRespondingHandler(t, []byte{127, 0, 0, 1, 0x1F, 0x90}, 60)
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&goodHits, 1)
		innerHandler(w, r)
	}))
	defer good.Close()

	tr := &Torrent{trackerClient: tracker.NewClient(nil)}
	resp, workedAt, err := tr.announceTier(context.Background(), []string{bad.URL, good.URL}, tracker.EventStarted)
	if err != nil {
		t.Fatalf("announceTier: %v", err)
	}
	if workedAt != 1 {
		t.Fatalf("workedAt = %d, want 1 (the second URL, since the first failed)", workedAt)
	}
	if resp.Interval != 60*time.Second {
		t.Fatalf("Interval = %s, want 60s", resp.Interval)
	}
	if atomic.LoadInt32(&badHits) == 0 || atomic.LoadInt32(&goodHits) == 0 {
		t.Fatalf("expected both trackers in the tier to be tried, got bad=%d good=%d", badHits, goodHits)
	}
}

func TestAnnounceTiersAggregatesPeersAcrossTiers(t *testing.T) {
	tier1 := httptest.NewServer(peerRespondingHandler(t, []byte{127, 0, 0, 1, 0x1F, 0x90}, 60))
	defer tier1.Close()
	tier2 := httptest.NewServer(peerRespondingHandler(t, []byte{127, 0, 0, 1, 0x1F, 0x91}, 120))
	defer tier2.Close()

	tr := &Torrent{trackerClient: tracker.NewClient(nil)}
	tiers := []trackerTier{{urls: []string{tier1.URL}}, {urls: []string{tier2.URL}}}

	resp, err := tr.announceTiers(context.Background(), tiers, tracker.EventStarted)
	if err != nil {
		t.Fatalf("announceTiers: %v", err)
	}
	if len(resp.Peers) != 2 {
		t.Fatalf("got %d peers, want 2 (one from each tier): %+v", len(resp.Peers), resp.Peers)
	}
	if resp.Interval != 60*time.Second {
		t.Fatalf("Interval = %s, want 60s (the shorter of the two tiers' intervals)", resp.Interval)
	}
}

func TestAnnounceTiersToleratesAFullyFailedTier(t *testing.T) {
	good := httptest.NewServer(peerRespondingHandler(t, []byte{127, 0, 0, 1, 0, 80}, 60))
	defer good.Close()

	tr := &Torrent{trackerClient: tracker.NewClient(nil)}
	tiers := []trackerTier{
		{urls: []string{"http://127.0.0.1:1/announce"}}, // nothing listens on port 1
		{urls: []string{good.URL}},
	}

	resp, err := tr.announceTiers(context.Background(), tiers, tracker.EventStarted)
	if err != nil {
		t.Fatalf("announceTiers: %v", err)
	}
	if len(resp.Peers) != 1 {
		t.Fatalf("got %d peers, want 1 from the surviving tier", len(resp.Peers))
	}
}

func TestAnnounceTiersErrorsOnlyWhenEveryTierFails(t *testing.T) {
	tr := &Torrent{trackerClient: tracker.NewClient(nil)}
	tiers := []trackerTier{
		{urls: []string{"http://127.0.0.1:1/announce"}},
		{urls: []string{"http://127.0.0.1:1/announce"}},
	}
	if _, err := tr.announceTiers(context.Background(), tiers, tracker.EventStarted); err == nil {
		t.Fatal("announceTiers succeeded with every tier dead, want an error")
	}
}
