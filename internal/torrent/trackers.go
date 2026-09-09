package torrent

import (
	"sort"
	"time"

	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// TrackerStatus is one tracker's most recent announce result, for a caller
// (4.2's GET .../trackers route) that wants per-tracker visibility rather
// than just the aggregated peer list every successful announce already
// feeds into the swarm.
type TrackerStatus struct {
	URL          string
	LastAnnounce time.Time
	// LastError is empty if the most recent announce to this URL
	// succeeded.
	LastError string
	Seeders   int
	Leechers  int
}

// recordTrackerResult publishes url's outcome. Called from announceOne,
// which runs on announceLoop's own spawned goroutine, not the actor's — so
// this uses the same atomic swap-the-whole-map publish pattern as
// extraTrackers/haveSnapshot rather than going through the actor at all,
// since a tracker announce result isn't actor state anything else needs to
// react to synchronously.
func (t *Torrent) recordTrackerResult(url string, resp *tracker.AnnounceResponse, err error) {
	var current map[string]TrackerStatus
	if p := t.trackerStatus.Load(); p != nil {
		current = *p
	}
	updated := make(map[string]TrackerStatus, len(current)+1)
	for k, v := range current {
		updated[k] = v
	}

	status := TrackerStatus{URL: url, LastAnnounce: time.Now()}
	if err != nil {
		status.LastError = err.Error()
	} else if resp != nil {
		status.Seeders = resp.Complete
		status.Leechers = resp.Incomplete
	}
	updated[url] = status
	t.trackerStatus.Store(&updated)
}

// TrackerStatuses returns every tracker this torrent has ever announced to
// in this process run, sorted by URL for a stable listing. Safe to call
// from any goroutine at any time — never blocks on the actor, unlike
// Peers/Stats, since the underlying state was never actor-owned to begin
// with.
func (t *Torrent) TrackerStatuses() []TrackerStatus {
	p := t.trackerStatus.Load()
	if p == nil {
		return nil
	}
	out := make([]TrackerStatus, 0, len(*p))
	for _, v := range *p {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out
}
