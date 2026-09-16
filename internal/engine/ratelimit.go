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

// TorrentRateLimit is SetTorrentRateLimit's read-side counterpart — needed
// by a caller (4.2's PATCH route) that wants to change just one direction
// and has to know the other's current value first, since SetTorrentRateLimit
// always sets both at once.
func (e *Engine) TorrentRateLimit(hash metainfo.Hash) (downBytesPerSec, upBytesPerSec int64, ok bool) {
	e.mu.Lock()
	mt, ok := e.torrents[hash]
	e.mu.Unlock()
	if !ok {
		return 0, 0, false
	}
	return mt.downLimit.Limit(), mt.upLimit.Limit(), true
}

// SetGlobalRateLimit changes the fleet-wide cap every managed torrent's
// Defaults.DownLimit/UpLimit composes with, in bytes/sec (0 or negative
// means unlimited, same convention as SetTorrentRateLimit). New guarantees
// a real *ratelimit.Limiter always exists for both directions, so this
// works even on an Engine that started with no cap configured at all — a
// caller (4.2's session PATCH route) does not need to have been started
// with -down-limit/-up-limit for this to take effect.
//
// Also updates the "normal" rate StartAltSpeedSchedule restores to outside
// its alt window, so a rate set here is the new normal rather than being
// silently overwritten by the next scheduler tick — the same reasoning
// SetTorrentRateLimit doesn't need, since per-torrent caps aren't part of
// the alt-schedule's own domain at all.
func (e *Engine) SetGlobalRateLimit(downBytesPerSec, upBytesPerSec int64) {
	e.mu.Lock()
	down, up := e.defaults.DownLimit, e.defaults.UpLimit
	e.normalDownBps = downBytesPerSec
	e.normalUpBps = upBytesPerSec
	e.mu.Unlock()
	down.SetLimit(downBytesPerSec)
	up.SetLimit(upBytesPerSec)
}

// GlobalRateLimit is SetGlobalRateLimit's read-side counterpart, for the
// same partial-update reason TorrentRateLimit exists. Reads the live rate
// actually in effect right now — which, while alt-speed (altspeed.go) is
// active, is the alt rate, not the normal configured one. Use
// NormalRateLimit instead for "what did the user configure as the normal
// cap," the same distinction Defaults.AltDownLimit/AltUpLimit's own
// existence already implies.
func (e *Engine) GlobalRateLimit() (downBytesPerSec, upBytesPerSec int64) {
	e.mu.Lock()
	down, up := e.defaults.DownLimit, e.defaults.UpLimit
	e.mu.Unlock()
	return down.Limit(), up.Limit()
}

// NormalRateLimit reports the fleet-wide rate limit as configured via
// SetGlobalRateLimit (or Defaults.DownLimit/UpLimit at construction),
// independent of whatever setAltSpeed may currently have the live
// *ratelimit.Limiter temporarily set to. Callers that read back "the
// current limit" purely to redisplay or resubmit it (a preferences dialog
// PATCHing back whatever GET just returned, say) want this, not
// GlobalRateLimit — reading the live rate there would capture the alt
// rate while alt-speed happens to be on and silently write it back as the
// new "normal" cap the next time that value is submitted.
func (e *Engine) NormalRateLimit() (downBytesPerSec, upBytesPerSec int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.normalDownBps, e.normalUpBps
}
