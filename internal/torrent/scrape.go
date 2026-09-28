package torrent

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// TorrentScrapeResult is one tracker's scrape reply for this torrent.
// Result is nil (with Err set) if that specific tracker failed or doesn't
// support scraping at all — an HTTP(S) tracker whose announce URL doesn't
// follow the scrape URL convention returns tracker.ErrScrapeNotSupported,
// which is a fact about that one tracker, not a reason to fail the whole
// call.
type TorrentScrapeResult struct {
	URL    string
	Result *tracker.ScrapeResult
	Err    error
}

// Scrape asks every tracker this torrent currently knows about — the same
// set announceOnce would flatten into tiers: the metainfo's announce-list/
// announce, a magnet's tr= trackers (Config.Trackers), and anything
// AddTracker has recorded since — for this torrent's current swarm
// statistics, one request per tracker, in parallel. Unlike an announce,
// this never registers this client with a tracker, carries no event, and
// doesn't affect the announce loop's own interval; it's read-only, so it
// needs no actor round trip at all. t.mi/t.cfg.Trackers/t.extraTrackers
// are the same atomics/immutable fields TrackerStatuses and
// ExportTorrentFile already read from any goroutine, and t.trackerClient
// is already shared across every announce goroutine that calls into it
// concurrently — Scrape is just one more concurrent caller.
//
// A caller almost always wants to bound how long this can take: a dead
// UDP tracker's own retry/backoff schedule (see udpRoundTrip) can run for
// a very long time on its own, and Scrape does not shorten that — it
// relies entirely on ctx's deadline to cut a slow or dead tracker off,
// exactly as announceUDP already does.
func (t *Torrent) Scrape(ctx context.Context) []TorrentScrapeResult {
	urls := t.scrapeableURLs()
	if len(urls) == 0 {
		return nil
	}

	results := make([]TorrentScrapeResult, len(urls))
	var wg sync.WaitGroup
	wg.Add(len(urls))
	for i, url := range urls {
		go func(i int, url string) {
			defer wg.Done()
			res, err := t.trackerClient.Scrape(ctx, url, [][20]byte{t.infoHash})
			out := TorrentScrapeResult{URL: url}
			switch {
			case err != nil:
				out.Err = err
			case len(res) == 0:
				out.Err = errors.New("tracker: scrape succeeded but had no data for this torrent")
			default:
				out.Result = &res[0]
			}
			results[i] = out
		}(i, url)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].URL < results[j].URL })
	return results
}

// scrapeableURLs flattens every tier announceOnce would build into one
// deduplicated list — scrape has no notion of tiers or try-the-next-one-
// on-failure, unlike an announce (every tracker is asked, independently),
// so there's no reason to preserve tier structure here. Reuses buildTiers
// itself, rather than reimplementing it, specifically to inherit its BEP
// 12 precedence rule (mi.Announce is a fallback used only when
// AnnounceList is empty, never added alongside it) — getting that rule
// wrong here would silently scrape a plain "announce" URL BEP 12 says to
// ignore once a real announce-list exists.
func (t *Torrent) scrapeableURLs() []string {
	seen := make(map[string]bool)
	var out []string
	add := func(urls []string) {
		for _, u := range urls {
			if !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}

	if mi := t.mi.Load(); mi != nil {
		for _, tier := range buildTiers(mi.AnnounceList, mi.Announce) {
			add(tier.urls)
		}
	}
	add(supportedAnnounceURLs(t.cfg.Trackers))
	if p := t.extraTrackers.Load(); p != nil {
		add(supportedAnnounceURLs(*p))
	}
	return out
}
