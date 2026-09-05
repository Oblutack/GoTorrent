package torrent

import "testing"

func TestStateTransitions(t *testing.T) {
	tests := []struct {
		from, to State
		want     bool
	}{
		// The normal path for a torrent added from a .torrent file.
		{StateAdded, StateCheckingFiles, true},
		{StateCheckingFiles, StateDownloading, true},
		{StateCheckingFiles, StateSeeding, true}, // already-complete data
		{StateDownloading, StateSeeding, true},

		// The magnet path.
		{StateAdded, StateFetchingMetadata, true},
		{StateFetchingMetadata, StateCheckingFiles, true},

		// Pause/resume from every active state, and back out again.
		{StateFetchingMetadata, StatePaused, true},
		{StateDownloading, StatePaused, true},
		{StateSeeding, StatePaused, true},
		{StatePaused, StateFetchingMetadata, true},
		{StatePaused, StateCheckingFiles, true},
		{StatePaused, StateDownloading, true},
		{StatePaused, StateSeeding, true},

		// Force-recheck from an active state.
		{StateDownloading, StateCheckingFiles, true},
		{StateSeeding, StateCheckingFiles, true},

		// Error is reachable from anywhere except itself.
		{StateAdded, StateError, true},
		{StateFetchingMetadata, StateError, true},
		{StateCheckingFiles, StateError, true},
		{StateDownloading, StateError, true},
		{StateSeeding, StateError, true},
		{StatePaused, StateError, true},
		{StateError, StateError, false},

		// Illegal jumps.
		{StateAdded, StateDownloading, false},
		{StateAdded, StateSeeding, false},
		{StateAdded, StatePaused, false},
		{StateSeeding, StateDownloading, false}, // going backwards without a recheck
		{StateError, StateDownloading, false},
		{StateError, StatePaused, false},
		{StateCheckingFiles, StateFetchingMetadata, false},

		// No state transitions to itself; that is a no-op the caller should
		// simply not perform, not a transition to validate.
		{StateAdded, StateAdded, false},
		{StateDownloading, StateDownloading, false},
		{StatePaused, StatePaused, false},
	}

	for _, tt := range tests {
		if got := tt.from.CanTransition(tt.to); got != tt.want {
			t.Errorf("%s.CanTransition(%s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestStateActive(t *testing.T) {
	active := []State{StateFetchingMetadata, StateCheckingFiles, StateDownloading, StateSeeding}
	idle := []State{StateAdded, StatePaused, StateError}

	for _, s := range active {
		if !s.Active() {
			t.Errorf("%s.Active() = false, want true", s)
		}
	}
	for _, s := range idle {
		if s.Active() {
			t.Errorf("%s.Active() = true, want false", s)
		}
	}
}

func TestStateString(t *testing.T) {
	if got := State(99).String(); got == "" {
		t.Fatal("String() on an unknown state returned empty")
	}
	for s := StateAdded; s <= StateError; s++ {
		if got := s.String(); got == "" {
			t.Errorf("State(%d).String() is empty", s)
		}
	}
}
