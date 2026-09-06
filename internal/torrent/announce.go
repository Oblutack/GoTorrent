package torrent

import (
	"context"
	"strings"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// defaultAnnounceInterval is used until a tracker tells us otherwise.
const defaultAnnounceInterval = 30 * time.Minute

// announceLoop periodically announces to every HTTP(S) tracker and feeds
// discovered peers back to the actor. It runs until ctx is cancelled.
//
// The tracker list is recomputed every iteration rather than fixed at
// startup: a magnet-link torrent starts this loop with only the trackers
// supplied at construction (Config.Trackers, from the magnet's tr=
// parameters), and once metadata arrives via SetMetadata the very next
// iteration picks up mi.AnnounceURLs() instead — no second loop needed, and
// no gap where the torrent is announcing nowhere.
//
// A final "stopped"/"completed" announce is deliberately not this loop's
// job — see announceOnce — since those need their own bounded timeout
// independent of the loop's re-announce cadence.
func (t *Torrent) announceLoop(ctx context.Context, firstEvent tracker.Event) {
	defer t.wg.Done()

	event := firstEvent
	interval := defaultAnnounceInterval

	for {
		if urls := t.announceURLs(); len(urls) == 0 {
			logger.Logf("torrent %s: no HTTP(S) trackers to announce to yet\n", t.infoHash)
		} else if resp, err := t.announce(ctx, urls, event); err != nil {
			logger.Warning.Printf("torrent %s: announce failed: %v\n", t.infoHash, err)
		} else {
			if resp.Interval > 0 {
				interval = resp.Interval
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

// announceOnce sends a single announce — used for "stopped" and "completed",
// and for the plain re-announce after a forced recheck — with its own
// bounded timeout, so shutdown is never stuck waiting on a slow or dead
// tracker. A torrent with no announceable trackers yet (e.g. mid-magnet,
// with no tr= parameters) is a silent no-op.
func (t *Torrent) announceOnce(event tracker.Event, timeout time.Duration) {
	urls := t.announceURLs()
	if len(urls) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, err := t.announce(ctx, urls, event); err != nil {
		logger.Logf("torrent %s: %s announce failed: %v\n", t.infoHash, event, err)
	}
}

// announceURLs is the torrent's metadata trackers once known, or the
// trackers supplied at construction before that.
func (t *Torrent) announceURLs() []string {
	if mi := t.mi.Load(); mi != nil {
		return supportedAnnounceURLs(mi.AnnounceURLs())
	}
	return supportedAnnounceURLs(t.cfg.Trackers)
}

func (t *Torrent) announce(ctx context.Context, urls []string, event tracker.Event) (*tracker.AnnounceResponse, error) {
	left := int64(-1) // unknown until metadata tells us the real size
	if mi := t.mi.Load(); mi != nil {
		left = mi.TotalLength - t.downloaded.Load()
		if left < 0 {
			left = 0
		}
	}

	req := tracker.AnnounceRequest{
		InfoHash:   t.infoHash,
		PeerID:     t.cfg.OurID,
		Port:       t.cfg.ListenPort,
		Uploaded:   t.uploaded.Load(),
		Downloaded: t.downloaded.Load(),
		Left:       left,
		Compact:    true,
		Event:      event,
		NumWant:    50,
	}

	var lastErr error
	for _, url := range urls {
		resp, err := t.trackerClient.Announce(ctx, url, req)
		if err != nil {
			lastErr = err
			continue
		}
		return resp, nil
	}
	return nil, lastErr
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
