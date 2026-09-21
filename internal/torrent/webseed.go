package torrent

import (
	"context"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/webseed"
)

// webSeedPollInterval bounds how often webSeedLoop checks back after
// finding nothing to fetch (every piece already have, or metadata not yet
// known) and how long it waits before retrying a failed fetch — a web seed
// is an HTTP server, not a peer this torrent maintains an open connection
// to, so there is no event to react to instead of polling; this is
// deliberately gentle rather than hammering a host that might be rate
// limiting or temporarily down.
const webSeedPollInterval = 2 * time.Second

// webSeedLoop repeatedly finds this torrent's lowest-index still-missing
// piece and fetches it from one BEP 19 web seed URL, applying each
// successful fetch through the exact same write-then-verify path a real
// network download or a cross-torrent dedupe match already uses —
// ApplyExternalPiece. Runs entirely off the actor goroutine (real HTTP I/O),
// mirroring announceLoop/dhtLoop's own shape; one of these runs per
// Config.WebSeeds entry, all sharing one cancelable context (see
// restartWebSeedLoops).
//
// Deliberately does not coordinate with real peer connections or other web
// seed loops about which piece to fetch next — a race where two sources
// fetch the same piece is possible and wasteful of bandwidth, but never
// wrong: ApplyExternalPiece already treats an already-have piece as a
// harmless no-op, the same tolerance that lets endgame duplicate requests
// and this coexist without any new reservation bookkeeping.
func (t *Torrent) webSeedLoop(ctx context.Context, url string) {
	defer t.wg.Done()
	client := webseed.New(url, nil)

	wait := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(webSeedPollInterval):
			return true
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		mi := t.mi.Load()
		if mi == nil || mi.Info.IsMultiFile() {
			// No metadata yet (a magnet still fetching it — worth
			// rechecking, since metadata can arrive later) or a multi-file
			// torrent (internal/webseed's own v1 scope boundary — this can
			// never resolve, but rechecking costs nothing and keeps this
			// loop from needing its own separate "give up for good" exit).
			if !wait() {
				return
			}
			continue
		}

		index, ok := t.nextMissingPieceForWebSeed(mi)
		if !ok {
			if !wait() {
				return
			}
			continue
		}

		data, err := client.FetchPiece(ctx, mi, index)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Warning.Printf("torrent %s: web seed %s: %v\n", t.infoHash, url, err)
			if !wait() {
				return
			}
			continue
		}
		if err := t.ApplyExternalPiece(index, data); err != nil {
			logger.Warning.Printf("torrent %s: web seed %s: applying piece %d: %v\n", t.infoHash, url, index, err)
		}
	}
}

// nextMissingPieceForWebSeed returns this torrent's lowest-index piece not
// yet verified, or false if there is none — the simplest possible piece
// selection for a web seed, deliberately: HaveBitfield is safe to call from
// any goroutine, so this needs no actor round trip, and a web seed's real
// value is completing the pieces nothing else has gotten to yet, not
// competing with the picker's own rarest-first/sequential reasoning over
// real BitTorrent peers.
func (t *Torrent) nextMissingPieceForWebSeed(mi *metainfo.MetaInfo) (int, bool) {
	have := t.HaveBitfield()
	for i := 0; i < mi.NumPieces(); i++ {
		if !have.Has(i) {
			return i, true
		}
	}
	return 0, false
}

// restartWebSeedLoops stops whatever web seed loops are currently running
// (if any) and, if this torrent has any Config.WebSeeds configured, starts
// a fresh one per URL — the web seed counterpart to restartDHTLoop, called
// from the same places (Run's setup, doResume, doRecheck) so every
// peer-discovery-or-equivalent loop this torrent runs starts and stops
// together.
func (t *Torrent) restartWebSeedLoops() {
	t.stopWebSeedLoops()
	if len(t.cfg.WebSeeds) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(t.ctx)
	t.webSeedCancel = cancel
	for _, url := range t.cfg.WebSeeds {
		t.wg.Add(1)
		go t.webSeedLoop(ctx, url)
	}
}

// stopWebSeedLoops cancels every running web seed loop, if any.
func (t *Torrent) stopWebSeedLoops() {
	if t.webSeedCancel != nil {
		t.webSeedCancel()
		t.webSeedCancel = nil
	}
}
