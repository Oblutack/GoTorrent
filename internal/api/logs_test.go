package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
)

// readLogFrame is readTextFrame's non-fatal counterpart, for a test that
// needs to keep retrying rather than fail the moment a single read
// attempt times out or the connection has nothing more queued yet.
func readLogFrame(client *eventsTestClient, timeout time.Duration) ([]byte, bool) {
	client.conn.SetReadDeadline(time.Now().Add(timeout))
	var head [2]byte
	if _, err := readFullBytes(client.r, head[:]); err != nil {
		return nil, false
	}
	length := int(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := readFullBytes(client.r, ext[:]); err != nil {
			return nil, false
		}
		length = int(ext[0])<<8 | int(ext[1])
	case 127:
		return nil, false
	}
	payload := make([]byte, length)
	if _, err := readFullBytes(client.r, payload); err != nil {
		return nil, false
	}
	return payload, true
}

// findLogEntry reads frames off client until one decodes as a WSLogEntry
// whose Message equals tag, or deadline passes — tolerant of read-timeout
// gaps between frames (real ones, waiting for the next entry to arrive),
// unlike readTextFrame which would fail the test outright on the first
// one.
func findLogEntry(client *eventsTestClient, tag string, deadline time.Time) bool {
	for time.Now().Before(deadline) {
		payload, ok := readLogFrame(client, 200*time.Millisecond)
		if !ok {
			continue
		}
		var entry WSLogEntry
		if err := json.Unmarshal(payload, &entry); err != nil {
			continue
		}
		if entry.Message == tag {
			return true
		}
	}
	return false
}

// TestLogsHandlerSendsRecentHistoryOnConnect proves a message logged
// before the connection was ever opened still shows up - the whole point
// of Tail existing separately from live Subscribe delivery. Requests a
// tail far larger than logger's own historyLimit (500) rather than
// relying on the small default (200): this whole package's test binary
// runs many other tests that start real engines with real background
// timers, and their async teardown can still be logging well after their
// own test function returned — real, observed noise, not hypothetical —
// so a small window risks the tagged entry aging out of "the most recent
// 200" before this test ever connects, even though it's nowhere close to
// aging out of "the most recent 500" logger itself still keeps.
func TestLogsHandlerSendsRecentHistoryOnConnect(t *testing.T) {
	tag := fmt.Sprintf("logstest-history-%d", time.Now().UnixNano())
	logger.Logf("%s", tag)

	srv := httptest.NewServer(http.HandlerFunc(LogsHandler()))
	defer srv.Close()
	client := dialEventsClient(t, srv.Listener.Addr().String(), "/api/v1/logs?tail=100000")

	if !findLogEntry(client, tag, time.Now().Add(3*time.Second)) {
		t.Fatalf("never received the pre-logged entry %q in the initial history burst", tag)
	}
}

// TestLogsHandlerLargeHistoryBurstIsNotTruncated is a real regression
// guard: LogsHandler's first cut wrote every history entry to the real
// ws.Conn in a tight loop via writeWSJSON, which gives up outright the
// moment ws.Conn's outbound queue (32 slots) is momentarily full — a real
// live test caught a ~70-entry burst cutting off partway through and the
// connection closing right there, well before defaultLogTail's own 200.
// Logs a batch comfortably larger than the outbound queue, immediately
// back to back with no pacing (the exact condition that triggered it),
// and proves every single one — including the very last, which a
// truncated burst would never reach — arrives.
func TestLogsHandlerLargeHistoryBurstIsNotTruncated(t *testing.T) {
	const burstSize = 200
	base := fmt.Sprintf("logstest-burst-%d", time.Now().UnixNano())
	for i := 0; i < burstSize; i++ {
		logger.Logf("%s-%d", base, i)
	}
	lastTag := fmt.Sprintf("%s-%d", base, burstSize-1)

	srv := httptest.NewServer(http.HandlerFunc(LogsHandler()))
	defer srv.Close()
	client := dialEventsClient(t, srv.Listener.Addr().String(), "/api/v1/logs?tail=100000")

	if !findLogEntry(client, lastTag, time.Now().Add(5*time.Second)) {
		t.Fatalf("never received %q, the last entry of a %d-entry burst — the connection was likely truncated/closed partway through", lastTag, burstSize)
	}
}

// TestLogsHandlerStreamsLiveEntries proves a message logged after the
// connection is already open arrives live, with ?tail=0 ruling out that
// it was merely part of the initial history burst instead.
func TestLogsHandlerStreamsLiveEntries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(LogsHandler()))
	defer srv.Close()
	client := dialEventsClient(t, srv.Listener.Addr().String(), "/api/v1/logs?tail=0")

	// The server subscribes only after replying to the handshake and
	// sending its (here, empty) history burst - re-logging on a short
	// interval rather than once closes the tiny real window where a
	// single early Logf call could race ahead of the server's own
	// Subscribe and never be delivered at all.
	tag := fmt.Sprintf("logstest-live-%d", time.Now().UnixNano())
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				logger.Logf("%s", tag)
			}
		}
	}()

	if !findLogEntry(client, tag, time.Now().Add(3*time.Second)) {
		t.Fatalf("never received the live entry %q", tag)
	}
}

// TestLogsHandlerTailZeroSkipsHistory proves "?tail=0" really means "send
// nothing from history" - a message logged before the connection was ever
// opened must never arrive on it, even though the same message logged
// after connecting (re-logged on an interval, same race-avoidance
// reasoning as the live-streaming test above) does.
func TestLogsHandlerTailZeroSkipsHistory(t *testing.T) {
	beforeTag := fmt.Sprintf("logstest-tail-zero-before-%d", time.Now().UnixNano())
	logger.Logf("%s", beforeTag)

	srv := httptest.NewServer(http.HandlerFunc(LogsHandler()))
	defer srv.Close()
	client := dialEventsClient(t, srv.Listener.Addr().String(), "/api/v1/logs?tail=0")

	afterTag := fmt.Sprintf("logstest-tail-zero-after-%d", time.Now().UnixNano())
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				logger.Logf("%s", afterTag)
			}
		}
	}()

	deadline := time.Now().Add(3 * time.Second)
	sawAfter := false
	for time.Now().Before(deadline) && !sawAfter {
		payload, ok := readLogFrame(client, 200*time.Millisecond)
		if !ok {
			continue
		}
		var entry WSLogEntry
		if err := json.Unmarshal(payload, &entry); err != nil {
			continue
		}
		if entry.Message == beforeTag {
			t.Fatal("received the pre-connect entry despite ?tail=0")
		}
		if entry.Message == afterTag {
			sawAfter = true
		}
	}
	if !sawAfter {
		t.Fatalf("never received the post-connect entry %q", afterTag)
	}
}

func TestLogsHandlerRejectsPlainHTTPRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(LogsHandler()))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("plain GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-WebSocket request", resp.StatusCode)
	}
}
