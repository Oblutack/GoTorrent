// Package trace implements Phase 8's explain/trace mode: a structured JSONL
// event log of what a torrent's actor actually did and why, meant to be
// replayed by a separate viewer (piece map over time, swarm graph, per-peer
// contribution, an annotated choke timeline) rather than read by this
// process itself. It is a debug/educational tool, not part of the wire
// protocol, so — unlike bencode — it deliberately uses the standard
// encoding/json rather than a hand-rolled encoder, the same choice
// internal/api's DTOs already made for the same reason.
package trace

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// Event is one line of a trace file. Every event carries Time/Torrent/Kind;
// the rest are populated only for the kinds that use them and left at their
// zero value (and so, thanks to omitempty, absent from the JSON) otherwise —
// one flat struct rather than a Kind-keyed union, so a viewer never has to
// know a whole schema hierarchy just to filter by Kind or Torrent.
type Event struct {
	Time    time.Time `json:"time"`
	Torrent string    `json:"torrent"`
	Kind    string    `json:"kind"`

	Peer string `json:"peer,omitempty"`
	// Piece and Begin are pointers, not plain ints: piece/block index 0 is
	// the single most common real value a piece-carrying event ever has
	// (the first piece, or a request/block starting at offset 0 — every
	// request in a single-block piece has Begin 0), and a plain int with
	// omitempty cannot tell "this event has no piece index" apart from
	// "this event's piece index is 0" — Go's encoding/json omits a zero
	// int just as eagerly as a genuinely absent one. A real trace-viewer
	// bug caught this: piece 0 of a real download silently had zero
	// trace events by every appearance, because every one of its events
	// had its "piece" field dropped entirely. Int is the constructor;
	// same "nil means not set, a real pointer to a real zero means
	// explicitly zero" idiom internal/engine's SetSeedLimits already uses
	// for this exact ambiguity.
	Piece  *int `json:"piece,omitempty"`
	Begin  *int `json:"begin,omitempty"`
	Length int  `json:"length,omitempty"`

	// From and To are the old and new state, for kind "state_changed".
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`

	// OK and Err are for kind "piece_verified" — OK is meaningful (and
	// always present) only for that kind, so it is not omitempty: a missing
	// "ok" field on any other kind just means the field does not apply.
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`

	// Priority, Strategy, Rarity, and Endgame are the picker's own reasoning
	// for kind "picker_decision" — why this piece, not some other one.
	Priority string `json:"priority,omitempty"`
	Strategy string `json:"strategy,omitempty"`
	Rarity   int    `json:"rarity,omitempty"`
	Endgame  bool   `json:"endgame,omitempty"`
}

// Int returns a pointer to v, for populating Event.Piece/Begin — a plain
// &v at the call site works just as well, but this reads better than a
// bare & in front of a temporary like len(x) or a loop variable.
func Int(v int) *int { return &v }

// Kinds. Kept as constants rather than typed so Event.Kind stays a plain
// string on the wire — a future viewer (or a future event kind this
// process's own older binary never knew about) never fails to parse a kind
// it doesn't recognize, the same "State is a plain string, not an enum"
// reasoning GoTorrent.Hub's own EngineClient DTOs already use.
const (
	KindStateChanged     = "state_changed"
	KindPeerConnected    = "peer_connected"
	KindPeerDisconnected = "peer_disconnected"
	KindChoke            = "choke"
	KindUnchoke          = "unchoke"
	KindPieceRequest     = "piece_request"
	KindBlockReceived    = "block_received"
	KindPieceVerified    = "piece_verified"
	KindPickerDecision   = "picker_decision"
)

// Writer appends Events to a trace file as JSON Lines (one compact JSON
// object per line) — the same line-oriented, append-only, tool-friendly
// shape as CLAUDE.md's own job-logs.txt this project has already leaned on
// for real debugging this session. Safe for concurrent use: Emit is called
// from several different goroutines in internal/torrent (the actor itself
// for most kinds, but verifyPiece's own goroutine for piece_verified).
//
// A nil *Writer is safe to call Emit/Close on and does nothing — the same
// "might not be one at all" convenience ipfilter.Filter and ratelimit
// already established, so every call site in internal/torrent can hand
// Config.Trace straight to Emit with no nil check, tracing being off by
// default.
type Writer struct {
	mu  sync.Mutex
	w   io.WriteCloser
	enc *json.Encoder
}

// New creates (or truncates) path and returns a Writer appending to it.
func New(path string) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &Writer{w: f, enc: json.NewEncoder(f)}, nil
}

// Emit stamps ev.Time (if not already set — real callers never set it
// themselves, but a test fixing a clock can) and appends it as one JSON
// line. A write failure is dropped rather than propagated: a trace file
// going bad (a full disk, most likely) must never be the reason a real
// download stalls or errors out — tracing is strictly observational.
func (w *Writer) Emit(ev Event) {
	if w == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.enc.Encode(ev)
}

// Close flushes and closes the underlying file. Safe to call on a nil
// Writer, same as Emit.
func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	return w.w.Close()
}
