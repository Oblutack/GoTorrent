package torrent

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// newPreSeededTorrent builds a real *Torrent that already has content on
// disk before it ever starts, so it verifies straight to StateSeeding
// without needing a peer — the seed side of a real two-actor transfer,
// unlike newFakeSeeder (which plays a peer on the wire but is not a real
// Torrent and so has none of SetFilePriority/SeedRatioLimit/Pause's actual
// behavior).
func newPreSeededTorrent(t *testing.T, name string, mi *metainfo.MetaInfo, content []byte, cfg Config) *Torrent {
	t.Helper()
	if cfg.DownloadDir == "" {
		cfg.DownloadDir = t.TempDir()
	}
	if cfg.ResumeDir == "" {
		cfg.ResumeDir = t.TempDir()
	}
	if err := os.WriteFile(filepath.Join(cfg.DownloadDir, name), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tr
}

// listenAndRoute opens a real loopback listener that hands every inbound
// connection to tr.AcceptPeer, mirroring the minimal version of
// engine.Engine.handleIncoming's handshake-then-route logic — just enough to
// let a second real *Torrent dial in and talk to tr over the actual wire
// protocol, without needing a whole engine.Engine in this package's tests.
func listenAndRoute(t *testing.T, tr *Torrent) tracker.PeerInfo {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				hs, err := peer.ReadHandshake(conn)
				if err != nil {
					conn.Close()
					return
				}
				tr.AcceptPeer(conn, hs)
			}()
		}
	}()

	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse listener port: %v", err)
	}
	return tracker.PeerInfo{IP: net.ParseIP("127.0.0.1"), Port: uint16(port)}
}

// TestSeedRatioLimitPausesSeedingTorrent proves the whole feature end to
// end with two real actors: seeder starts already-Seeding with a ratio
// limit configured, a real leecher downloads the entire file from it (the
// only source of the seeder's upload count), and once Uploaded/Downloaded
// crosses the configured ratio the seeder pauses itself without anything
// external telling it to.
func TestSeedRatioLimitPausesSeedingTorrent(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "ratio.bin", pieceLength, []fileSpec{{length: pieceLength * 20}})

	seederCfg := newTestConfig(t)
	seederCfg.SeedRatioLimit = 0.5
	seeder := newPreSeededTorrent(t, "ratio.bin", mi, content, seederCfg)
	runInBackground(t, seeder)
	waitForState(t, seeder, StateSeeding, 5*time.Second)
	pi := listenAndRoute(t, seeder)

	leecher, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New (leecher): %v", err)
	}
	runInBackground(t, leecher)
	leecher.DialPeer(pi)

	// The seeder is expected to pause itself partway through — once it has
	// served half the file, its own ratio crosses the 0.5 limit — which cuts
	// the leecher's connection off before a full download completes. That's
	// the behavior under test, not a race to avoid: don't wait for the
	// leecher to reach Seeding, just prove the seeder paused itself with a
	// ratio at or above the configured limit, and that real bytes were
	// actually transferred to get there (not a false positive from denom
	// falling back to TotalLength with zero real uploads).
	waitForState(t, seeder, StatePaused, 15*time.Second)

	stats := seeder.Stats()
	if stats.SeedRatio < seederCfg.SeedRatioLimit {
		t.Fatalf("seeder paused with SeedRatio %.2f, want at least the configured limit %.2f", stats.SeedRatio, seederCfg.SeedRatioLimit)
	}
	if stats.Uploaded == 0 {
		t.Fatal("seeder paused with zero bytes ever uploaded — ratio limit fired on no real traffic")
	}
	if leecher.Stats().Downloaded == 0 {
		t.Fatal("leecher never received anything from the seeder")
	}
}

// TestSeedTimeLimitPausesSeedingTorrent proves the time-based limit
// independently of any upload traffic: a torrent that reaches Seeding purely
// by verifying pre-existing content still gets paused once it has spent the
// configured amount of time in that state, with no peer involved at all.
func TestSeedTimeLimitPausesSeedingTorrent(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "time.bin", pieceLength, []fileSpec{{length: pieceLength * 3}})

	cfg := newTestConfig(t)
	cfg.SeedTimeLimit = 300 * time.Millisecond
	tr := newPreSeededTorrent(t, "time.bin", mi, content, cfg)
	runInBackground(t, tr)

	waitForState(t, tr, StateSeeding, 5*time.Second)
	waitForState(t, tr, StatePaused, 5*time.Second)

	if got := tr.Stats().SeedingDuration; got < cfg.SeedTimeLimit {
		t.Fatalf("seeder paused with SeedingDuration %s, want at least the configured limit %s", got, cfg.SeedTimeLimit)
	}
}

// TestOnSeedLimitReachedFiresAfterThePause proves the callback fires
// exactly once, and only after doPause has already taken effect — 3.4's
// remove/remove-and-delete-data actions rely on the pause (peers
// disconnected, checkpointed) having already happened by the time they run.
func TestOnSeedLimitReachedFiresAfterThePause(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "callback.bin", pieceLength, []fileSpec{{length: pieceLength * 3}})

	cfg := newTestConfig(t)
	cfg.SeedTimeLimit = 200 * time.Millisecond
	tr := newPreSeededTorrent(t, "callback.bin", mi, content, cfg)

	var mu sync.Mutex
	fired := 0
	var stateWhenFired State
	tr.OnSeedLimitReached(func() {
		mu.Lock()
		fired++
		stateWhenFired = tr.State()
		mu.Unlock()
	})
	runInBackground(t, tr)

	waitForState(t, tr, StateSeeding, 5*time.Second)
	waitForState(t, tr, StatePaused, 5*time.Second)

	// The callback runs asynchronously with no ordering guarantee relative
	// to the caller observing StatePaused, so poll rather than assume it
	// already ran the instant State() reports Paused.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := fired
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if fired != 1 {
		t.Fatalf("OnSeedLimitReached fired %d times, want exactly 1", fired)
	}
	if stateWhenFired != StatePaused {
		t.Fatalf("OnSeedLimitReached observed state %s, want it to fire after StatePaused", stateWhenFired)
	}
}

// TestSeedLimitsDisabledByDefault proves a zero-value Config (the default,
// used by every test that isn't specifically exercising this feature) never
// pauses a torrent on its own — SeedRatioLimit/SeedTimeLimit being 0 must
// mean unlimited, not "pause immediately".
func TestSeedLimitsDisabledByDefault(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "unlimited.bin", pieceLength, []fileSpec{{length: pieceLength * 2}})

	tr := newPreSeededTorrent(t, "unlimited.bin", mi, content, newTestConfig(t))
	runInBackground(t, tr)
	waitForState(t, tr, StateSeeding, 5*time.Second)

	time.Sleep(300 * time.Millisecond)
	if got := tr.State(); got != StateSeeding {
		t.Fatalf("torrent with no seed limits configured left Seeding on its own, now %s", got)
	}
}
