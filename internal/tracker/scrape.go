package tracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/version"
)

// ScrapeResult is one torrent's swarm statistics from a scrape (BEP 48 for
// HTTP(S) trackers, BEP 15's action=2 for UDP ones). Complete/Incomplete
// mirror AnnounceResponse's own fields (seeders/leechers currently active);
// Downloaded is the all-time count of peers that have ever finished, which
// no announce response carries at all — the one piece of information
// scrape has that a plain announce doesn't.
type ScrapeResult struct {
	InfoHash   [20]byte
	Complete   int
	Incomplete int
	Downloaded int
}

// ErrScrapeNotSupported is returned for an HTTP(S) tracker whose announce
// URL doesn't follow the scrape URL convention (see ScrapeURL) — an
// unofficial but universally implemented rule, not part of BEP 48 itself,
// which only defines the request/response shape once you already have a
// scrape URL. UDP trackers have no such restriction: BEP 15's scrape is
// just a different action (2) on the same connection an announce already
// uses, so announceUDP's own URL always works for scrapeUDP too.
var ErrScrapeNotSupported = errors.New("tracker: this tracker's announce URL does not support the scrape convention")

// ScrapeURL derives the scrape URL from an HTTP(S) announce URL, per the
// convention documented at
// https://wiki.theory.org/BitTorrentSpecification#Tracker_.27scrape.27_Convention
// (not part of BEP 48 itself, which is silent on how a client is meant to
// find the scrape endpoint — this is the de facto rule every real tracker
// and client actually implements): find the last '/' in the URL string,
// and if the text immediately following it starts with the literal string
// "announce", substitute "scrape" for that prefix. Operates on the raw
// URL string, not a parsed/re-escaped one — the convention explicitly
// requires "entity unquoting is not to be done", so this deliberately does
// not use net/url anywhere. Returns ok=false if the tracker doesn't
// support scraping by this convention.
func ScrapeURL(announceURL string) (string, bool) {
	idx := strings.LastIndex(announceURL, "/")
	if idx < 0 {
		return "", false
	}
	rest := announceURL[idx+1:]
	if !strings.HasPrefix(rest, "announce") {
		return "", false
	}
	return announceURL[:idx+1] + "scrape" + rest[len("announce"):], true
}

// Scrape asks announceURL's tracker for current swarm statistics on one or
// more torrents in a single round trip, dispatching to the UDP (BEP 15,
// action 2) or HTTP(S) (BEP 48) implementation by the announce URL's
// scheme, the same way Announce does. The two protocols disagree on what
// happens for an infoHash the tracker has no data for: UDP's response is
// purely positional (one 12-byte entry per requested hash, in order, with
// no way to signal "unknown" beyond all-zero counts), so scrapeUDP always
// returns exactly len(infoHashes) results; HTTP's response is a dict keyed
// by infohash, so scrapeHTTP omits any hash the tracker didn't include —
// this asymmetry is real, not a bug, and documented here rather than
// papered over with synthesized zero entries HTTP never actually sent.
func (c *Client) Scrape(ctx context.Context, announceURL string, infoHashes [][20]byte) ([]ScrapeResult, error) {
	if len(infoHashes) == 0 {
		return nil, errors.New("tracker: scrape needs at least one info hash")
	}

	u, err := url.Parse(announceURL)
	if err != nil {
		return nil, fmt.Errorf("tracker: invalid announce URL %q: %w", announceURL, err)
	}
	switch u.Scheme {
	case "udp":
		return c.scrapeUDP(ctx, u, infoHashes)
	case "http", "https":
		return c.scrapeHTTP(ctx, announceURL, infoHashes)
	default:
		return nil, fmt.Errorf("tracker: unsupported announce scheme %q", u.Scheme)
	}
}

// scrapeFileWire mirrors one torrent's entry in a BEP 48 scrape response's
// "files" dictionary. The spec documents exactly these three fields — no
// "name" or per-response "flags"/min_request_interval, despite some real
// trackers sending extras; unknown keys are ignored the same way every
// other bencoded dict in this codebase tolerates them.
type scrapeFileWire struct {
	Complete   int64 `bencode:"complete"`
	Downloaded int64 `bencode:"downloaded"`
	Incomplete int64 `bencode:"incomplete"`
}

type scrapeResponseWire struct {
	FailureReason string                    `bencode:"failure reason,omitempty"`
	Files         map[string]scrapeFileWire `bencode:"files"`
}

// scrapeHTTP implements BEP 48 over HTTP(S).
func (c *Client) scrapeHTTP(ctx context.Context, announceURL string, infoHashes [][20]byte) ([]ScrapeResult, error) {
	scrapeURL, ok := ScrapeURL(announceURL)
	if !ok {
		return nil, ErrScrapeNotSupported
	}

	base, err := url.Parse(scrapeURL)
	if err != nil {
		return nil, fmt.Errorf("tracker: bad scrape URL %q: %w", scrapeURL, err)
	}
	params := url.Values{}
	for _, h := range infoHashes {
		// url.Values.Add percent-encodes the raw binary infohash the same
		// way AnnounceRequest.BuildURL already does for info_hash.
		params.Add("info_hash", string(h[:]))
	}
	if base.RawQuery != "" {
		base.RawQuery += "&" + params.Encode()
	} else {
		base.RawQuery = params.Encode()
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("tracker: could not build scrape request: %w", err)
	}
	httpReq.Header.Set("User-Agent", version.UserAgent)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tracker: scrape of %s failed: %w", scrapeURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("tracker: %s returned status %d: %s", scrapeURL, resp.StatusCode, snippet)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("tracker: reading scrape response from %s: %w", scrapeURL, err)
	}

	var wire scrapeResponseWire
	if err := bencode.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("tracker: could not decode scrape response from %s: %w", scrapeURL, err)
	}
	if wire.FailureReason != "" {
		return nil, &ErrTrackerFailure{URL: scrapeURL, Reason: wire.FailureReason}
	}

	out := make([]ScrapeResult, 0, len(infoHashes))
	for _, h := range infoHashes {
		fw, ok := wire.Files[string(h[:])]
		if !ok {
			continue // the tracker has no data for this torrent — not an error
		}
		out = append(out, ScrapeResult{
			InfoHash:   h,
			Complete:   int(fw.Complete),
			Incomplete: int(fw.Incomplete),
			Downloaded: int(fw.Downloaded),
		})
	}
	return out, nil
}
