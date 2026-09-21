package torrent

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

// streamLookaheadPieces is how many pieces ahead of a streaming read's
// current position stay boosted to PriorityHigh — enough that a modest
// network hiccup doesn't stall playback, small enough that a seek doesn't
// leave a long tail of now-irrelevant high-priority work behind. A fixed
// piece count rather than a duration: this package has no idea what
// bitrate the content actually is, and piece count is the unit the picker
// already reasons in.
const streamLookaheadPieces = 8

// boostStreamWindow raises priorities[i] to PriorityHigh for the
// streamLookaheadPieces pieces starting at off's piece, on top of whatever
// priorities already holds — never lowers anything, and (like
// boostFirstAndLastPiece) never touches a piece that's already Skip, so
// streaming a file never un-skips a different file straddling the same
// piece.
func boostStreamWindow(mi *metainfo.MetaInfo, off int64, priorities []picker.Priority) {
	if off < 0 {
		off = 0
	}
	start := int(off / mi.Info.PieceLength)
	n := len(priorities)
	for i := start; i < n && i < start+streamLookaheadPieces; i++ {
		if priorities[i] != picker.PrioritySkip && priorities[i] < picker.PriorityHigh {
			priorities[i] = picker.PriorityHigh
		}
	}
}

// SetStreamPosition tells this torrent's picker that a consumer (Phase 8's
// internal/stream, a byte-range HTTP server) is currently reading at byte
// offset off in the torrent's flat content space — the same addressing
// ReadAt and a peer upload request already use. It re-derives every
// piece's priority from scratch (file priorities, FirstLastPieceFirst, and
// now this window), exactly like SetFilePriority/SetFirstLastPieceFirst
// already do; cheap enough to call on every piece boundary a streaming read
// crosses, not on every individual Read call — see internal/stream's own
// Content type for that throttling.
func (t *Torrent) SetStreamPosition(off int64) error {
	resp := make(chan error, 1)
	select {
	case t.control <- controlMsg{kind: ctrlSetStreamPosition, streamByteOffset: off, errReply: resp}:
	case <-t.done:
		return ErrClosed
	}
	select {
	case err := <-resp:
		return err
	case <-t.done:
		return ErrClosed
	}
}

// WaitForOffset blocks until the piece covering byte offset off is
// verified, or ctx is done. It polls HaveBitfield() rather than a
// broadcast-wake mechanism — piece verification happens on the order of
// seconds in practice (network-bound), so a 150ms poll is imperceptible to
// a streaming reader and needs no new actor-synchronized wait-registration
// machinery, unlike a proper condition-variable-per-piece design would.
func (t *Torrent) WaitForOffset(ctx context.Context, off int64) error {
	mi := t.mi.Load()
	if mi == nil {
		return errors.New("torrent: no metadata yet")
	}
	index := int(off / mi.Info.PieceLength)
	if t.HaveBitfield().Has(index) {
		return nil
	}
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if t.HaveBitfield().Has(index) {
				return nil
			}
		}
	}
}

// ReadAt reads into p starting at byte offset off in the torrent's flat
// content space, capping the read to the end of off's own piece — a
// deliberate short read rather than spanning multiple pieces in one call,
// which keeps this method from having to reason about a piece boundary
// mid-read; io.Reader callers (Content.Read, in internal/stream) already
// have to tolerate short reads regardless. Safe to call from any goroutine,
// same as hasPieceSafe/readBlockSafe: storage.ReadAt is safe for concurrent
// use by design. Does not itself wait for off's piece to be verified —
// pair with WaitForOffset first, or expect storage's own read-back to
// return whatever bytes happen to be on disk (possibly not yet written)
// otherwise.
func (t *Torrent) ReadAt(p []byte, off int64) (int, error) {
	mi := t.mi.Load()
	if mi == nil || t.storage == nil {
		return 0, errors.New("torrent: no data available yet")
	}
	if off < 0 || off >= mi.TotalLength {
		return 0, io.EOF
	}
	index := int(off / mi.Info.PieceLength)
	pieceStart := int64(index) * mi.Info.PieceLength
	pieceEnd := pieceStart + mi.PieceLen(index)

	max := pieceEnd - off
	if int64(len(p)) > max {
		p = p[:max]
	}
	return t.storage.ReadAt(p, off)
}
