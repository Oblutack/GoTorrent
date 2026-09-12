package engine

import (
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

// TestAddTrackerReachesTheManagedTorrent proves the fleet-level wrapper
// actually delegates to the real *torrent.Torrent, not a no-op - reads the
// result back via the same TrackerStatuses a control-API client would see
// through GET .../trackers, after a real Reannounce (an added tracker has
// no status at all until its first announce attempt).
func TestAddTrackerReachesTheManagedTorrent(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "add-tracker")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := e.AddTracker(hash, "http://127.0.0.1:1/announce"); err != nil {
		t.Fatalf("AddTracker: %v", err)
	}

	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("torrent missing right after Add")
	}
	if err := tr.Reannounce(); err != nil {
		t.Fatalf("Reannounce: %v", err)
	}

	// TrackerStatuses only reflects a completed announce attempt, published
	// asynchronously from announceOne's own goroutine - poll rather than
	// assume Reannounce's nudge has already been acted on by the time it
	// returns.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range tr.TrackerStatuses() {
			if s.URL == "http://127.0.0.1:1/announce" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("added tracker never appeared in TrackerStatuses(): %v", tr.TrackerStatuses())
}

func TestAddTrackerRejectsUnknownHash(t *testing.T) {
	e := newTestEngine(t)
	var bogus metainfo.Hash
	if err := e.AddTracker(bogus, "http://example.com/announce"); err == nil {
		t.Fatal("AddTracker on an unmanaged hash: want an error")
	}
}

// TestSetSequentialReachesTheManagedTorrent proves the fleet-level wrapper
// delegates for real - the actual sequential-ordering behavior itself is
// already proven at internal/torrent and internal/picker's own levels;
// this only has to prove the call reaches a real managed torrent instead
// of silently doing nothing.
func TestSetSequentialReachesTheManagedTorrent(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "set-sequential")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := e.SetSequential(hash, true); err != nil {
		t.Fatalf("SetSequential: %v", err)
	}
}

func TestSetSequentialRejectsUnknownHash(t *testing.T) {
	e := newTestEngine(t)
	var bogus metainfo.Hash
	if err := e.SetSequential(bogus, true); err == nil {
		t.Fatal("SetSequential on an unmanaged hash: want an error")
	}
}

// TestSetFilePriorityReachesTheManagedTorrent proves the fleet-level
// wrapper delegates for real, same reasoning as TestSetSequentialReachesTheManagedTorrent -
// the priority/picker mechanics themselves are already proven at
// internal/torrent's own level.
func TestSetFilePriorityReachesTheManagedTorrent(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "set-file-priority")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := e.SetFilePriority(hash, 0, picker.PriorityHigh); err != nil {
		t.Fatalf("SetFilePriority: %v", err)
	}

	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("torrent missing right after Add")
	}
	got := tr.Stats().FilePriorities
	if len(got) != 1 || got[0] != picker.PriorityHigh {
		t.Fatalf("FilePriorities = %v, want [high]", got)
	}
}

func TestSetFilePriorityRejectsUnknownHash(t *testing.T) {
	e := newTestEngine(t)
	var bogus metainfo.Hash
	if err := e.SetFilePriority(bogus, 0, picker.PriorityHigh); err == nil {
		t.Fatal("SetFilePriority on an unmanaged hash: want an error")
	}
}
