package torrent

import (
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
)

// seedRatio is Uploaded / Downloaded, falling back to the torrent's total
// length when nothing has been downloaded this session (the initial-seed
// case, where dividing by zero would otherwise make the ratio undefined
// rather than simply large). Using Downloaded rather than TotalLength once
// something has been downloaded means a torrent with skipped files (3.2) is
// judged against what was actually pulled down, not the whole torrent's size.
func seedRatio(uploaded, downloaded, totalLength int64) float64 {
	denom := downloaded
	if denom <= 0 {
		denom = totalLength
	}
	if denom <= 0 {
		return 0
	}
	return float64(uploaded) / float64(denom)
}

// currentSeedingDuration is seedingDuration plus whatever has elapsed since
// seedingStartedAt, if a seeding run is currently in progress. Both fields
// are actor-owned (touched only from setState), so this must only be called
// from the actor goroutine.
func (t *Torrent) currentSeedingDuration(now time.Time) time.Duration {
	d := t.seedingDuration
	if !t.seedingStartedAt.IsZero() {
		d += now.Sub(t.seedingStartedAt)
	}
	return d
}

// checkSeedLimits pauses the torrent once Config.SeedRatioLimit or
// Config.SeedTimeLimit is reached. Called once per tick while Seeding — see
// run.go's tick. A limit of 0 means unlimited, so a torrent with neither
// configured pays only these two cheap comparisons.
func (t *Torrent) checkSeedLimits(now time.Time) {
	if t.State() != StateSeeding {
		return
	}

	if t.cfg.SeedRatioLimit > 0 {
		var total int64
		if mi := t.mi.Load(); mi != nil {
			total = mi.TotalLength
		}
		if seedRatio(t.uploaded.Load(), t.downloaded.Load(), total) >= t.cfg.SeedRatioLimit {
			logger.Logf("torrent %s: seed ratio limit (%.2f) reached, pausing\n", t.infoHash, t.cfg.SeedRatioLimit)
			t.doPause()
			t.fireSeedLimitReached()
			return
		}
	}

	if t.cfg.SeedTimeLimit > 0 && t.currentSeedingDuration(now) >= t.cfg.SeedTimeLimit {
		logger.Logf("torrent %s: seed time limit (%s) reached, pausing\n", t.infoHash, t.cfg.SeedTimeLimit)
		t.doPause()
		t.fireSeedLimitReached()
	}
}

// fireSeedLimitReached calls the OnSeedLimitReached callback, if any — see
// its own doc comment for why this must never block or call back into t.
func (t *Torrent) fireSeedLimitReached() {
	if t.onSeedLimitReached != nil {
		t.onSeedLimitReached()
	}
}
