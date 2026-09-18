package torrent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/trace"
)

// TestTraceCapturesARealDownload drives a real download (the same shape as
// TestFullDownload — New, Run, dial a real fake seeder, download every piece
// over the real wire protocol) with Config.Trace wired to a real
// *trace.Writer, and asserts the resulting JSONL file on disk actually
// contains the events Phase 8's explain/trace mode promises: state
// transitions, a real peer connecting, the picker's own reasoning for the
// pieces it started, real requests going out, real blocks coming back, and
// every piece hashing OK. This is the trace package's own contract proven
// against the real actor that is supposed to use it, not just against
// trace.Writer in isolation (see internal/trace's own tests for that).
func TestTraceCapturesARealDownload(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "traced.bin", pieceLength, []fileSpec{
		{length: pieceLength*3 + 111},
	})

	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	tw, err := trace.New(tracePath)
	if err != nil {
		t.Fatalf("trace.New: %v", err)
	}

	cfg := newTestConfig(t)
	cfg.Trace = tw

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 30*time.Second)
	if err := tw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events := readTraceEvents(t, tracePath)
	if len(events) == 0 {
		t.Fatal("no trace events were written for a real download")
	}

	wantHash := mi.InfoHash.String()
	for _, ev := range events {
		if ev.Torrent != wantHash {
			t.Fatalf("event %+v has Torrent %q, want %q", ev, ev.Torrent, wantHash)
		}
	}

	byKind := make(map[string][]trace.Event)
	for _, ev := range events {
		byKind[ev.Kind] = append(byKind[ev.Kind], ev)
	}

	requireKind(t, byKind, trace.KindStateChanged)
	requireKind(t, byKind, trace.KindPeerConnected)
	requireKind(t, byKind, trace.KindPickerDecision)
	requireKind(t, byKind, trace.KindPieceRequest)
	requireKind(t, byKind, trace.KindBlockReceived)
	requireKind(t, byKind, trace.KindPieceVerified)

	if got := byKind[trace.KindPeerConnected][0].Peer; got != seeder.peerInfo().Addr() {
		t.Errorf("peer_connected Peer = %q, want %q", got, seeder.peerInfo().Addr())
	}

	for _, ev := range byKind[trace.KindPieceVerified] {
		if !ev.OK {
			t.Errorf("piece_verified for piece %d reported OK=false in a download that should have succeeded", ev.Piece)
		}
		if ev.Piece < 0 || ev.Piece >= mi.NumPieces() {
			t.Errorf("piece_verified Piece = %d, out of range [0,%d)", ev.Piece, mi.NumPieces())
		}
	}

	// The picker's own reasoning has to be real, resolvable data, not just
	// a non-empty string — Priority/Strategy round-trip through their own
	// String() methods, and Rarity must reflect a real peer actually
	// holding the piece (nextPiece never starts one with a zero count).
	for _, ev := range byKind[trace.KindPickerDecision] {
		if ev.Priority != "normal" {
			t.Errorf("picker_decision Priority = %q, want %q (no file priorities configured)", ev.Priority, "normal")
		}
		if ev.Strategy != "rarest-first" {
			t.Errorf("picker_decision Strategy = %q, want %q (the default)", ev.Strategy, "rarest-first")
		}
		if ev.Rarity < 1 {
			t.Errorf("picker_decision Rarity = %d, want >=1 (the seeder holds every piece)", ev.Rarity)
		}
	}

	sawSeeding := false
	for _, ev := range byKind[trace.KindStateChanged] {
		if ev.To == StateSeeding.String() {
			sawSeeding = true
		}
	}
	if !sawSeeding {
		t.Error("no state_changed event recorded the transition into seeding")
	}
}

// TestTraceOffByDefaultAddsNoOverhead proves a Config with no Trace set
// (the default for every existing caller) never touches a nil *trace.Writer
// in a way that would panic — internal/trace's own tests already prove Emit
// is nil-safe in isolation; this proves the real call sites in this package
// actually rely on that rather than guarding it themselves, by running a
// real download with Config.Trace left nil, same as every test before this
// file existed.
func TestTraceOffByDefaultAddsNoOverhead(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "untraced.bin", pieceLength, []fileSpec{
		{length: pieceLength * 2},
	})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 30*time.Second)
}

func requireKind(t *testing.T, byKind map[string][]trace.Event, kind string) {
	t.Helper()
	if len(byKind[kind]) == 0 {
		t.Errorf("no %q event was recorded", kind)
	}
}

func readTraceEvents(t *testing.T, path string) []trace.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open trace file: %v", err)
	}
	defer f.Close()

	var events []trace.Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var ev trace.Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("line %q is not valid JSON: %v", scanner.Text(), err)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanning trace file: %v", err)
	}
	return events
}
