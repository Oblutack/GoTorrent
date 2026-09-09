// Package bootstrap wires up a fully-running *engine.Engine from a set of
// options, in the one dependency order every piece actually needs: IP
// filter before the listener (so nothing can slip in during a startup
// window with the filter not loaded yet), the listener before port
// mapping/DHT/LSD (they need the actual bound port, including whatever the
// OS assigned for -random-port), and all of that before Load (so a
// manifest-reloaded torrent gets a working DHT/PEX/LSD peer source from
// its very first tick, not just a freshly Add'd one).
//
// This exists because cmd/gottrent and cmd/gottrentd both need to run this
// exact sequence — duplicating it invites the two binaries' startup
// behavior to drift apart silently, the same class of bug 3.6 found in
// announceLoop/announceOnce (two copies of one rule, only one of them kept
// up to date).
package bootstrap

import (
	"context"
	"fmt"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/logger"
)

// Options configures Engine. It mirrors engine.Defaults plus the handful of
// knobs that are about *how this process starts up* rather than per-torrent
// behavior (Defaults already covers that half).
type Options struct {
	// StateDir holds the fleet manifest. Empty resolves to
	// engine.DefaultStateDir().
	StateDir string
	// ListenPort is the requested inbound peer port. Ignored if RandomPort
	// is set.
	ListenPort uint16
	// RandomPort listens on an OS-assigned port instead of ListenPort.
	RandomPort bool
	// NoPortMap disables automatic UPnP/NAT-PMP port mapping.
	NoPortMap bool
	// WatchDir starts StartWatchFolder when non-empty.
	WatchDir string
	// Defaults is passed straight through to engine.New.
	Defaults engine.Defaults
}

// Engine builds and starts every fleet-wide subsystem in the order above,
// then replays the persisted manifest (Load). The returned actualPort is
// what every subsequently Add'd torrent advertises to trackers and DHT
// peers — the requested ListenPort, whatever the OS assigned under
// RandomPort, or whatever StartPortMapping got a gateway to actually grant.
//
// engine.New, StartIPFilter, and Load failing is fatal — returned as an
// error rather than logged, since none of them are optional (a torrent
// added after New failed has nothing to be added to; a corrupt manifest
// silently ignored would mean the caller thinks it's managing a fleet it
// is not). Listen/StartPortMapping/StartDHT/StartLSD failing is not: each
// one only degrades what this process can do (no inbound connections, no
// automatic NAT traversal, no DHT/LSD peer discovery respectively), logged
// as a warning so a caller watching stderr can see it without treating a
// single blocked UDP port as reason to refuse to start at all.
func Engine(ctx context.Context, opts Options) (e *engine.Engine, actualPort uint16, err error) {
	dir := opts.StateDir
	if dir == "" {
		dir, err = engine.DefaultStateDir()
		if err != nil {
			return nil, 0, fmt.Errorf("bootstrap: resolving state directory: %w", err)
		}
	}

	e, err = engine.New(dir, opts.Defaults)
	if err != nil {
		return nil, 0, fmt.Errorf("bootstrap: creating engine: %w", err)
	}

	// Before Listen: closes the startup window where an inbound connection
	// could arrive before a configured IP filter is actually loaded.
	if err := e.StartIPFilter(ctx); err != nil {
		return nil, 0, fmt.Errorf("bootstrap: loading IP filter: %w", err)
	}

	actualPort = opts.ListenPort
	if opts.RandomPort {
		p, lerr := e.ListenRandomPort(ctx)
		if lerr != nil {
			logger.Warning.Printf("bootstrap: not accepting inbound connections: %v\n", lerr)
		} else {
			actualPort = p
			logger.Logf("bootstrap: listening on random port %d\n", actualPort)
		}
	} else if lerr := e.Listen(ctx); lerr != nil {
		logger.Warning.Printf("bootstrap: not accepting inbound connections: %v\n", lerr)
	}

	if !opts.NoPortMap {
		// Before StartDHT/Load: a successful mapping updates the port every
		// subsequently-built torrent advertises, so it needs to land before
		// anything reads that value.
		if perr := e.StartPortMapping(ctx, actualPort); perr != nil {
			logger.Logf("bootstrap: not mapping a port automatically (%v) - inbound connections need the port forwarded by hand unless this machine is already reachable\n", perr)
		}
	}
	if derr := e.StartDHT(ctx, actualPort); derr != nil {
		logger.Warning.Printf("bootstrap: not starting DHT: %v\n", derr)
	}
	// LSD always advertises the internal port, never StartPortMapping's
	// rewritten external one - an LSD peer is on the same LAN and connects
	// directly, not through any NAT mapping.
	if lerr := e.StartLSD(ctx, actualPort); lerr != nil {
		logger.Warning.Printf("bootstrap: not starting local service discovery: %v\n", lerr)
	}
	// Reactive queue enforcement is always on (every Add'd torrent's own
	// OnStateChange callback triggers it); this just adds the periodic
	// safety-net pass, and can start any time before or after Load/Add.
	e.StartQueue(ctx)
	// A no-op unless Defaults.AltSchedule was set.
	e.StartAltSpeedSchedule(ctx)
	if opts.WatchDir != "" {
		e.StartWatchFolder(ctx, opts.WatchDir)
	}

	// All of the above must be up before Load so every torrent - reloaded
	// from a previous run included - gets a working DHT/LSD peer source and
	// IP filter from the start, not just a freshly Add'd one.
	if err := e.Load(); err != nil {
		return nil, 0, fmt.Errorf("bootstrap: loading fleet manifest: %w", err)
	}

	return e, actualPort, nil
}
