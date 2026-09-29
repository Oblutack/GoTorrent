package tuiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/ws"
)

// newEventTestServer is a real ws.Upgrade-backed server, standing in for
// internal/api's own EventsHandler (which this test intentionally doesn't
// import, to keep internal/tuiclient's own tests independent of
// internal/api/internal/engine's much heavier dependency graph) — it just
// pushes whatever JSON messages the test hands it and records the
// Authorization header the real handshake carried.
func newEventTestServer(t *testing.T) (*httptest.Server, chan<- string, <-chan string) {
	t.Helper()
	toSend := make(chan string, 8)
	gotAuth := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth <- r.Header.Get("Authorization")
		conn, err := ws.Upgrade(w, r)
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		defer conn.Close()
		for msg := range toSend {
			if err := conn.WriteMessage(ws.OpText, []byte(msg)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, toSend, gotAuth
}

func TestSubscribeDecodesRealEventsAndSendsTheBearerToken(t *testing.T) {
	srv, toSend, gotAuth := newEventTestServer(t)
	c := New(srv.URL, "test-token")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, closeFn, err := Subscribe(ctx, c)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer func() { _ = closeFn() }()

	if got := <-gotAuth; got != "Bearer test-token" {
		t.Fatalf("Authorization header = %q, want Bearer test-token", got)
	}

	data, err := json.Marshal(WSEvent{Kind: "torrentAdded", InfoHash: "abc123"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	toSend <- string(data)

	select {
	case ev := <-events:
		if ev.Kind != "torrentAdded" || ev.InfoHash != "abc123" {
			t.Fatalf("got %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the real event to arrive over the wire")
	}
}

// TestSubscribeCloseFnUnblocksAPendingUndrainedEvent is the real reason
// Subscribe derives its own cancellable context rather than relying on
// conn.Close alone: a server message can arrive and be read off the wire
// before a caller ever calls closeFn, leaving the forwarding goroutine
// blocked trying to hand it to the unbuffered events channel rather than
// blocked inside ReadMessage - conn.Close() by itself only unblocks the
// latter. Without deriving innerCtx and selecting on it in that send, this
// test would hang until its own 5s timeout rather than passing quickly.
func TestSubscribeCloseFnUnblocksAPendingUndrainedEvent(t *testing.T) {
	srv, toSend, gotAuth := newEventTestServer(t)
	c := New(srv.URL, "")

	events, closeFn, err := Subscribe(context.Background(), c)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	<-gotAuth

	data, err := json.Marshal(WSEvent{Kind: "torrentAdded"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	toSend <- string(data)
	// Give the forwarding goroutine a real chance to read the message off
	// the wire and block trying to send it - deliberately not draining
	// events at all, the exact scenario the fix is for.
	time.Sleep(100 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		_ = closeFn()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("closeFn did not return - the forwarding goroutine is stuck sending an undrained event")
	}

	// events must still end up closed even though nothing ever drained it.
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("received a real event on a channel nobody was draining, want it closed instead")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("events was never closed after closeFn returned")
	}
}

func TestSubscribeClosesTheChannelWhenCloseFnIsCalled(t *testing.T) {
	srv, _, gotAuth := newEventTestServer(t)
	c := New(srv.URL, "")

	events, closeFn, err := Subscribe(context.Background(), c)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	<-gotAuth // wait for the real handshake to land before closing

	if err := closeFn(); err != nil {
		t.Fatalf("closeFn: %v", err)
	}

	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("received a real event after closeFn, want the channel closed instead")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for events to close after closeFn - a real goroutine leak, not just a slow test")
	}
}
