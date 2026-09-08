package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// waitUntil polls cond until it reports true or timeout passes, failing the
// test with msg otherwise. Used throughout this file since queue decisions
// happen asynchronously (OnStateChange's callback spawns a goroutine rather
// than acting inline — see queue.go's comment on why).
func waitUntil(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cond() {
		t.Fatal(msg)
	}
}

// countState returns how many of hashes are currently in state st.
func countState(e *Engine, hashes []metainfo.Hash, st torrent.State) int {
	n := 0
	for _, h := range hashes {
		if tr, ok := e.Get(h); ok && tr.State() == st {
			n++
		}
	}
	return n
}

// addDeadTorrents adds n torrents with dead announce URLs and no data on
// disk, so each reaches StateDownloading and sits there indefinitely (no
// tracker will ever hand back a peer) — exactly what the queue's admission
// logic needs to exercise without any real transfer. Returns their hashes
// in Add order, which is also initial queue-position order.
func addDeadTorrents(t *testing.T, e *Engine, n int) []metainfo.Hash {
	t.Helper()
	dir := t.TempDir()
	hashes := make([]metainfo.Hash, n)
	for i := 0; i < n; i++ {
		path, hash := writeTorrentFile(t, dir, fmt.Sprintf("q%d", i))
		got, err := e.Add(path, "")
		if err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
		if got != hash {
			t.Fatalf("Add returned %s, want %s", got, hash)
		}
		hashes[i] = hash
	}
	return hashes
}

func TestMaxActiveDownloadsLimitsConcurrentDownloading(t *testing.T) {
	e, err := New(t.TempDir(), Defaults{
		DownloadDir:        t.TempDir(),
		ResumeDir:          t.TempDir(),
		MaxActiveDownloads: 2,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.StartQueue(ctx)

	hashes := addDeadTorrents(t, e, 4)

	waitUntil(t, 5*time.Second, "did not settle at exactly 2 Downloading", func() bool {
		return countState(e, hashes, torrent.StateDownloading) == 2
	})
	waitUntil(t, 5*time.Second, "did not settle at exactly 2 Paused", func() bool {
		return countState(e, hashes, torrent.StatePaused) == 2
	})

	// Priority order is queue position (Add order) when nothing overrides
	// it, so the first two Added should be the two actually running.
	for i, h := range hashes {
		tr, _ := e.Get(h)
		want := torrent.StateDownloading
		if i >= 2 {
			want = torrent.StatePaused
		}
		if got := tr.State(); got != want {
			t.Fatalf("torrent %d (queue position %d): state %s, want %s", i, i, got, want)
		}
	}
}

func TestForceStartBypassesTheLimit(t *testing.T) {
	e, err := New(t.TempDir(), Defaults{
		DownloadDir:        t.TempDir(),
		ResumeDir:          t.TempDir(),
		MaxActiveDownloads: 1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.StartQueue(ctx)

	hashes := addDeadTorrents(t, e, 3)
	waitUntil(t, 5*time.Second, "did not settle at exactly 1 Downloading", func() bool {
		return countState(e, hashes, torrent.StateDownloading) == 1
	})

	// hashes[2] is last in queue order, so ordinarily it would stay Paused.
	// Force-starting it must get it running even though a lower-position
	// torrent isn't force-started.
	if err := e.SetForceStart(hashes[2], true); err != nil {
		t.Fatalf("SetForceStart: %v", err)
	}
	waitUntil(t, 5*time.Second, "force-started torrent never started running", func() bool {
		tr, _ := e.Get(hashes[2])
		return tr.State() == torrent.StateDownloading
	})

	// The limit is still 1 total (force-start bypasses the queue, it
	// doesn't lift the cap), so exactly one torrent should be running: the
	// force-started one.
	if got := countState(e, hashes, torrent.StateDownloading); got != 1 {
		t.Fatalf("%d torrents Downloading with MaxActiveDownloads=1 and one force-started, want 1", got)
	}
}

func TestSetQueuePositionReordersWhichRun(t *testing.T) {
	e, err := New(t.TempDir(), Defaults{
		DownloadDir:        t.TempDir(),
		ResumeDir:          t.TempDir(),
		MaxActiveDownloads: 1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.StartQueue(ctx)

	hashes := addDeadTorrents(t, e, 3)
	waitUntil(t, 5*time.Second, "first-added torrent never started running", func() bool {
		tr, _ := e.Get(hashes[0])
		return tr.State() == torrent.StateDownloading
	})

	// Move the last torrent to the front of the queue; it should displace
	// the one currently running.
	if err := e.SetQueuePosition(hashes[2], 0); err != nil {
		t.Fatalf("SetQueuePosition: %v", err)
	}
	waitUntil(t, 5*time.Second, "reordered torrent never took over the running slot", func() bool {
		tr, _ := e.Get(hashes[2])
		return tr.State() == torrent.StateDownloading
	})
	waitUntil(t, 5*time.Second, "originally-running torrent was never paused after losing its slot", func() bool {
		tr, _ := e.Get(hashes[0])
		return tr.State() == torrent.StatePaused
	})
}

func TestQueueLimitsDisabledByDefault(t *testing.T) {
	e := newTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.StartQueue(ctx)

	hashes := addDeadTorrents(t, e, 5)
	for _, h := range hashes {
		tr, ok := e.Get(h)
		if !ok {
			t.Fatal("Get: torrent missing right after Add")
		}
		waitForState(t, tr, torrent.StateDownloading, 5*time.Second)
	}
}
