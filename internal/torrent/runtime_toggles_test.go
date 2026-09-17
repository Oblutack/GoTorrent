package torrent

import (
	"sync"
	"testing"
	"time"
)

// TestSetSuperSeedingRuntimeEnableSwitchesInitialStateToSinglePieceHave
// proves the runtime PATCH toggle (Stage 5), not just Config.SuperSeeding at
// construction (already covered by superseed_test.go), actually reaches the
// actor: a torrent that reached Seeding as an ordinary seed still hands a
// freshly-dialed connection a normal HaveAll-shaped bitfield until
// SetSuperSeeding(true) is called, and a single targeted Have afterward.
func TestSetSuperSeedingRuntimeEnableSwitchesInitialStateToSinglePieceHave(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "superseed-runtime.bin", pieceLength, []fileSpec{{length: pieceLength * 4}})

	seeder := newPreSeededTorrent(t, "superseed-runtime.bin", mi, content, newTestConfig(t))
	runInBackground(t, seeder)
	waitForState(t, seeder, StateSeeding, 5*time.Second)

	if err := seeder.SetSuperSeeding(true); err != nil {
		t.Fatalf("SetSuperSeeding(true): %v", err)
	}

	pi := listenAndRoute(t, seeder)
	conn := dialRawSuperSeedPeer(t, pi.Addr(), mi.InfoHash)

	// superSeedAssign round-robins from index 0, so the very first connection
	// dialed in after enabling gets piece 0 as its single advertised Have.
	if got := readNextHave(t, conn); got != 0 {
		t.Fatalf("initial super-seed Have after runtime enable = %d, want 0", got)
	}
}

// TestSetSuperSeedingRuntimeDisableGraduatesImmediately proves turning
// super-seeding off early (before every piece has naturally been released to
// the swarm) reaches graduateSuperSeeding — the same full-Have-sweep path
// natural graduation uses — rather than leaving a connected peer stuck
// believing it only has one piece.
func TestSetSuperSeedingRuntimeDisableGraduatesImmediately(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "superseed-disable.bin", pieceLength, []fileSpec{{length: pieceLength * 4}})

	cfg := newTestConfig(t)
	cfg.SuperSeeding = true
	seeder := newPreSeededTorrent(t, "superseed-disable.bin", mi, content, cfg)
	runInBackground(t, seeder)
	waitForState(t, seeder, StateSeeding, 5*time.Second)

	pi := listenAndRoute(t, seeder)
	conn := dialRawSuperSeedPeer(t, pi.Addr(), mi.InfoHash)

	// Confirm we're really in the single-piece-at-a-time regime first.
	if got := readNextHave(t, conn); got != 0 {
		t.Fatalf("initial super-seed Have = %d, want 0", got)
	}

	if err := seeder.SetSuperSeeding(false); err != nil {
		t.Fatalf("SetSuperSeeding(false): %v", err)
	}

	// Disabling early sweeps every piece to this already-connected peer in
	// index order, exactly like natural graduation does.
	for want := uint32(0); want < 4; want++ {
		if got := readNextHave(t, conn); got != want {
			t.Fatalf("disable-graduation sweep: got Have(%d), want Have(%d)", got, want)
		}
	}
}

// TestSetSuperSeedingBeforeMetadataDoesNotPanic guards against the exact
// crash class doSetSequential/doSetFilePriority were already found
// vulnerable to (see their own doc comments): a control-channel call
// reaching a nil t.pick before metadata is known. doSetSuperSeeding is
// deliberately structured so superSeeding()'s own State()==StateSeeding
// check means there's no nil-pick path to reach — this pins that down
// rather than trusting the reasoning in the comment to stay true forever.
func TestSetSuperSeedingBeforeMetadataDoesNotPanic(t *testing.T) {
	mi, _ := buildTorrent(t, "magnet-superseed.bin", 16384, []fileSpec{{length: 16384 * 4}})

	tr, err := NewFromInfoHash(mi.InfoHash, newTestConfig(t))
	if err != nil {
		t.Fatalf("NewFromInfoHash: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateFetchingMetadata, 2*time.Second)

	if err := tr.SetSuperSeeding(true); err != nil {
		t.Fatalf("SetSuperSeeding(true) before metadata: want no error (no-op), got %v", err)
	}
}

// TestSetFirstLastPieceFirstRuntimeBoostsFirstAndLastPiece proves the
// runtime toggle actually reaches the picker: a leecher started with no
// FirstLastPieceFirst boost configured, told to enable it at runtime right
// after dialing, downloads its first and last piece before any of the
// interior ones — internal/picker's own tests already prove a High-priority
// tier is served before Normal; this only has to prove the runtime call
// really recomputes and applies that tiering.
func TestSetFirstLastPieceFirstRuntimeBoostsFirstAndLastPiece(t *testing.T) {
	mi, content := buildTorrent(t, "boost-runtime", 16384, []fileSpec{
		{path: []string{"data.bin"}, length: 16384 * 5},
	})
	seeder := newThrottledFakeSeeder(t, mi, content, 50*time.Millisecond)

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var mu sync.Mutex
	var order []int
	tr.OnPieceVerified(func(index int, peerAddr string) {
		mu.Lock()
		order = append(order, index)
		mu.Unlock()
	})

	runInBackground(t, tr)
	if err := tr.SetFirstLastPieceFirst(true); err != nil {
		t.Fatalf("SetFirstLastPieceFirst(true): %v", err)
	}
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 5*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 {
		t.Fatalf("verified %d pieces, want at least 2 to check boost ordering", len(order))
	}
	first := map[int]bool{order[0]: true, order[1]: true}
	if !first[0] || !first[4] {
		t.Fatalf("first two verified pieces = %v, want {0, 4} (first/last piece boost didn't take effect)", order[:2])
	}
}

// TestSetFirstLastPieceFirstBeforeMetadataReturnsAnErrorRatherThanPanicking
// mirrors TestSetSequentialBeforeMetadataReturnsAnErrorRatherThanPanicking —
// the identical crash class this method's own doc comment describes,
// pinned down the same way.
func TestSetFirstLastPieceFirstBeforeMetadataReturnsAnErrorRatherThanPanicking(t *testing.T) {
	mi, _ := buildTorrent(t, "magnet-boost.bin", 16384, []fileSpec{{length: 16384 * 4}})

	tr, err := NewFromInfoHash(mi.InfoHash, newTestConfig(t))
	if err != nil {
		t.Fatalf("NewFromInfoHash: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateFetchingMetadata, 2*time.Second)

	if err := tr.SetFirstLastPieceFirst(true); err == nil {
		t.Fatal("SetFirstLastPieceFirst before metadata: want an error, got nil")
	}
}

// TestSetSeedLimitsRuntimeRatioLimitPausesSeedingTorrent proves the runtime
// PATCH toggle, not just Config.SeedRatioLimit at construction (already
// covered by seedlimit_test.go), reaches checkSeedLimits: a seeder started
// with no limit at all gets one applied at runtime shortly after a leecher
// starts pulling from it, and still pauses itself once it crosses the newly
// set ratio.
func TestSetSeedLimitsRuntimeRatioLimitPausesSeedingTorrent(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "ratio-runtime.bin", pieceLength, []fileSpec{{length: pieceLength * 20}})

	seeder := newPreSeededTorrent(t, "ratio-runtime.bin", mi, content, newTestConfig(t))
	runInBackground(t, seeder)
	waitForState(t, seeder, StateSeeding, 5*time.Second)
	pi := listenAndRoute(t, seeder)

	ratio := 0.5
	if err := seeder.SetSeedLimits(&ratio, nil); err != nil {
		t.Fatalf("SetSeedLimits: %v", err)
	}

	leecher, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New (leecher): %v", err)
	}
	runInBackground(t, leecher)
	leecher.DialPeer(pi)

	waitForState(t, seeder, StatePaused, 15*time.Second)

	stats := seeder.Stats()
	if stats.SeedRatio < ratio {
		t.Fatalf("seeder paused with SeedRatio %.2f, want at least the runtime-set limit %.2f", stats.SeedRatio, ratio)
	}
	if stats.Uploaded == 0 {
		t.Fatal("seeder paused with zero bytes ever uploaded — ratio limit fired on no real traffic")
	}
}

// TestSetSeedLimitsNilArgumentLeavesTheOtherLimitUnchanged proves the
// documented "either argument may be nil to leave that particular limit
// unchanged" contract — setting only the ratio must not clobber a
// previously configured time limit back to 0 (unlimited).
func TestSetSeedLimitsNilArgumentLeavesTheOtherLimitUnchanged(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "seedlimits-partial.bin", pieceLength, []fileSpec{{length: pieceLength * 2}})

	cfg := newTestConfig(t)
	cfg.SeedTimeLimit = time.Hour
	tr := newPreSeededTorrent(t, "seedlimits-partial.bin", mi, content, cfg)
	runInBackground(t, tr)
	waitForState(t, tr, StateSeeding, 5*time.Second)

	ratio := 0.75
	if err := tr.SetSeedLimits(&ratio, nil); err != nil {
		t.Fatalf("SetSeedLimits: %v", err)
	}

	if got := tr.cfg.SeedRatioLimit; got != ratio {
		t.Fatalf("SeedRatioLimit = %v, want %v", got, ratio)
	}
	if got := tr.cfg.SeedTimeLimit; got != time.Hour {
		t.Fatalf("SeedTimeLimit = %v, want unchanged at %v (nil argument clobbered it)", got, time.Hour)
	}
}
