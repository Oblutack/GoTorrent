package engine

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// queueReconcileInterval is StartQueue's periodic safety-net pass. The
// reactive path (Torrent.OnStateChange, wired in Add) already calls
// reevaluateQueue on every relevant transition, so this is a backstop
// against anything that reactive path might miss, not the primary
// mechanism — it can afford to be infrequent.
const queueReconcileInterval = 5 * time.Second

// StartQueue begins the periodic safety-net reconciliation pass for
// Defaults.MaxActiveDownloads/MaxActiveSeeds/MaxActiveTotal. It is optional:
// the reactive path (every Add'd torrent's OnStateChange callback) already
// enforces the limits on every state transition, and reevaluateQueue itself
// is a cheap no-op the instant all three limits are 0 (unlimited) — so
// skipping this call just means losing the backstop, not the feature.
// Like StartDHT/StartPortMapping/StartLSD, it runs until ctx is cancelled;
// unlike them it holds no OS resource, so there is nothing for Shutdown to
// explicitly close.
func (e *Engine) StartQueue(ctx context.Context) {
	go e.queueLoop(ctx)
}

func (e *Engine) queueLoop(ctx context.Context) {
	ticker := time.NewTicker(queueReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.reevaluateQueue()
		}
	}
}

// SetQueuePosition moves hash to position pos among the fleet (0 = highest
// priority, started first), shifting every torrent between its old and new
// position by one. pos is clamped into range rather than rejected — asking
// for the top or bottom of a fleet whose size you don't know exactly is a
// normal thing to want, so 0 and "anything large" both do the obvious
// thing. Reconciles the queue immediately rather than waiting for the next
// safety-net pass.
func (e *Engine) SetQueuePosition(hash metainfo.Hash, pos int) error {
	e.mu.Lock()
	if _, ok := e.torrents[hash]; !ok {
		e.mu.Unlock()
		return fmt.Errorf("engine: %s is not managed by this engine", hash)
	}

	type ordered struct {
		hash metainfo.Hash
		mt   *managedTorrent
	}
	entries := make([]ordered, 0, len(e.torrents))
	for h, mt := range e.torrents {
		entries = append(entries, ordered{h, mt})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mt.queuePos < entries[j].mt.queuePos })

	idx := -1
	for i, en := range entries {
		if en.hash == hash {
			idx = i
			break
		}
	}
	moved := entries[idx]
	entries = append(entries[:idx], entries[idx+1:]...)

	if pos < 0 {
		pos = 0
	}
	if pos > len(entries) {
		pos = len(entries)
	}
	entries = append(entries[:pos], append([]ordered{moved}, entries[pos:]...)...)

	for i, en := range entries {
		en.mt.queuePos = i
	}
	e.mu.Unlock()

	e.reevaluateQueue()
	return nil
}

// SetForceStart marks hash as bypassing MaxActiveDownloads/MaxActiveSeeds/
// MaxActiveTotal entirely: the queue always tries to keep a force-started
// torrent running, ahead of every non-force-started one, regardless of
// queue position. Reconciles the queue immediately.
func (e *Engine) SetForceStart(hash metainfo.Hash, force bool) error {
	e.mu.Lock()
	mt, ok := e.torrents[hash]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("engine: %s is not managed by this engine", hash)
	}
	mt.forceStart = force
	e.mu.Unlock()

	e.reevaluateQueue()
	return nil
}

// queueCandidate is reevaluateQueue's working view of one managed torrent,
// snapshotted so the (blocking) Pause/Resume calls that follow never happen
// while holding e.mu — see Remove's own comment on why that matters.
type queueCandidate struct {
	hash       metainfo.Hash
	t          *torrent.Torrent
	queuePos   int
	forceStart bool
	paused     bool
}

// reevaluateQueue applies Defaults.MaxActiveDownloads/MaxActiveSeeds/
// MaxActiveTotal: it pauses the lowest-priority torrents once a limit is
// exceeded and resumes queue-held ones once a slot is free again. Priority
// order is force-started first, then ascending queue position.
//
// It only ever acts on a torrent that is either currently active
// (Downloading/Seeding) or Paused specifically because the queue itself
// paused it (managedTorrent.queueHeld) — a torrent Paused for any other
// reason (a direct Torrent.Pause via Get, or 3.4's seed-limit auto-pause)
// is left alone, since a bare Paused state cannot say why. The one known
// gap this leaves, documented rather than solved: if a user pauses a
// torrent that the queue already happens to be holding back, that
// distinction is lost — it can get swept up and resumed again once a slot
// frees, indistinguishable from a torrent the queue itself is holding. This
// is the same class of simplification as internal/torrent's documented
// ones (e.g. Cancel being a broadcast, not targeted).
func (e *Engine) reevaluateQueue() {
	e.mu.Lock()
	maxDown, maxSeed, maxTotal := e.defaults.MaxActiveDownloads, e.defaults.MaxActiveSeeds, e.defaults.MaxActiveTotal
	if maxDown == 0 && maxSeed == 0 && maxTotal == 0 {
		e.mu.Unlock()
		return // unlimited: skip the Stats() round trips below entirely
	}
	// entry captures every managedTorrent-owned field this pass needs while
	// e.mu is still held - queuePos/forceStart/queueHeld are plain fields
	// mutated under e.mu by SetQueuePosition/SetForceStart/setQueueHeld from
	// other goroutines, so reading them off mt after unlocking (the previous
	// shape of this snapshot) was an unsynchronized read racing those
	// writers. Only *torrent.Torrent itself is safe to keep as a live
	// pointer here, since its own State()/Stats() are already safe to call
	// from any goroutine.
	type entry struct {
		hash       metainfo.Hash
		t          *torrent.Torrent
		queuePos   int
		forceStart bool
		queueHeld  bool
	}
	entries := make([]entry, 0, len(e.torrents))
	for hash, mt := range e.torrents {
		entries = append(entries, entry{hash: hash, t: mt.t, queuePos: mt.queuePos, forceStart: mt.forceStart, queueHeld: mt.queueHeld})
	}
	e.mu.Unlock()

	var downloading, seeding []queueCandidate
	for _, en := range entries {
		st := en.t.State()
		if st == torrent.StatePaused && !en.queueHeld {
			continue
		}
		if st != torrent.StateDownloading && st != torrent.StateSeeding && st != torrent.StatePaused {
			continue // FetchingMetadata/CheckingFiles/Error: not ours to act on
		}
		stats := en.t.Stats()
		c := queueCandidate{hash: en.hash, t: en.t, queuePos: en.queuePos, forceStart: en.forceStart, paused: st == torrent.StatePaused}
		if stats.Left == 0 {
			seeding = append(seeding, c)
		} else {
			downloading = append(downloading, c)
		}
	}

	sortByPriority(downloading)
	sortByPriority(seeding)

	keepDownload := selectAllowed(downloading, maxDown)
	keepSeed := selectAllowed(seeding, maxSeed)
	if maxTotal > 0 {
		keepDownload, keepSeed = capTotal(keepDownload, keepSeed, maxTotal)
	}

	keep := make(map[metainfo.Hash]bool, len(keepDownload)+len(keepSeed))
	for _, c := range keepDownload {
		keep[c.hash] = true
	}
	for _, c := range keepSeed {
		keep[c.hash] = true
	}

	all := append(append([]queueCandidate{}, downloading...), seeding...)
	for _, c := range all {
		switch want := keep[c.hash]; {
		case want && c.paused:
			if err := c.t.Resume(); err != nil {
				logger.Warning.Printf("engine: queue resume %s: %v\n", c.hash, err)
				continue
			}
			e.setQueueHeld(c.hash, false)
		case !want && !c.paused:
			if err := c.t.Pause(); err != nil {
				logger.Warning.Printf("engine: queue pause %s: %v\n", c.hash, err)
				continue
			}
			e.setQueueHeld(c.hash, true)
		}
	}
}

func (e *Engine) setQueueHeld(hash metainfo.Hash, held bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if mt, ok := e.torrents[hash]; ok {
		mt.queueHeld = held
	}
}

// sortByPriority orders candidates force-started-first, then by ascending
// queue position — the order reevaluateQueue keeps when a limit is tighter
// than the candidate list.
func sortByPriority(cs []queueCandidate) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].forceStart != cs[j].forceStart {
			return cs[i].forceStart
		}
		return cs[i].queuePos < cs[j].queuePos
	})
}

// selectAllowed returns the top max candidates from an already
// priority-sorted list, or every candidate if max is 0 (unlimited).
func selectAllowed(cs []queueCandidate, max int) []queueCandidate {
	if max <= 0 || max >= len(cs) {
		return cs
	}
	return cs[:max]
}

// capTotal trims the combined downloading+seeding "should run" sets down to
// maxTotal, re-sorting across both kinds by the same priority order so a
// force-started or high-priority seed can still bump a low-priority
// download (and vice versa) rather than each kind's own cap being treated
// as a separate budget.
func capTotal(keepDownload, keepSeed []queueCandidate, maxTotal int) ([]queueCandidate, []queueCandidate) {
	combined := append(append([]queueCandidate{}, keepDownload...), keepSeed...)
	sortByPriority(combined)
	if len(combined) > maxTotal {
		combined = combined[:maxTotal]
	}
	allowed := make(map[metainfo.Hash]bool, len(combined))
	for _, c := range combined {
		allowed[c.hash] = true
	}
	var newDown, newSeed []queueCandidate
	for _, c := range keepDownload {
		if allowed[c.hash] {
			newDown = append(newDown, c)
		}
	}
	for _, c := range keepSeed {
		if allowed[c.hash] {
			newSeed = append(newSeed, c)
		}
	}
	return newDown, newSeed
}
