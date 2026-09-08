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
