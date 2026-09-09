package bootstrap

import (
	"context"
	"os"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/logger"
)

func TestMain(m *testing.M) {
	logger.Init(false)
	os.Exit(m.Run())
}

// TestEngineWiresSubsystemsAndLoads proves the real sequence runs end to
// end and returns a usable *engine.Engine: RandomPort/NoPortMap keep this
// fast and network-independent (no real UPnP discovery attempted, no fixed
// port to collide with a parallel test run), matching the pattern
// internal/engine's own StartDHT/StartLSD tests already use.
func TestEngineWiresSubsystemsAndLoads(t *testing.T) {
	e, port, err := Engine(context.Background(), Options{
		StateDir:   t.TempDir(),
		RandomPort: true,
		NoPortMap:  true,
		Defaults: engine.Defaults{
			DownloadDir: t.TempDir(),
			ResumeDir:   t.TempDir(),
		},
	})
	if err != nil {
		t.Fatalf("Engine: %v", err)
	}
	t.Cleanup(e.Shutdown)

	if port == 0 {
		t.Fatal("RandomPort was set but the returned actualPort is 0")
	}
	if len(e.List()) != 0 {
		t.Fatalf("List() = %d entries on a fresh state dir, want 0", len(e.List()))
	}
}

// TestEngineFailsOnInvalidDefaults proves a genuinely fatal setup error
// (here, engine.New's own AnonymousMode-without-a-proxy refusal) comes back
// as an error rather than being swallowed like the best-effort subsystems
// (Listen/StartPortMapping/StartDHT/StartLSD) are.
func TestEngineFailsOnInvalidDefaults(t *testing.T) {
	_, _, err := Engine(context.Background(), Options{
		StateDir: t.TempDir(),
		Defaults: engine.Defaults{
			DownloadDir:   t.TempDir(),
			ResumeDir:     t.TempDir(),
			AnonymousMode: true, // no ProxyType set - engine.New must refuse this
		},
	})
	if err == nil {
		t.Fatal("Engine succeeded with AnonymousMode set and no proxy configured, want an error")
	}
}
