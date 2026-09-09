package torrent

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
)

// TestTrackerStatusesRecordsSuccessfulAnnounce proves a real announce to a
// real (fake) tracker shows up in TrackerStatuses with the seeder/leecher
// counts it reported.
func TestTrackerStatusesRecordsSuccessfulAnnounce(t *testing.T) {
	mi, _ := buildTorrent(t, "trackerstatus.bin", 16384, []fileSpec{{length: 16384 * 2}})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			Interval   int64 `bencode:"interval"`
			Complete   int   `bencode:"complete"`
			Incomplete int   `bencode:"incomplete"`
		}{Interval: 3600, Complete: 5, Incomplete: 2}
		data, err := bencode.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal fake tracker response: %v", err)
		}
		w.Write(data)
	}))
	defer srv.Close()
	mi.Announce = srv.URL + "/announce"

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	deadline := time.Now().Add(5 * time.Second)
	var statuses []TrackerStatus
	for time.Now().Before(deadline) {
		statuses = tr.TrackerStatuses()
		if len(statuses) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(statuses) != 1 {
		t.Fatalf("TrackerStatuses() = %d entries, want 1", len(statuses))
	}
	s := statuses[0]
	if s.URL != mi.Announce {
		t.Fatalf("URL = %q, want %q", s.URL, mi.Announce)
	}
	if s.LastError != "" {
		t.Fatalf("LastError = %q, want empty for a successful announce", s.LastError)
	}
	if s.Seeders != 5 || s.Leechers != 2 {
		t.Fatalf("Seeders/Leechers = %d/%d, want 5/2", s.Seeders, s.Leechers)
	}
	if s.LastAnnounce.IsZero() {
		t.Fatal("LastAnnounce was never set")
	}
}

// TestTrackerStatusesRecordsFailure proves a dead tracker shows up with a
// non-empty LastError rather than being silently absent.
func TestTrackerStatusesRecordsFailure(t *testing.T) {
	mi, _ := buildTorrent(t, "trackerfail.bin", 16384, []fileSpec{{length: 16384 * 2}})
	mi.Announce = "http://127.0.0.1:1/announce" // buildTorrent's own dead default, kept explicit here for clarity

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	deadline := time.Now().Add(5 * time.Second)
	var statuses []TrackerStatus
	for time.Now().Before(deadline) {
		statuses = tr.TrackerStatuses()
		if len(statuses) == 1 && statuses[0].LastError != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(statuses) != 1 || statuses[0].LastError == "" {
		t.Fatalf("TrackerStatuses() = %+v, want one entry with a non-empty LastError", statuses)
	}
}

func TestTrackerStatusesEmptyBeforeAnyAnnounce(t *testing.T) {
	mi, _ := buildTorrent(t, "notrackeryet.bin", 16384, []fileSpec{{length: 16384 * 2}})
	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if statuses := tr.TrackerStatuses(); len(statuses) != 0 {
		t.Fatalf("TrackerStatuses() before Run = %d entries, want 0", len(statuses))
	}
}
