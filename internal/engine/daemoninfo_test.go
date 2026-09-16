package engine

import (
	"context"
	"testing"
)

func TestInfoReportsZeroValuesBeforeAnythingStarted(t *testing.T) {
	e := newTestEngine(t)
	info := e.Info()

	if info.ListenPort != 0 {
		t.Errorf("ListenPort = %d, want 0 (Listen never called)", info.ListenPort)
	}
	if info.ExternalPort != 0 || info.PortMapped {
		t.Errorf("ExternalPort/PortMapped = %d/%v, want 0/false (StartPortMapping never called)", info.ExternalPort, info.PortMapped)
	}
	if info.DHTRunning || info.DHTNodeCount != 0 {
		t.Errorf("DHTRunning/DHTNodeCount = %v/%d, want false/0 (StartDHT never called)", info.DHTRunning, info.DHTNodeCount)
	}
	if info.LSDRunning {
		t.Error("LSDRunning = true, want false (StartLSD never called)")
	}
	if !info.PEXEnabled {
		t.Error("PEXEnabled = false, want true (always on per-torrent, no engine-level switch)")
	}
	if info.FreeDiskBytes < 0 {
		t.Errorf("FreeDiskBytes = %d, want a real non-negative figure for a real temp directory", info.FreeDiskBytes)
	}
}

// TestInfoReportsRealListenPortIndependentOfDefaultsListenPort is a real
// regression guard: StartPortMapping overwrites Defaults.ListenPort with
// the external port on success (see its own doc comment), which would make
// the real internal listen port unrecoverable if Info read that field
// instead of the actual listener. Simulates the rewrite directly (a real
// gateway isn't reachable in this environment - see
// TestStartPortMappingFailsGracefullyWithNoGateway) to prove Info still
// reports the real bound port regardless.
func TestInfoReportsRealListenPortIndependentOfDefaultsListenPort(t *testing.T) {
	e := newTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	actual, err := e.ListenRandomPort(ctx)
	if err != nil {
		t.Fatalf("ListenRandomPort: %v", err)
	}

	e.mu.Lock()
	e.defaults.ListenPort = 9999
	e.mu.Unlock()

	if got := e.Info().ListenPort; got != actual {
		t.Fatalf("Info().ListenPort = %d, want the real bound port %d (not Defaults.ListenPort's rewritten value 9999)", got, actual)
	}
}

func TestInfoReportsDHTRunningAndNodeCount(t *testing.T) {
	e := newTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := e.StartDHT(ctx, freeUDPPort(t)); err != nil {
		t.Fatalf("StartDHT: %v", err)
	}

	info := e.Info()
	if !info.DHTRunning {
		t.Fatal("DHTRunning = false after a real StartDHT call")
	}
	if info.DHTNodeCount != 0 {
		t.Fatalf("DHTNodeCount = %d, want 0 for a freshly-started node with no bootstrap peers", info.DHTNodeCount)
	}
}

func TestInfoReportsLSDRunning(t *testing.T) {
	e := newTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := e.StartLSD(ctx, 6881); err != nil {
		t.Fatalf("StartLSD: %v", err)
	}

	if !e.Info().LSDRunning {
		t.Fatal("LSDRunning = false after a real StartLSD call")
	}
}
