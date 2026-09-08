package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/ratelimit"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// MoveData relocates hash's downloaded content to newDir: stops the
// torrent (closing its storage, so no file is still open when it moves —
// required on Windows, harmless elsewhere), renames its content root
// (Torrent.ContentPath — the single file, or the whole wrapping directory
// for a multi-file torrent) into newDir, then starts a fresh Torrent under
// the new location. It picks up right where it left off: os.Rename
// preserves mtimes, and resume data is trusted by size+mtime match at
// whatever path the torrent's own Config.DownloadDir resolves to, so it
// doesn't care that the path changed, only that the bytes at the new one
// still check out.
//
// Refused for a multi-file torrent using storage.LayoutNoSubfolder, whose
// content root is the download directory itself — moving that would mean
// moving (or deleting) whatever else lives there, which might not even
// belong to this torrent. Only moves within one filesystem volume: a
// cross-device rename fails with the underlying error rather than falling
// back to a copy, which is deliberately not implemented here.
func (e *Engine) MoveData(hash metainfo.Hash, newDir string) error {
	e.mu.Lock()
	mt, ok := e.torrents[hash]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("engine: %s is not managed by this engine", hash)
	}

	mi := mt.t.Metadata()
	if mi == nil {
		return fmt.Errorf("engine: %s has no metadata yet, nothing to move", hash)
	}
	oldContentPath := mt.t.ContentPath()
	if oldContentPath == "" {
		return fmt.Errorf("engine: %s has no resolved content path", hash)
	}
	if mi.Info.IsMultiFile() && oldContentPath == mt.downloadDir {
		return fmt.Errorf("engine: %s uses a no-subfolder layout, whose content root is its whole download directory — refusing to move it automatically", hash)
	}

	// Must happen before anything on disk moves, and must not be called
	// while holding e.mu (Stop blocks on the actor's shutdown) — same
	// constraint Remove documents.
	mt.t.Stop()

	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return fmt.Errorf("engine: creating %s: %w", newDir, err)
	}
	newContentPath := filepath.Join(newDir, filepath.Base(oldContentPath))
	if err := os.Rename(oldContentPath, newContentPath); err != nil {
		return fmt.Errorf("engine: moving data: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	cfg := e.torrentConfig(newDir)
	// Every torrent gets its own rate-cap pair — see Add's identical
	// comment on why this is appended rather than left to torrentConfig.
	downLimit, upLimit := ratelimit.Unlimited(), ratelimit.Unlimited()
	cfg.DownLimit = append(cfg.DownLimit, downLimit)
	cfg.UpLimit = append(cfg.UpLimit, upLimit)

	tr, err := torrent.New(mi, cfg)
	if err != nil {
		return fmt.Errorf("engine: restarting %s under %s: %w", hash, newDir, err)
	}

	mt.t = tr
	mt.downloadDir = newDir
	mt.downLimit, mt.upLimit = downLimit, upLimit
	// completionHookFired is deliberately left as-is: a torrent that
	// already finished and had its hook fired should not have it fire
	// again just because its data moved — this isn't a new completion.

	if err := e.saveManifestLocked(); err != nil {
		return fmt.Errorf("engine: persisting manifest after move: %w", err)
	}

	tr.OnStateChange(func(s torrent.State) {
		go e.reevaluateQueue()
		go e.dispatchCompletionHook(hash, s)
	})
	go func() {
		if err := tr.Run(context.Background()); err != nil {
			logger.Error.Printf("engine: torrent %s: %v\n", hash, err)
		}
	}()
	return nil
}
