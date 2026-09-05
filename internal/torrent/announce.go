package torrent

import (
	"context"
	"strings"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// defaultAnnounceInterval is used until a tracker tells us otherwise.
const defaultAnnounceInterval = 30 * time.Minute

// announceLoop periodically announces to every HTTP(S) tracker in the
// torrent's announce list and feeds discovered peers back to the actor. It
// runs until ctx is cancelled. A final "stopped"/"completed" announce is
// deliberately not this loop's job — see announceOnce — since those need
// their own bounded timeout independent of the loop's re-announce cadence.
func (t *Torrent) announceLoop(ctx context.Context, firstEvent tracker.Event) {
	defer t.wg.Done()

	mi := t.mi.Load()
	urls := httpAnnounceURLs(mi.AnnounceURLs())
	if len(urls) == 0 {
		logger.Warning.Printf("torrent %s: no HTTP(S) trackers, cannot discover peers\n", t.infoHash)
		return
	}

	event := firstEvent
	interval := defaultAnnounceInterval

	for {
		resp, err := t.announce(ctx, mi, urls, event)
		event = tracker.EventNone
		if err != nil {
			logger.Warning.Printf("torrent %s: announce failed: %v\n", t.infoHash, err)
		} else {
			if resp.Interval > 0 {
				interval = resp.Interval
			}
			t.sendEvent(ctx, eventTrackerPeers{peers: resp.Peers})
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// announceOnce sends a single announce — used for "stopped" and "completed",
// and for the plain re-announce after a forced recheck — with its own
// bounded timeout, so shutdown is never stuck waiting on a slow or dead
// tracker.
func (t *Torrent) announceOnce(mi *metainfo.MetaInfo, event tracker.Event, timeout time.Duration) {
	urls := httpAnnounceURLs(mi.AnnounceURLs())
	if len(urls) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, err := t.announce(ctx, mi, urls, event); err != nil {
		logger.Logf("torrent %s: %s announce failed: %v\n", t.infoHash, event, err)
	}
}

func (t *Torrent) announce(ctx context.Context, mi *metainfo.MetaInfo, urls []string, event tracker.Event) (*tracker.AnnounceResponse, error) {
	req := tracker.AnnounceRequest{
		InfoHash:   t.infoHash,
		PeerID:     t.cfg.OurID,
		Port:       t.cfg.ListenPort,
		Uploaded:   t.uploaded.Load(),
		Downloaded: t.downloaded.Load(),
		Left:       mi.TotalLength - t.downloaded.Load(),
		Compact:    true,
		Event:      event,
		NumWant:    50,
	}
	if req.Left < 0 {
		req.Left = 0
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

func httpAnnounceURLs(all []string) []string {
	var out []string
	for _, u := range all {
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			out = append(out, u)
		}
	}
	return out
}
