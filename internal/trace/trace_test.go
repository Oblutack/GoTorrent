package trace

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriterEmitAppendsOneJSONLinePerEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	w, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w.Emit(Event{Torrent: "abc", Kind: KindPeerConnected, Peer: "1.2.3.4:6881"})
	w.Emit(Event{Torrent: "abc", Kind: KindPieceVerified, Piece: Int(5), OK: true})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}

	var first Event
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("unmarshal first line: %v", err)
	}
	if first.Kind != KindPeerConnected || first.Peer != "1.2.3.4:6881" || first.Time.IsZero() {
		t.Errorf("first event = %+v, want kind/peer set and a real timestamp", first)
	}

	var second Event
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("unmarshal second line: %v", err)
	}
	if second.Kind != KindPieceVerified || second.Piece == nil || *second.Piece != 5 || !second.OK {
		t.Errorf("second event = %+v, want piece_verified/piece=5/ok=true", second)
	}
}

func TestNilWriterEmitAndCloseAreNoOps(t *testing.T) {
	var w *Writer
	w.Emit(Event{Kind: KindStateChanged}) // must not panic
	if err := w.Close(); err != nil {
		t.Errorf("Close on nil Writer = %v, want nil", err)
	}
}

func TestWriterEmitIsSafeForConcurrentUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	w, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const goroutines = 20
	const perGoroutine = 25
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				w.Emit(Event{Torrent: "abc", Kind: KindBlockReceived, Piece: Int(g), Begin: Int(i)})
			}
		}(g)
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("line %d is not valid JSON: %v (%q)", n, err, scanner.Text())
		}
		n++
	}
	if n != goroutines*perGoroutine {
		t.Errorf("got %d well-formed lines, want %d", n, goroutines*perGoroutine)
	}
}
