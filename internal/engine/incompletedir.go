package engine

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/ratelimit"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// wireTorrentHooks attaches every hook a managed torrent needs for its
// whole life — OnStateChange (queue re-evaluation, the on-complete shell
// hook, the completed-at timestamp, fleet events, cross-torrent dedupe, and
// incomplete-directory staging), OnSeedLimitReached, and the three peer/
// piece event hooks. Shared by AddWithOptions and by anything that restarts
// a *torrent.Torrent under a fresh Config for an already-managed hash
// (MoveData, maybeMoveFromIncompleteDir below) — MoveData previously only
// rewired a subset (OnStateChange's queue/completion-hook pair and
// OnSeedLimitReached), which meant a torrent moved via MoveData silently
// stopped reporting peer connect/disconnect, per-piece verification, and
// (since recordCompletedAt is part of the same callback) its completed-at
// timestamp for the rest of its life — a real, if minor, gap this
// generalization also fixes, not just new code for incomplete-dir staging.
//
// Every hook here must not block or call back synchronously into tr itself
// — OnStateChange/OnSeedLimitReached fire from the actor's own goroutine,
// and calling something like Pause/Stop directly from inside one would
// deadlock the actor's control channel. Anything that might do that runs in
// its own detached goroutine (go e.xxx(...)); anything that only touches
// eventMu/dedupeMu and non-blocking channel sends is safe to call directly.
func (e *Engine) wireTorrentHooks(hash metainfo.Hash, tr *torrent.Torrent) {
	tr.OnStateChange(func(s torrent.State) {
		go e.reevaluateQueue()
		go e.dispatchCompletionHook(hash, s)
		go e.recordCompletedAt(hash, s)
		go e.maybeMoveFromIncompleteDir(hash, s)
		// broadcast only touches eventMu and non-blocking channel sends —
		// it never calls back into tr, so unlike the goroutines above it
		// needs no go of its own to stay safe on the actor goroutine.
		e.broadcast(Event{Kind: EventTorrentStateChanged, InfoHash: hash, State: s})
		// Cross-torrent dedupe (Phase 8). Two separate passes, both needed:
		// applyDedupe *consumes* the fleet-wide index (only meaningful once
		// there's something missing to want, i.e. Downloading) — calls back
		// into tr (ApplyExternalPiece), so it needs the same
		// detached-goroutine treatment as reevaluateQueue/
		// dispatchCompletionHook above. publishAllDedupeSources *produces*
		// into it: OnPieceVerified below already records each piece as it
		// downloads, but a torrent that was already complete when Added
		// (bulk-verified) or resumed from trusted resume data never sends a
		// single piece through that per-piece hook — both paths call
		// t.pick.SetHave directly — so without this bulk publish here, on
		// reaching Downloading or Seeding, that torrent's pieces would never
		// become available to any other torrent's dedupe lookups at all,
		// defeating the single most common real case for this feature (an
		// already-seeded file, re-added under a second torrent).
		if s == torrent.StateDownloading {
			go e.applyDedupe(hash)
		}
		if s == torrent.StateDownloading || s == torrent.StateSeeding {
			go e.publishAllDedupeSources(hash)
		}
	})
	// Same constraint as OnStateChange above — must not block or call back
	// into tr synchronously, since it fires from the actor's own tick
	// goroutine. A no-op when SeedLimitAction is the default (Pause): the
	// pause itself already happened inside the actor before this fires.
	tr.OnSeedLimitReached(func() { go e.applySeedLimitAction(hash) })
	tr.OnPeerConnected(func(addr string) {
		e.broadcast(Event{Kind: EventPeerConnected, InfoHash: hash, PeerAddr: addr})
	})
	tr.OnPeerDisconnected(func(addr string) {
		e.broadcast(Event{Kind: EventPeerDisconnected, InfoHash: hash, PeerAddr: addr})
	})
	tr.OnPieceVerified(func(index int, peerAddr string) {
		e.broadcast(Event{Kind: EventPieceVerified, InfoHash: hash, PieceIndex: index, PeerAddr: peerAddr})
		e.recordDedupeSource(hash, index)
	})
}

// maybeMoveFromIncompleteDir is what actually implements Defaults.
// IncompleteDir's "move to the real directory on completion" half — a
// no-op unless hash is currently staged (managedTorrent.incompleteDir
// non-empty) and s is StateSeeding, i.e. exactly the transition that first
// means "this torrent is done." Reuses MoveData's own proven stop-rename-
// restart mechanism (a fresh *torrent.Torrent under the new Config.
// DownloadDir, trusting resume data by size+mtime match at the new path —
// os.Rename preserves mtimes, so it verifies instantly) rather than a
// second copy of that logic.
//
// Clears managedTorrent.incompleteDir *before* restarting the torrent,
// which is what makes this self-guarding against firing twice: the fresh
// torrent's own startup will reach StateSeeding again on its own (trusted
// resume data verifies immediately), re-entering this same function, but
// by then incompleteDir is already "", so the guard below skips it. This
// is also why dispatchCompletionHook/recordCompletedAt check
// managedTorrent.incompleteDir themselves and skip firing on *this*
// (pre-move) transition — %F/the completed-at moment must reflect the
// torrent's real, final location, not the soon-to-be-renamed-away staging
// one, and firing an on-complete shell command against a path that's
// mid-rename (or about to vanish) would be a real correctness bug, not
// just an odd cosmetic one. They fire for real on the second (post-move)
// transition instead, once incompleteDir has already been cleared.
func (e *Engine) maybeMoveFromIncompleteDir(hash metainfo.Hash, s torrent.State) {
	if s != torrent.StateSeeding {
		return
	}

	e.mu.Lock()
	mt, ok := e.torrents[hash]
	if !ok || mt.incompleteDir == "" {
		e.mu.Unlock()
		return
	}
	stagingDir := mt.incompleteDir
	finalDir := mt.downloadDir
	e.mu.Unlock()

	mi := mt.t.Metadata()
	if mi == nil {
		// Can't happen in practice (Seeding implies metadata is known),
		// but there is nothing sane to do here if it somehow did.
		return
	}
	oldContentPath := mt.t.ContentPath()
	if oldContentPath == "" {
		logger.Warning.Printf("engine: %s reached Seeding while staged, but has no resolved content path - leaving it in %s\n", hash, stagingDir)
		return
	}
	// Same refusal MoveData already established, generalized to compare
	// against the staging directory rather than a hardcoded downloadDir:
	// a multi-file torrent using storage.LayoutNoSubfolder has no content
	// root of its own to move - its "root" is the whole staging
	// directory, which might hold other torrents' data too. Rather than
	// silently leaving the torrent stuck mid-stage forever with no
	// explanation, this is surfaced once, loudly, as a warning - the
	// torrent keeps seeding correctly from the staging directory, it
	// just never relocates on its own.
	if mi.Info.IsMultiFile() && oldContentPath == stagingDir {
		logger.Warning.Printf("engine: %s uses a no-subfolder layout inside the incomplete directory, whose content root is the whole staging directory - refusing to move it automatically; it will keep seeding from %s\n", hash, stagingDir)
		return
	}

	mt.t.Stop()

	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		logger.Warning.Printf("engine: %s: creating final directory %s: %v\n", hash, finalDir, err)
		return
	}
	newContentPath := filepath.Join(finalDir, filepath.Base(oldContentPath))
	if err := os.Rename(oldContentPath, newContentPath); err != nil {
		logger.Warning.Printf("engine: %s: moving data out of the incomplete directory: %v\n", hash, err)
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	// The torrent may have been Removed while the rename above was
	// running (Stop already made it safe to do so) - nothing left to
	// restart if so.
	mt, ok = e.torrents[hash]
	if !ok {
		return
	}

	cfg := e.torrentConfig(finalDir)
	downLimit, upLimit := ratelimit.Unlimited(), ratelimit.Unlimited()
	cfg.DownLimit = append(cfg.DownLimit, downLimit)
	cfg.UpLimit = append(cfg.UpLimit, upLimit)

	tr, err := torrent.New(mi, cfg)
	if err != nil {
		logger.Warning.Printf("engine: %s: restarting under its final directory %s: %v\n", hash, finalDir, err)
		return
	}

	mt.t = tr
	mt.incompleteDir = ""
	mt.downLimit, mt.upLimit = downLimit, upLimit

	if err := e.saveManifestLocked(); err != nil {
		logger.Warning.Printf("engine: %s: persisting manifest after leaving the incomplete directory: %v\n", hash, err)
	}

	e.wireTorrentHooks(hash, tr)
	go func() {
		if err := tr.Run(context.Background()); err != nil {
			logger.Error.Printf("engine: torrent %s: %v\n", hash, err)
		}
	}()
}
