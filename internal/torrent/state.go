// Package torrent implements the torrent actor: one goroutine per torrent
// that owns all of its piece state and drives it through a state machine.
//
// This replaces the design in internal/session, where a torrent was
// constructed *from* a fully parsed MetaInfo (session.New(metaInfo, ...)) and
// piece state lived behind a single mutex shared by every peer goroutine. A
// torrent here is identified by its infohash from the moment it is created;
// metadata may arrive later, which is what a magnet link needs. State that
// belongs to the download — the picker, the active bitfield, the connected
// peers — is touched only by the actor goroutine (Torrent.run); everything
// else talks to it through channels or atomics, never a lock.
package torrent

import "fmt"

// State is where a torrent has got to in its lifecycle.
//
//	Added ──▶ FetchingMetadata ──▶ CheckingFiles ──▶ Downloading ──▶ Seeding
//	            (magnet only)            ▲                │              │
//	                                     └──────Paused─────┴──────────────┘
//	                                              │
//	                                            Error
type State int32

const (
	// StateAdded is the instant after construction, before the actor's first
	// tick has decided whether metadata is already known.
	StateAdded State = iota

	// StateFetchingMetadata means the infohash is known but the info
	// dictionary is not. Peers may already be connected. This is the magnet
	// link state; nothing populates it yet (that is Phase 2's BEP 9), but the
	// type and the transition exist so Phase 2 only has to plug in the
	// exchange itself.
	StateFetchingMetadata

	// StateCheckingFiles means metadata is known and the on-disk data is being
	// hashed against it, either from scratch or because resume data could not
	// be trusted.
	StateCheckingFiles

	// StateDownloading means at least one piece is still missing.
	StateDownloading

	// StateSeeding means every piece is verified and on disk.
	StateSeeding

	// StatePaused means the torrent is not connecting to peers or
	// transferring, but its state is intact and Resume() picks up where it
	// left off.
	StatePaused

	// StateError means the actor hit something it cannot recover from on its
	// own (repeated disk I/O failure, most likely) and stopped.
	StateError
)

func (s State) String() string {
	switch s {
	case StateAdded:
		return "Added"
	case StateFetchingMetadata:
		return "FetchingMetadata"
	case StateCheckingFiles:
		return "CheckingFiles"
	case StateDownloading:
		return "Downloading"
	case StateSeeding:
		return "Seeding"
	case StatePaused:
		return "Paused"
	case StateError:
		return "Error"
	default:
		return fmt.Sprintf("State(%d)", int32(s))
	}
}

// transitions is the complete allowed-edge table. Anything not listed here —
// including every state's edge to itself — is rejected by setState, which is
// what makes the diagram in the package comment an enforced invariant instead
// of a comment that can drift from the code.
var transitions = map[State][]State{
	StateAdded:            {StateFetchingMetadata, StateCheckingFiles},
	StateFetchingMetadata: {StateCheckingFiles, StatePaused},
	StateCheckingFiles:    {StateDownloading, StateSeeding, StatePaused},
	StateDownloading:      {StateSeeding, StatePaused, StateCheckingFiles},
	StateSeeding:          {StatePaused, StateCheckingFiles},
	StatePaused:           {StateFetchingMetadata, StateCheckingFiles, StateDownloading, StateSeeding},
}

// CanTransition reports whether moving from s to next is a legal edge.
// StateError is reachable from any non-terminal state and is not listed
// per-state to avoid repeating it six times.
func (s State) CanTransition(next State) bool {
	if next == StateError {
		return s != StateError
	}
	for _, allowed := range transitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// Active reports whether the torrent is doing something in this state, as
// opposed to sitting idle (Paused) or stopped (Error).
func (s State) Active() bool {
	switch s {
	case StateFetchingMetadata, StateCheckingFiles, StateDownloading, StateSeeding:
		return true
	default:
		return false
	}
}
