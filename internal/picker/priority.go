package picker

import "fmt"

// Priority is a piece's download priority, derived from the priority of
// whichever file(s) it belongs to — see SetPriorities.
type Priority uint8

const (
	// PrioritySkip pieces are never picked: SetPriorities is how a file
	// selection ("don't download this file") reaches the picker.
	PrioritySkip Priority = iota
	PriorityLow
	PriorityNormal
	PriorityHigh
)

func (p Priority) String() string {
	switch p {
	case PrioritySkip:
		return "skip"
	case PriorityLow:
		return "low"
	case PriorityNormal:
		return "normal"
	case PriorityHigh:
		return "high"
	default:
		return fmt.Sprintf("Priority(%d)", uint8(p))
	}
}

// priorityTiers is every non-skip priority, highest first — the order
// nextPiece walks them in.
var priorityTiers = [...]Priority{PriorityHigh, PriorityNormal, PriorityLow}

// SetPriorities sets every piece's priority at once — normally computed by
// the caller from per-file priorities and file/piece geometry (see
// internal/torrent, which owns that mapping since it is the one place file
// layout and picker both meet). A piece already in progress when it drops
// to PrioritySkip is not evicted: whatever blocks are already in flight for
// it are left to finish rather than wasted, but no new work is ever started
// on a skip-priority piece — see nextPiece.
//
// Passing nil (or never calling this) leaves every piece at the implicit
// default of PriorityNormal, and Remaining/Complete use a faster path that
// assumes nothing is ever skipped — the common case, a torrent with no file
// selection at all, pays nothing for a feature it isn't using.
func (p *Picker) SetPriorities(pr []Priority) error {
	if len(pr) != p.cfg.NumPieces {
		return fmt.Errorf("picker: got %d piece priorities, torrent has %d pieces", len(pr), p.cfg.NumPieces)
	}
	p.priority = append(p.priority[:0], pr...)
	return nil
}
