package logger

import (
	"testing"
	"time"
)

// drainHistory resets the package-level history/subs state so each test
// starts from a clean slate — this package's state is global, and every
// other package's own tests call logger.Init/Logf/Info/etc. throughout a
// single `go test ./...` process, so a test here can't assume it's the
// only thing that has ever recorded an Entry.
func drainHistory(t *testing.T) {
	t.Helper()
	historyMu.Lock()
	history = nil
	historyMu.Unlock()
}

func TestLogfAlwaysRecordsRegardlessOfVerbose(t *testing.T) {
	Init(false)
	drainHistory(t)
	t.Cleanup(func() { Init(false) })

	Logf("hello %s", "world")

	got := Tail(-1)
	if len(got) != 1 {
		t.Fatalf("Tail returned %d entries, want 1", len(got))
	}
	if got[0].Level != "debug" {
		t.Fatalf("Level = %q, want %q", got[0].Level, "debug")
	}
	if got[0].Message != "hello world" {
		t.Fatalf("Message = %q, want %q", got[0].Message, "hello world")
	}
}

func TestInfoWarningErrorAlwaysRecordRegardlessOfVerbose(t *testing.T) {
	Init(false)
	drainHistory(t)
	t.Cleanup(func() { Init(false) })

	Info.Print("an info line")
	Warning.Print("a warning line")
	Error.Print("an error line")

	got := Tail(-1)
	if len(got) != 3 {
		t.Fatalf("Tail returned %d entries, want 3, got %+v", len(got), got)
	}
	wantLevels := []string{"info", "warning", "error"}
	for i, want := range wantLevels {
		if got[i].Level != want {
			t.Fatalf("entry %d level = %q, want %q", i, got[i].Level, want)
		}
	}
}

func TestTailBoundsToHistoryLimit(t *testing.T) {
	Init(false)
	drainHistory(t)
	t.Cleanup(func() { Init(false) })

	for i := 0; i < historyLimit+50; i++ {
		Logf("line %d", i)
	}

	got := Tail(-1)
	if len(got) != historyLimit {
		t.Fatalf("Tail(-1) returned %d entries, want the cap %d", len(got), historyLimit)
	}
	// The oldest entries (0..49) should have been evicted - the earliest
	// entry kept is line 50.
	if got[0].Message != "line 50" {
		t.Fatalf("oldest kept entry = %q, want %q (the earliest ones evicted)", got[0].Message, "line 50")
	}
}

// TestTailZeroReturnsNothing is a real regression guard: Tail(0) used to
// hit the same "n <= 0 means everything" branch as a negative n, which
// broke internal/api's LogsHandler ("?tail=0" is supposed to mean "skip
// history entirely") — caught live by a WS test in that package, not by a
// test here first.
func TestTailZeroReturnsNothing(t *testing.T) {
	Init(false)
	drainHistory(t)
	t.Cleanup(func() { Init(false) })

	Logf("should not appear")

	got := Tail(0)
	if len(got) != 0 {
		t.Fatalf("Tail(0) returned %d entries, want 0", len(got))
	}
}

func TestTailWithNReturnsOnlyTheMostRecentN(t *testing.T) {
	Init(false)
	drainHistory(t)
	t.Cleanup(func() { Init(false) })

	Logf("first")
	Logf("second")
	Logf("third")

	got := Tail(2)
	if len(got) != 2 {
		t.Fatalf("Tail(2) returned %d entries, want 2", len(got))
	}
	if got[0].Message != "second" || got[1].Message != "third" {
		t.Fatalf("Tail(2) = %v, want [second, third]", got)
	}
}

func TestSubscribeReceivesLiveEntries(t *testing.T) {
	Init(false)
	drainHistory(t)
	t.Cleanup(func() { Init(false) })

	ch, cancel := Subscribe()
	defer cancel()

	Logf("live entry")

	select {
	case e := <-ch:
		if e.Message != "live entry" {
			t.Fatalf("received Message = %q, want %q", e.Message, "live entry")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a subscribed entry")
	}
}

func TestSubscribeCancelStopsDelivery(t *testing.T) {
	Init(false)
	drainHistory(t)
	t.Cleanup(func() { Init(false) })

	ch, cancel := Subscribe()
	cancel()

	Logf("after cancel")

	select {
	case e, ok := <-ch:
		if ok {
			t.Fatalf("received an entry after cancel: %+v", e)
		}
	case <-time.After(100 * time.Millisecond):
		// No delivery within a short window is the expected outcome — the
		// channel is never closed by cancel (see Subscribe's own doc
		// comment), so this can't assert on a closed channel instead.
	}
}

func TestSubscribeDoesNotBlockOnAFullSubscriberChannel(t *testing.T) {
	Init(false)
	drainHistory(t)
	t.Cleanup(func() { Init(false) })

	ch, cancel := Subscribe()
	defer cancel()

	// Fill the subscriber's buffer without ever draining it, then log one
	// more than it can hold - record must not block waiting on a full
	// channel (the non-blocking-send-under-backpressure contract).
	done := make(chan struct{})
	go func() {
		for i := 0; i < cap(ch)+5; i++ {
			Logf("flood %d", i)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Logf blocked while flooding a full subscriber channel")
	}
}
