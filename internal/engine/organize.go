package engine

import (
	"fmt"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// SetCategory changes hash's category after the fact. It only updates the
// recorded metadata and persists it — it never moves the torrent's files;
// use MoveData for that, since a category and a save path are two separate
// concerns here (Defaults.CategoryPaths only ever applies at Add time, when
// downloadDir is empty).
func (e *Engine) SetCategory(hash metainfo.Hash, category string) error {
	e.mu.Lock()
	mt, ok := e.torrents[hash]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("engine: %s is not managed by this engine", hash)
	}
	mt.category = category
	err := e.saveManifestLocked()
	e.mu.Unlock()
	return err
}

// SetTags replaces hash's tag set entirely (not a merge — pass the full
// list you want). Persists immediately.
func (e *Engine) SetTags(hash metainfo.Hash, tags []string) error {
	e.mu.Lock()
	mt, ok := e.torrents[hash]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("engine: %s is not managed by this engine", hash)
	}
	mt.tags = append([]string(nil), tags...)
	err := e.saveManifestLocked()
	e.mu.Unlock()
	return err
}

// AddTracker adds url to hash's tracker list at runtime — a thin fleet-level
// wrapper around Torrent.AddTracker (3.6), for the control API (a control
// API client has no other way to reach a specific managed *torrent.Torrent
// directly).
func (e *Engine) AddTracker(hash metainfo.Hash, url string) error {
	tr, ok := e.Get(hash)
	if !ok {
		return fmt.Errorf("engine: %s is not managed by this engine", hash)
	}
	return tr.AddTracker(url)
}

// SetSequential switches hash between rarest-first and sequential piece
// ordering at runtime — a thin fleet-level wrapper around
// Torrent.SetSequential, same reasoning as AddTracker above.
func (e *Engine) SetSequential(hash metainfo.Hash, sequential bool) error {
	tr, ok := e.Get(hash)
	if !ok {
		return fmt.Errorf("engine: %s is not managed by this engine", hash)
	}
	return tr.SetSequential(sequential)
}
