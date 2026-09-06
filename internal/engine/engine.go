// Package engine manages a fleet of torrents: adding, listing, and removing
// them as a set, and persisting that set across restarts. internal/torrent
// already proved a single process can run many independent Torrent actors
// side by side (TestConcurrentTorrents); Engine is the layer that tracks
// which torrents exist, so a caller (the CLI, or eventually a daemon) does
// not have to.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/picker"
	"github.com/Oblutack/GoTorrent/internal/ratelimit"
	"github.com/Oblutack/GoTorrent/internal/storage"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// DefaultStateDir returns the directory an Engine's manifest lives in when a
// caller has no preference, mirroring torrent.ResumeDir: a per-user config
// directory, portable across OSes via os.UserConfigDir.
func DefaultStateDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("engine: could not locate a config directory: %w", err)
	}
	return filepath.Join(base, "GoTorrent", "engine"), nil
}

// Defaults configures every torrent the Engine starts. DownLimit and UpLimit
// are typically shared *ratelimit.Limiter instances across the whole fleet,
// so the cap bounds the process's total transfer rate rather than each
// torrent independently.
type Defaults struct {
	DownloadDir    string
	ResumeDir      string
	ListenPort     uint16
	Allocation     storage.Allocation
	PickerStrategy picker.Strategy
	DownLimit      *ratelimit.Limiter
	UpLimit        *ratelimit.Limiter
}

// Summary is a point-in-time view of one managed torrent, safe to read from
// any goroutine.
type Summary struct {
	InfoHash    metainfo.Hash
	TorrentPath string
	DownloadDir string
	Stats       torrent.Stats
}

// managedTorrent is what the Engine tracks per torrent beyond what Torrent
// itself already knows — the .torrent file path and resolved download
// directory need to survive a restart, so they are exactly what the
// manifest records.
type managedTorrent struct {
	t           *torrent.Torrent
	torrentPath string
	downloadDir string
}

// Engine owns a set of running torrents and the manifest that lets them
// survive a process restart. It is safe for concurrent use.
type Engine struct {
	mu       sync.Mutex
	stateDir string
	defaults Defaults
	torrents map[metainfo.Hash]*managedTorrent
}

// New creates an Engine whose manifest lives under stateDir. It does not load
// any previously-persisted torrents; call Load for that.
func New(stateDir string, defaults Defaults) (*Engine, error) {
	if stateDir == "" {
		return nil, errors.New("engine: state directory is required")
	}
	return &Engine{
		stateDir: stateDir,
		defaults: defaults,
		torrents: make(map[metainfo.Hash]*managedTorrent),
	}, nil
}

// Add loads a .torrent file and starts it running under the engine's
// management. downloadDir overrides the engine's default for this torrent
// alone; pass "" to use the default. The torrent is persisted to the
// manifest before it is started, so Add either leaves the fleet exactly as
// it was or commits both the in-memory and on-disk state together.
func (e *Engine) Add(torrentPath, downloadDir string) (metainfo.Hash, error) {
	mi, err := metainfo.Load(torrentPath)
	if err != nil {
		return metainfo.Hash{}, fmt.Errorf("engine: loading %s: %w", torrentPath, err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.torrents[mi.InfoHash]; exists {
		return mi.InfoHash, fmt.Errorf("engine: %s is already added", mi.InfoHash)
	}
	if downloadDir == "" {
		downloadDir = e.defaults.DownloadDir
	}
	if downloadDir == "" {
		return metainfo.Hash{}, errors.New("engine: no download directory given and no default configured")
	}

	tr, err := torrent.New(mi, e.torrentConfig(downloadDir))
	if err != nil {
		return metainfo.Hash{}, fmt.Errorf("engine: creating torrent: %w", err)
	}

	mt := &managedTorrent{t: tr, torrentPath: torrentPath, downloadDir: downloadDir}
	e.torrents[mi.InfoHash] = mt

	if err := e.saveManifestLocked(); err != nil {
		delete(e.torrents, mi.InfoHash)
		return metainfo.Hash{}, fmt.Errorf("engine: persisting manifest: %w", err)
	}

	go func() {
		if err := tr.Run(context.Background()); err != nil {
			logger.Error.Printf("engine: torrent %s: %v\n", mi.InfoHash, err)
		}
	}()

	return mi.InfoHash, nil
}

// Load reloads every torrent recorded in the manifest, e.g. at process
// startup. A torrent file that fails to load is logged and skipped rather
// than aborting the rest of the fleet — a single moved or deleted .torrent
// file should not take every other torrent down with it.
func (e *Engine) Load() error {
	entries, err := e.readManifest()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("engine: reading manifest: %w", err)
	}
	for _, ent := range entries {
		if _, err := e.Add(ent.TorrentPath, ent.DownloadDir); err != nil {
			logger.Warning.Printf("engine: could not reload %s: %v\n", ent.TorrentPath, err)
		}
	}
	return nil
}

// Remove stops a managed torrent for good and drops it from the manifest.
// It blocks until the torrent has actually shut down.
func (e *Engine) Remove(hash metainfo.Hash) error {
	e.mu.Lock()
	mt, ok := e.torrents[hash]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("engine: %s is not managed by this engine", hash)
	}
	delete(e.torrents, hash)
	if err := e.saveManifestLocked(); err != nil {
		e.torrents[hash] = mt // keep in-memory and on-disk state consistent
		e.mu.Unlock()
		return fmt.Errorf("engine: persisting manifest: %w", err)
	}
	e.mu.Unlock()

	// Stop blocks on the torrent's own shutdown; it must not be called while
	// holding e.mu; List/Get calls a Remove-in-progress torrent would
	// otherwise deadlock behind still need to work.
	mt.t.Stop()
	return nil
}

// Get returns the managed Torrent for hash, if any, for callers that need
// direct access (Pause/Resume/DialPeer/Stats beyond the Summary).
func (e *Engine) Get(hash metainfo.Hash) (*torrent.Torrent, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	mt, ok := e.torrents[hash]
	if !ok {
		return nil, false
	}
	return mt.t, true
}

// List returns a snapshot of every managed torrent, ordered by infohash for
// a stable, deterministic listing.
func (e *Engine) List() []Summary {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]Summary, 0, len(e.torrents))
	for hash, mt := range e.torrents {
		out = append(out, Summary{
			InfoHash:    hash,
			TorrentPath: mt.torrentPath,
			DownloadDir: mt.downloadDir,
			Stats:       mt.t.Stats(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InfoHash.String() < out[j].InfoHash.String() })
	return out
}

// Shutdown stops every managed torrent and waits for all of them to finish.
// Torrents are stopped concurrently — Stop is documented safe to call from
// any goroutine — so shutting down N torrents costs the slowest one, not the
// sum of all of them.
func (e *Engine) Shutdown() {
	e.mu.Lock()
	torrents := make([]*torrent.Torrent, 0, len(e.torrents))
	for _, mt := range e.torrents {
		torrents = append(torrents, mt.t)
	}
	e.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(len(torrents))
	for _, tr := range torrents {
		go func(tr *torrent.Torrent) {
			defer wg.Done()
			tr.Stop()
		}(tr)
	}
	wg.Wait()
}

func (e *Engine) torrentConfig(downloadDir string) torrent.Config {
	return torrent.Config{
		DownloadDir:    downloadDir,
		ResumeDir:      e.defaults.ResumeDir,
		ListenPort:     e.defaults.ListenPort,
		Allocation:     e.defaults.Allocation,
		PickerStrategy: e.defaults.PickerStrategy,
		DownLimit:      e.defaults.DownLimit,
		UpLimit:        e.defaults.UpLimit,
	}
}
