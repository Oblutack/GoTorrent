package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
)

// watchFolderInterval is how often StartWatchFolder re-scans the directory
// for new .torrent files. A var, not a const, so a test can shrink it
// before calling StartWatchFolder — the same pattern pexInterval already
// uses in internal/torrent.
var watchFolderInterval = 10 * time.Second

// StartWatchFolder polls dir every watchFolderInterval for .torrent files
// and Adds any this Engine doesn't already manage — drop a .torrent in,
// it gets picked up on the next scan, no restart needed. Polling rather
// than a filesystem-event API (inotify/ReadDirectoryChangesW) keeps this
// dependency-free and identical across platforms, at the cost of up to one
// interval's latency, which is a fine trade for "a new torrent file showed
// up" rather than something that needs to react instantly.
//
// A file already managed (by infohash, once parsed) is left alone — Add's
// own "already added" check is what actually prevents a duplicate, this
// just avoids the log noise of trying every scan. A file that fails to
// parse or add is logged once and left in place; it is retried every scan
// (nothing marks it as permanently rejected), so fixing the file picks it
// up on the next pass without any other action needed. Runs until ctx is
// cancelled.
func (e *Engine) StartWatchFolder(ctx context.Context, dir string) {
	go e.watchFolderLoop(ctx, dir)
}

func (e *Engine) watchFolderLoop(ctx context.Context, dir string) {
	e.watchFolderScan(dir)
	ticker := time.NewTicker(watchFolderInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.watchFolderScan(dir)
		}
	}
}

func (e *Engine) watchFolderScan(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Warning.Printf("engine: watch folder %s: %v\n", dir, err)
		return
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.EqualFold(filepath.Ext(ent.Name()), ".torrent") {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		if _, err := e.Add(path, ""); err != nil {
			if !errors.Is(err, ErrAlreadyAdded) {
				logger.Warning.Printf("engine: watch folder could not add %s: %v\n", path, err)
			}
			continue
		}
		logger.Logf("engine: watch folder added %s\n", path)
	}
}
