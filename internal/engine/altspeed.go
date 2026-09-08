package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// altSpeedCheckInterval is how often StartAltSpeedSchedule re-checks
// whether the current time is inside Defaults.AltSchedule's window. A
// minute-level schedule doesn't need finer granularity than this.
const altSpeedCheckInterval = 30 * time.Second

// Schedule describes a recurring weekly window during which
// Defaults.AltDownLimit/AltUpLimit apply instead of the normal
// DownLimit/UpLimit. A zero Schedule (every Days entry false) never
// matches anything.
type Schedule struct {
	Days  [7]bool // indexed by time.Weekday (Sunday = 0)
	Start time.Duration
	End   time.Duration
}

var weekdayNames = map[string]time.Weekday{
	"sun": time.Sunday, "sunday": time.Sunday,
	"mon": time.Monday, "monday": time.Monday,
	"tue": time.Tuesday, "tuesday": time.Tuesday,
	"wed": time.Wednesday, "wednesday": time.Wednesday,
	"thu": time.Thursday, "thursday": time.Thursday,
	"fri": time.Friday, "friday": time.Friday,
	"sat": time.Saturday, "saturday": time.Saturday,
}

// ParseSchedule parses "[days ]HH:MM-HH:MM" — e.g. "22:00-06:00" (every
// day, crossing midnight) or "Mon,Tue,Wed,Thu,Fri 09:00-17:00" (weekdays
// only). Day names are case-insensitive, full or three-letter abbreviated.
// Omitting the day list means every day.
func ParseSchedule(s string) (Schedule, error) {
	fields := strings.Fields(s)
	var dayField, rangeField string
	switch len(fields) {
	case 1:
		rangeField = fields[0]
	case 2:
		dayField, rangeField = fields[0], fields[1]
	default:
		return Schedule{}, fmt.Errorf("engine: invalid schedule %q, want \"[days] HH:MM-HH:MM\"", s)
	}

	var sched Schedule
	if dayField == "" {
		for i := range sched.Days {
			sched.Days[i] = true
		}
	} else {
		for _, d := range strings.Split(dayField, ",") {
			wd, ok := weekdayNames[strings.ToLower(strings.TrimSpace(d))]
			if !ok {
				return Schedule{}, fmt.Errorf("engine: invalid schedule day %q", d)
			}
			sched.Days[wd] = true
		}
	}

	start, end, ok := strings.Cut(rangeField, "-")
	if !ok {
		return Schedule{}, fmt.Errorf("engine: invalid schedule time range %q, want HH:MM-HH:MM", rangeField)
	}
	startTOD, err := parseTimeOfDay(start)
	if err != nil {
		return Schedule{}, err
	}
	endTOD, err := parseTimeOfDay(end)
	if err != nil {
		return Schedule{}, err
	}
	sched.Start, sched.End = startTOD, endTOD
	return sched, nil
}

func parseTimeOfDay(s string) (time.Duration, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	hour, err1 := strconv.Atoi(h)
	minute, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("engine: invalid time %q, want HH:MM (00:00-23:59)", s)
	}
	return time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute, nil
}

// active reports whether t falls inside the schedule's window.
//
// For a window that crosses midnight (End <= Start), the Days check for
// the portion past midnight uses the day the window STARTED on, not
// today's own Days entry — e.g. a Friday-only "22:00-06:00" schedule is
// active from Friday 22:00 through Saturday 06:00, not Saturday 22:00
// onward. This is a deliberate simplification: which calendar day "owns"
// an overnight window that spans a Days boundary has no universally
// obvious right answer (real clients disagree with each other here too),
// so this project picks the simplest consistent rule rather than guessing
// at intent.
func (s Schedule) active(t time.Time) bool {
	tod := timeOfDay(t)
	if s.Start == s.End {
		return false // zero-width window never matches
	}
	if s.End > s.Start {
		return s.Days[t.Weekday()] && tod >= s.Start && tod < s.End
	}
	// Overnight window: active from Start to midnight (today's Days), or
	// from midnight to End (yesterday's Days, since that's the day it
	// started on).
	if tod >= s.Start {
		return s.Days[t.Weekday()]
	}
	if tod < s.End {
		yesterday := (t.Weekday() + 6) % 7
		return s.Days[yesterday]
	}
	return false
}

func timeOfDay(t time.Time) time.Duration {
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute + time.Duration(t.Second())*time.Second
}

// StartAltSpeedSchedule begins periodically checking Defaults.AltSchedule
// and switching Defaults.DownLimit/UpLimit between the normal rate and
// AltDownLimit/AltUpLimit accordingly. A nil AltSchedule (the default) is a
// no-op — nothing is ever touched. Runs until ctx is cancelled; like
// StartQueue, there is no OS resource here for Shutdown to explicitly
// close.
func (e *Engine) StartAltSpeedSchedule(ctx context.Context) {
	if e.defaults.AltSchedule == nil {
		return
	}
	go e.altSpeedLoop(ctx)
}

func (e *Engine) altSpeedLoop(ctx context.Context) {
	e.applyAltSpeed(time.Now())
	ticker := time.NewTicker(altSpeedCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			e.applyAltSpeed(now)
		}
	}
}

// applyAltSpeed sets Defaults.DownLimit/UpLimit to the alt rate if now is
// inside the schedule, or restores the normal rate otherwise. SetLimit is
// cheap and idempotent, so this doesn't bother tracking which state was
// last applied — every tick just asserts the correct one.
func (e *Engine) applyAltSpeed(now time.Time) {
	e.mu.Lock()
	sched := e.defaults.AltSchedule
	down, up := e.defaults.DownLimit, e.defaults.UpLimit
	altDown, altUp := e.defaults.AltDownLimit, e.defaults.AltUpLimit
	normalDown, normalUp := e.normalDownBps, e.normalUpBps
	e.mu.Unlock()
	if sched == nil {
		return
	}

	wantDown, wantUp := normalDown, normalUp
	if sched.active(now) {
		wantDown, wantUp = altDown, altUp
	}
	if down != nil {
		down.SetLimit(wantDown)
	}
	if up != nil {
		up.SetLimit(wantUp)
	}
}
