package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/ratelimit"
)

func mustParseSchedule(t *testing.T, s string) Schedule {
	t.Helper()
	sched, err := ParseSchedule(s)
	if err != nil {
		t.Fatalf("ParseSchedule(%q): %v", s, err)
	}
	return sched
}

func TestParseScheduleRejectsMalformedInput(t *testing.T) {
	cases := []string{
		"",
		"22:00",
		"22:00-06:00-extra",
		"Mon 22:00",       // range field has no "-"
		"Xyz 22:00-06:00", // bad day name
		"25:00-06:00",     // bad hour
		"22:99-06:00",     // bad minute
	}
	for _, c := range cases {
		if _, err := ParseSchedule(c); err == nil {
			t.Errorf("ParseSchedule(%q) accepted, want an error", c)
		}
	}
}

func TestScheduleActiveWithinASingleDayWindow(t *testing.T) {
	sched := mustParseSchedule(t, "Mon,Tue,Wed,Thu,Fri 09:00-17:00")

	// Wednesday 12:00 — inside the window, an active day.
	if !sched.active(time.Date(2024, 1, 3, 12, 0, 0, 0, time.UTC)) {
		t.Error("Wednesday noon: want active")
	}
	// Wednesday 08:00 — before the window starts.
	if sched.active(time.Date(2024, 1, 3, 8, 0, 0, 0, time.UTC)) {
		t.Error("Wednesday 08:00: want inactive (before window)")
	}
	// Wednesday 17:00 — window end is exclusive.
	if sched.active(time.Date(2024, 1, 3, 17, 0, 0, 0, time.UTC)) {
		t.Error("Wednesday 17:00: want inactive (window end is exclusive)")
	}
	// Saturday noon — not an active day.
	if sched.active(time.Date(2024, 1, 6, 12, 0, 0, 0, time.UTC)) {
		t.Error("Saturday noon: want inactive (not a scheduled day)")
	}
}

func TestScheduleActiveAcrossMidnight(t *testing.T) {
	sched := mustParseSchedule(t, "Fri 22:00-06:00")

	// Friday 23:00 — after Start, same day.
	if !sched.active(time.Date(2024, 1, 5, 23, 0, 0, 0, time.UTC)) {
		t.Error("Friday 23:00: want active")
	}
	// Saturday 03:00 — before End, the overnight tail of Friday's window.
	if !sched.active(time.Date(2024, 1, 6, 3, 0, 0, 0, time.UTC)) {
		t.Error("Saturday 03:00 (tail of Friday's window): want active")
	}
	// Saturday 12:00 — well outside the window, and Saturday isn't itself
	// a scheduled day.
	if sched.active(time.Date(2024, 1, 6, 12, 0, 0, 0, time.UTC)) {
		t.Error("Saturday noon: want inactive")
	}
	// Saturday 23:00 — Saturday is not a scheduled day, so its own evening
	// must not be active even though the time-of-day matches.
	if sched.active(time.Date(2024, 1, 6, 23, 0, 0, 0, time.UTC)) {
		t.Error("Saturday 23:00: want inactive (Saturday not scheduled)")
	}
}

func TestParseScheduleDefaultsToEveryDay(t *testing.T) {
	sched := mustParseSchedule(t, "22:00-06:00")
	for _, d := range sched.Days {
		if !d {
			t.Fatal("no day list given: want every day enabled")
		}
	}
}

// TestStartAltSpeedScheduleTogglesARealEngine drives the real Engine
// (constructed via New, so normalDownBps/normalUpBps get captured the way
// production code does) through an always-on and an always-off schedule
// and checks the limiter's actual configured rate each time.
func TestStartAltSpeedScheduleTogglesARealEngine(t *testing.T) {
	always := Schedule{Days: [7]bool{true, true, true, true, true, true, true}, Start: 0, End: 23*time.Hour + 59*time.Minute}
	e, err := New(t.TempDir(), Defaults{
		DownloadDir:  t.TempDir(),
		ResumeDir:    t.TempDir(),
		DownLimit:    ratelimit.New(1000),
		UpLimit:      ratelimit.New(2000),
		AltDownLimit: 100,
		AltUpLimit:   200,
		AltSchedule:  &always,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	// Inside the (always-on) window: alt rates.
	e.applyAltSpeed(time.Date(2024, 1, 3, 12, 0, 0, 0, time.UTC))
	if got := e.defaults.DownLimit.Limit(); got != 100 {
		t.Fatalf("DownLimit = %d, want alt rate 100", got)
	}
	if got := e.defaults.UpLimit.Limit(); got != 200 {
		t.Fatalf("UpLimit = %d, want alt rate 200", got)
	}

	// Outside the window (0 width won't happen with "always", so flip the
	// schedule to a window that excludes noon instead).
	e.defaults.AltSchedule = &Schedule{Days: [7]bool{true, true, true, true, true, true, true}, Start: 0, End: 1 * time.Hour}
	e.applyAltSpeed(time.Date(2024, 1, 3, 12, 0, 0, 0, time.UTC))
	if got := e.defaults.DownLimit.Limit(); got != 1000 {
		t.Fatalf("DownLimit = %d, want normal rate 1000 restored", got)
	}
	if got := e.defaults.UpLimit.Limit(); got != 2000 {
		t.Fatalf("UpLimit = %d, want normal rate 2000 restored", got)
	}
}

func TestStartAltSpeedScheduleNoopWithoutASchedule(t *testing.T) {
	e := newTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.StartAltSpeedSchedule(ctx) // must not panic or spawn anything harmful
}

func TestSetTorrentRateLimitAppliesImmediately(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "ratelimit")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	mt, ok := e.torrents[hash]
	if !ok {
		t.Fatal("torrent missing right after Add")
	}
	if got := mt.downLimit.Limit(); got != 0 {
		t.Fatalf("fresh per-torrent downLimit = %d, want 0 (unlimited)", got)
	}

	if err := e.SetTorrentRateLimit(hash, 5000, 6000); err != nil {
		t.Fatalf("SetTorrentRateLimit: %v", err)
	}
	if got := mt.downLimit.Limit(); got != 5000 {
		t.Fatalf("downLimit after SetTorrentRateLimit = %d, want 5000", got)
	}
	if got := mt.upLimit.Limit(); got != 6000 {
		t.Fatalf("upLimit after SetTorrentRateLimit = %d, want 6000", got)
	}
}

func TestSetTorrentRateLimitRejectsUnknownHash(t *testing.T) {
	e := newTestEngine(t)
	var bogus metainfo.Hash
	if err := e.SetTorrentRateLimit(bogus, 1, 1); err == nil {
		t.Fatal("SetTorrentRateLimit on an unmanaged hash: want an error")
	}
}
