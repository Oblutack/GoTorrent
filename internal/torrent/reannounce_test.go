package torrent

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
)

// TestReannounceForcesImmediateAnnounce proves Reannounce actually gets a
// fresh announce out promptly rather than waiting on the tracker's own
// (here, deliberately very long) interval - the whole point of the route
// this backs (4.2's POST .../reannounce).
func TestReannounceForcesImmediateAnnounce(t *testing.T) {
	mi, _ := buildTorrent(t, "reannounce.bin", 16384, []fileSpec{{length: 16384 * 2}})

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		resp := struct {
			Interval int64 `bencode:"interval"`
		}{Interval: 3600} // long enough that a second hit can only be Reannounce, not the loop's own timer
		data, err := bencode.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal fake tracker response: %v", err)
		}
		w.Write(data)
	}))
	defer srv.Close()
	mi.Announce = srv.URL + "/announce" // overrides buildTorrent's dead default, before New ever sees it

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	deadline := time.Now().Add(5 * time.Second)
	for hits.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("tracker never received the torrent's initial announce")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := tr.Reannounce(); err != nil {
		t.Fatalf("Reannounce: %v", err)
	}

	deadline = time.Now().Add(5 * time.Second)
	for hits.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("Reannounce did not produce a second announce within 5s (interval is 3600s, so only Reannounce could have caused it)")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestReannounceFailsWhilePaused is the control: a caller asking a paused
// torrent to reannounce right now should get told that plainly rather than
// silently doing nothing.
func TestReannounceFailsWhilePaused(t *testing.T) {
	mi, _ := buildTorrent(t, "reannounce-paused.bin", 16384, []fileSpec{{length: 16384 * 2}})
	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	if err := tr.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	waitForState(t, tr, StatePaused, 5*time.Second)

	if err := tr.Reannounce(); err == nil {
		t.Fatal("Reannounce succeeded on a paused torrent, want an error")
	}
}
