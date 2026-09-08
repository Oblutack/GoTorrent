package engine

import (
	"fmt"
	"os"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// SeedLimitAction chooses what happens, beyond the pause that always
// happens first, when a torrent's SeedRatioLimit/SeedTimeLimit is reached
// (3.4). The pause is unconditional and handled entirely inside the
// torrent actor (peers disconnected, checkpointed, tracker told) before
// any of these ever run — an action here is purely additive.
type SeedLimitAction string

const (
	// SeedLimitActionPause is the default: nothing beyond the automatic
	// pause.
	SeedLimitActionPause SeedLimitAction = ""
	// SeedLimitActionRemove drops the torrent from the managed fleet
	// (Engine.Remove) but leaves its downloaded data on disk.
	SeedLimitActionRemove SeedLimitAction = "remove"
	// SeedLimitActionRemoveDeleteData removes the torrent and deletes its
	// downloaded data (Engine.RemoveAndDeleteData).
	SeedLimitActionRemoveDeleteData SeedLimitAction = "remove-delete-data"
)

// applySeedLimitAction runs Defaults.SeedLimitAction for hash — the
// Torrent.OnSeedLimitReached callback wired in Add/MoveData. Always called
// from a detached goroutine already (the callback registration does that),
// so blocking here (Remove/RemoveAndDeleteData both call the actor's Stop,
// which waits for real shutdown) is fine.
func (e *Engine) applySeedLimitAction(hash metainfo.Hash) {
	var err error
	switch e.defaults.SeedLimitAction {
	case SeedLimitActionRemove:
		err = e.Remove(hash)
	case SeedLimitActionRemoveDeleteData:
		err = e.RemoveAndDeleteData(hash)
	default:
		return
	}
	if err != nil {
		logger.Warning.Printf("engine: seed-limit action %q for %s: %v\n", e.defaults.SeedLimitAction, hash, err)
	}
}

// RemoveAndDeleteData is Remove plus deleting the torrent's downloaded
// content from disk (Torrent.ContentPath() — the single file, or the whole
// wrapping directory for a multi-file torrent) once the actor has actually
// stopped, so nothing is still writing to it when the delete runs.
//
// Refused for a multi-file torrent using storage.LayoutNoSubfolder, whose
// content root is the download directory itself — deleting that could
// delete files that aren't even this torrent's. Same guard MoveData uses,
// for the same reason.
func (e *Engine) RemoveAndDeleteData(hash metainfo.Hash) error {
	e.mu.Lock()
	mt, ok := e.torrents[hash]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("engine: %s is not managed by this engine", hash)
	}

	var contentPath string
	if mi := mt.t.Metadata(); mi != nil {
		contentPath = mt.t.ContentPath()
		if mi.Info.IsMultiFile() && contentPath == mt.downloadDir {
			return fmt.Errorf("engine: %s uses a no-subfolder layout, whose content root is its whole download directory — refusing to delete it automatically", hash)
		}
	}

	if err := e.Remove(hash); err != nil {
		return err
	}
	if contentPath == "" {
		return nil // no metadata ever arrived; nothing on disk to delete
	}
	if err := os.RemoveAll(contentPath); err != nil {
		return fmt.Errorf("engine: deleting %s: %w", contentPath, err)
	}
	return nil
}
