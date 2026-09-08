package engine

import (
	"fmt"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// SetTorrentRateLimit sets hash's own download/upload rate cap, in
// bytes/sec — 0 or negative means unlimited on that direction, matching
// ratelimit.New's own convention. This is on top of, not instead of,
// Defaults.DownLimit/UpLimit: the two compose (see torrent.Config's own
// doc comment), so a per-torrent cap can only ever make one torrent slower
// than the fleet-wide rate, never faster than it.
//
// Takes effect immediately for every connection this torrent already has
// open — the underlying *ratelimit.Limiter is mutable and shared by every
// one of them, so this needs no round trip through the torrent actor.
func (e *Engine) SetTorrentRateLimit(hash metainfo.Hash, downBytesPerSec, upBytesPerSec int64) error {
	e.mu.Lock()
	mt, ok := e.torrents[hash]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("engine: %s is not managed by this engine", hash)
	}
	mt.downLimit.SetLimit(downBytesPerSec)
	mt.upLimit.SetLimit(upBytesPerSec)
	return nil
}
