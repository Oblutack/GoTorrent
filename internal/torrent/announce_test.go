package torrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
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

// TestAnnounceOneDualAnnouncesAHybridTorrent proves a hybrid torrent (both
// v1 and v2 present in the same info dict) announces to the same tracker
// URL twice — once under each identity — and merges both real responses
// into one, rather than only ever using the v1 hash a plain v1 torrent
// would. See announceOne's own doc comment for why: a v1-only swarm and a
// v2-only swarm on the same tracker can be genuinely disjoint.
func TestAnnounceOneDualAnnouncesAHybridTorrent(t *testing.T) {
	var mu sync.Mutex
	var gotHashes []string
	peerPort := byte(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotHashes = append(gotHashes, r.URL.Query().Get("info_hash"))
		peerPort++
		p := peerPort
		mu.Unlock()
		peerRespondingHandler(t, []byte{127, 0, 0, 1, 0, p}, 60)(w, r)
	}))
	defer srv.Close()

	files := map[string][]byte{"fileA.bin": genBytesV2(102400)}
	mi := buildV2TestTorrent(t, 32768, true, files) // hybrid
	if len(mi.PieceHashes) == 0 || mi.InfoHashV2.IsZero() {
		t.Fatal("test setup: expected a real hybrid torrent with both v1 and v2 identities")
	}

	tr := &Torrent{trackerClient: tracker.NewClient(nil), infoHash: mi.InfoHash}
	tr.mi.Store(mi)

	resp, err := tr.announceOne(context.Background(), srv.URL, tracker.EventStarted)
	if err != nil {
		t.Fatalf("announceOne: %v", err)
	}
	if len(resp.Peers) != 2 {
		t.Fatalf("got %d peers, want 2 (one real announce per identity, merged)", len(resp.Peers))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotHashes) != 2 {
		t.Fatalf("tracker received %d requests, want 2 (one per identity)", len(gotHashes))
	}
	wantV1 := string(mi.InfoHash[:])
	v2Truncated := mi.InfoHashV2.Truncated20()
	wantV2 := string(v2Truncated[:])
	if gotHashes[0] == gotHashes[1] {
		t.Fatalf("both announces used the same info_hash %x, want one v1 and one v2", gotHashes[0])
	}
	seen := map[string]bool{gotHashes[0]: true, gotHashes[1]: true}
	if !seen[wantV1] {
		t.Fatalf("tracker never received the v1 info_hash %x (got %x, %x)", wantV1, gotHashes[0], gotHashes[1])
	}
	if !seen[wantV2] {
		t.Fatalf("tracker never received the truncated v2 info_hash %x (got %x, %x)", wantV2, gotHashes[0], gotHashes[1])
	}
}

// TestAnnounceOneDoesNotDualAnnounceAPureV2Torrent proves a pure-v2
// torrent (no v1 pieces at all) announces exactly once per URL — it's
// already identified by its v2 hash (t.infoHash itself, per torrent.New's
// own identity selection), so a second announce would be a pointless
// duplicate rather than genuine extra swarm discovery.
func TestAnnounceOneDoesNotDualAnnounceAPureV2Torrent(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		peerRespondingHandler(t, []byte{127, 0, 0, 1, 0, 80}, 60)(w, r)
	}))
	defer srv.Close()

	files := map[string][]byte{"fileA.bin": genBytesV2(102400)}
	mi := buildV2TestTorrent(t, 32768, false, files) // pure v2
	if mi.InfoHashV2.IsZero() || len(mi.PieceHashes) != 0 {
		t.Fatal("test setup: expected a real pure-v2 torrent")
	}

	tr := &Torrent{trackerClient: tracker.NewClient(nil), infoHash: mi.InfoHashV2.Truncated20()}
	tr.mi.Store(mi)

	if _, err := tr.announceOne(context.Background(), srv.URL, tracker.EventStarted); err != nil {
		t.Fatalf("announceOne: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("tracker received %d requests, want exactly 1 for a pure-v2 torrent", got)
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
