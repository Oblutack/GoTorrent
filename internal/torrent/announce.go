package torrent

import (
	"context"
	"math/rand"
	"strings"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// defaultAnnounceInterval is used until a tracker tells us otherwise.
const defaultAnnounceInterval = 30 * time.Minute

// trackerTier is one announce-list tier (BEP 12): trackers to try in order
// until one succeeds. The order is mutable — a successful tracker gets
// promoted to the front so it's tried first next time — which is exactly
// why this lives as loop-local state in announceLoop rather than being
// rebuilt from metainfo on every iteration.
type trackerTier struct {
	urls []string
}

// announceLoop periodically announces to every tracker tier and feeds
// discovered peers back to the actor. It runs until ctx is cancelled.
//
// Tiers are built once — from Config.Trackers (a magnet's tr=) if metadata
// isn't known yet, or from the real announce-list once it is — and then
// only mutated in place (shuffling within a tier once, promoting whichever
// tracker answers to the front of its tier), never rebuilt, so that
// promotion survives across iterations. The one exception is the magnet
// case: tiers are deliberately rebuilt exactly once, the first time
// metadata becomes available, to switch from the magnet's flat tr= list to
// the real tiered announce-list — see the haveMetadata guard below.
//
// A final "stopped"/"completed" announce is deliberately not this loop's
// job — see announceOnce — since those need their own bounded timeout
// independent of the loop's re-announce cadence.
func (t *Torrent) announceLoop(ctx context.Context, firstEvent tracker.Event) {
	defer t.wg.Done()

	event := firstEvent
	interval := defaultAnnounceInterval

	var tiers []trackerTier
	haveMetadata := false

	for {
		if mi := t.mi.Load(); mi != nil {
			if !haveMetadata {
				tiers = buildTiers(mi.AnnounceList, mi.Announce)
				// A magnet's tr= parameters (Config.Trackers) are never
				// part of what BEP 9 actually exchanges (only the info
				// dict), so mi.AnnounceList/Announce are empty for a
				// magnet-sourced MetaInfo — without this, a magnet
				// torrent's tracker-based peer discovery would silently
				// stop the instant metadata arrived, keeping only
				// DHT/PEX/LSD. A real bug, found while wiring 3.6's
				// AddTracker/extraTrackers into this same loop.
				if urls := supportedAnnounceURLs(t.cfg.Trackers); len(urls) > 0 {
					tiers = append(tiers, trackerTier{urls: urls})
				}
				haveMetadata = true
			}
		} else if tiers == nil {
			tiers = buildTiers(nil, "")
			if urls := supportedAnnounceURLs(t.cfg.Trackers); len(urls) > 0 {
				tiers = []trackerTier{{urls: urls}}
			}
		}

		allTiers := tiers
		if extra := t.extraTrackerTier(); extra != nil {
			allTiers = append(append([]trackerTier(nil), tiers...), *extra)
		}

		if len(allTiers) == 0 {
			logger.Logf("torrent %s: no trackers to announce to yet\n", t.infoHash)
		} else if resp, err := t.announceTiers(ctx, allTiers, event); err != nil {
			logger.Warning.Printf("torrent %s: announce failed across every tier: %v\n", t.infoHash, err)
		} else {
			if resp.Interval > 0 {
				interval = resp.Interval
			}
			if resp.MinInterval > interval {
				interval = resp.MinInterval
			}
			t.sendEvent(ctx, eventTrackerPeers{peers: resp.Peers})
		}
		event = tracker.EventNone

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// restartAnnounceLoop stops whatever announce loop is currently running (if
// any) and starts a fresh one under a context derived from t.ctx, so it
// still exits when the torrent itself is stopped without needing its own
// explicit cleanup. Actor-only: called from Run's setup and from
// doResume/doRecheck's control handlers.
func (t *Torrent) restartAnnounceLoop(firstEvent tracker.Event) {
	t.stopAnnounceLoop()
	ctx, cancel := context.WithCancel(t.ctx)
	t.announceCancel = cancel
	t.wg.Add(1)
	go t.announceLoop(ctx, firstEvent)
}

// stopAnnounceLoop cancels the running announce loop, if any. Pause calls
// this so a subsequent Resume's restartAnnounceLoop doesn't end up racing (or
// duplicating) a loop from before the pause.
func (t *Torrent) stopAnnounceLoop() {
	if t.announceCancel != nil {
		t.announceCancel()
		t.announceCancel = nil
	}
}

// announceOnce sends a single announce across every tier — used for
// "stopped" and "completed", and for the plain re-announce after a forced
// recheck — with its own bounded timeout, so shutdown is never stuck
// waiting on a slow or dead tracker. A torrent with no announceable
// trackers yet (e.g. mid-magnet, with no tr= parameters) is a silent no-op.
// Unlike announceLoop, this builds tiers fresh every call: a one-off
// announce has no promotion state worth keeping.
func (t *Torrent) announceOnce(event tracker.Event, timeout time.Duration) {
	var tiers []trackerTier
	if mi := t.mi.Load(); mi != nil {
		tiers = buildTiers(mi.AnnounceList, mi.Announce)
		// See announceLoop's identical comment: a magnet's tr= parameters
		// are not part of the exchanged metadata and must be kept even
		// after metadata is known.
		if urls := supportedAnnounceURLs(t.cfg.Trackers); len(urls) > 0 {
			tiers = append(tiers, trackerTier{urls: urls})
		}
	} else if urls := supportedAnnounceURLs(t.cfg.Trackers); len(urls) > 0 {
		tiers = []trackerTier{{urls: urls}}
	}
	if extra := t.extraTrackerTier(); extra != nil {
		tiers = append(tiers, *extra)
	}
	if len(tiers) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, err := t.announceTiers(ctx, tiers, event); err != nil {
		logger.Logf("torrent %s: %s announce failed: %v\n", t.infoHash, event, err)
	}
}

// extraTrackerTier returns a tier holding whatever AddTracker has recorded
// so far, or nil if none. Unlike tiers (built once and then only mutated in
// place for promotion), this is rebuilt fresh from the atomic snapshot on
// every call — so a tracker promoted to the front of it does not stay
// promoted across announce cycles. Accepted: the point of AddTracker is
// "this tracker gets tried at all", not tier-promotion memory for it.
func (t *Torrent) extraTrackerTier() *trackerTier {
	p := t.extraTrackers.Load()
	if p == nil || len(*p) == 0 {
		return nil
	}
	urls := supportedAnnounceURLs(*p)
	if len(urls) == 0 {
		return nil
	}
	tier := trackerTier{urls: urls}
	return &tier
}

// buildTiers turns a metainfo announce-list into shuffled tracker tiers,
// filtered to schemes tracker.Client actually implements. Per BEP 12, the
// plain announce field is used only as a fallback single tier when
// announce-list is absent or empty (once filtered) — a client that
// understands announce-list should not also treat announce as a peer tier.
func buildTiers(announceList [][]string, announce string) []trackerTier {
	var tiers []trackerTier
	for _, tier := range announceList {
		urls := supportedAnnounceURLs(tier)
		if len(urls) == 0 {
			continue
		}
		rand.Shuffle(len(urls), func(i, j int) { urls[i], urls[j] = urls[j], urls[i] })
		tiers = append(tiers, trackerTier{urls: urls})
	}
	if len(tiers) == 0 {
		if urls := supportedAnnounceURLs([]string{announce}); len(urls) > 0 {
			tiers = append(tiers, trackerTier{urls: urls})
		}
	}
	return tiers
}

// announceTiers announces to every tier (BEP 12: redundancy across tiers,
// not just failover within one), aggregating peers from each tier that
// produced a successful reply. A tracker that answers gets promoted to the
// front of its own tier in place, so it's tried first next time this same
// tiers slice is reused.
func (t *Torrent) announceTiers(ctx context.Context, tiers []trackerTier, event tracker.Event) (*tracker.AnnounceResponse, error) {
	var aggregated *tracker.AnnounceResponse
	var lastErr error

	for i := range tiers {
		resp, workedAt, err := t.announceTier(ctx, tiers[i].urls, event)
		if err != nil {
			lastErr = err
			continue
		}
		if workedAt > 0 {
			urls := tiers[i].urls
			urls[0], urls[workedAt] = urls[workedAt], urls[0]
		}
		if aggregated == nil {
			aggregated = resp
			continue
		}
		aggregated.Peers = append(aggregated.Peers, resp.Peers...)
		if resp.Interval > 0 && (aggregated.Interval == 0 || resp.Interval < aggregated.Interval) {
			aggregated.Interval = resp.Interval // the shortest interval any tracker asked for
		}
	}

	if aggregated == nil {
		return nil, lastErr
	}
	return aggregated, nil
}

// announceTier tries each tracker in a tier in order until one succeeds,
// returning its response and its index within the tier (so the caller can
// promote it).
func (t *Torrent) announceTier(ctx context.Context, urls []string, event tracker.Event) (*tracker.AnnounceResponse, int, error) {
	var lastErr error
	for i, url := range urls {
		resp, err := t.announceOne(ctx, url, event)
		if err != nil {
			lastErr = err
			continue
		}
		return resp, i, nil
	}
	return nil, 0, lastErr
}

// announceOne sends one announce to one tracker URL under this torrent's
// primary identity, and — for a genuinely hybrid torrent (both v1 and v2
// present, see hybridV2InfoHash) — a second announce to the same URL under
// its v2 identity, merging both into one response and one recorded
// TrackerStatus. Real hybrid clients do this since a v1-only swarm and a
// v2-only swarm on the same tracker can be disjoint — cheap (one extra
// request per tracker per interval), not a protocol redesign. A pure-v2
// torrent needs no second announce: t.infoHash already IS its v2 identity
// (see torrent.New's own doc comment on identity selection).
func (t *Torrent) announceOne(ctx context.Context, url string, event tracker.Event) (*tracker.AnnounceResponse, error) {
	resp, err := t.announceOneHash(ctx, url, event, t.infoHash)

	v2Hash, hybrid := t.hybridV2InfoHash()
	if !hybrid {
		t.recordTrackerResult(url, resp, err)
		return resp, err
	}

	resp2, err2 := t.announceOneHash(ctx, url, event, v2Hash)
	merged, mergedErr := mergeAnnounceResponses(resp, err, resp2, err2)
	t.recordTrackerResult(url, merged, mergedErr)
	return merged, mergedErr
}

// hybridV2InfoHash returns this torrent's v2 identity (truncated per BEP
// 52) for a genuinely hybrid torrent — both v1 and v2 present in the same
// info dict — or false when there's nothing to dual-announce: metadata not
// known yet, a plain v1 torrent, or a pure-v2 one (already announced under
// its v2 identity as t.infoHash itself, needing no second request).
func (t *Torrent) hybridV2InfoHash() (metainfo.Hash, bool) {
	mi := t.mi.Load()
	if mi == nil || mi.MetaVersion != 2 || mi.IsPureV2() {
		return metainfo.Hash{}, false
	}
	return mi.InfoHashV2.Truncated20(), true
}

// mergeAnnounceResponses combines a hybrid torrent's two per-hash announce
// results into one: peers from both, seeder/leecher counts summed (both
// counts describe the same logical torrent, just observed under its two
// different identities), and the shorter of the two requested intervals.
// A single failure is tolerated silently — the other hash's real peers are
// still worth using — only a double failure propagates an error.
func mergeAnnounceResponses(resp *tracker.AnnounceResponse, err error, resp2 *tracker.AnnounceResponse, err2 error) (*tracker.AnnounceResponse, error) {
	switch {
	case err != nil && err2 != nil:
		return nil, err
	case err != nil:
		return resp2, nil
	case err2 != nil:
		return resp, nil
	}
	merged := *resp
	merged.Peers = append(append([]tracker.PeerInfo(nil), resp.Peers...), resp2.Peers...)
	merged.Complete += resp2.Complete
	merged.Incomplete += resp2.Incomplete
	if resp2.Interval > 0 && (merged.Interval == 0 || resp2.Interval < merged.Interval) {
		merged.Interval = resp2.Interval
	}
	if resp2.MinInterval > merged.MinInterval {
		merged.MinInterval = resp2.MinInterval
	}
	return &merged, nil
}

// announceOneHash sends one announce to one tracker URL under a specific
// infohash — factored out of announceOne so a hybrid torrent's dual
// announce can call it twice without duplicating the request-building.
func (t *Torrent) announceOneHash(ctx context.Context, url string, event tracker.Event, hash metainfo.Hash) (*tracker.AnnounceResponse, error) {
	left := int64(-1) // unknown until metadata tells us the real size
	if mi := t.mi.Load(); mi != nil {
		left = mi.TotalLength - t.downloaded.Load()
		if left < 0 {
			left = 0
		}
	}

	req := tracker.AnnounceRequest{
		InfoHash:   hash,
		PeerID:     t.cfg.OurID,
		Port:       t.cfg.ListenPort,
		Uploaded:   t.uploaded.Load(),
		Downloaded: t.downloaded.Load(),
		Left:       left,
		Compact:    true,
		Event:      event,
		NumWant:    50,
	}
	return t.trackerClient.Announce(ctx, url, req)
}

// supportedAnnounceURLs keeps only the schemes tracker.Client.Announce
// actually implements (http, https, udp — BEP 15), so an unsupported scheme
// in a torrent's announce list (or a magnet's tr=) is silently skipped
// rather than tried and failing every interval.
func supportedAnnounceURLs(all []string) []string {
	var out []string
	for _, u := range all {
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "udp://") {
			out = append(out, u)
		}
	}
	return out
}
