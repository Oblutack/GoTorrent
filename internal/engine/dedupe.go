package engine

import (
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// dedupeLocation is where a piece with a given content hash was last known
// to be verified and on disk — owner plus that torrent's own index for it,
// since two torrents sharing a piece's exact content need not agree on
// where in their own piece list it falls (different preceding files,
// different file ordering, ...). Piece hash equality is what makes the
// index meaningful at all: SHA-1 makes it implausible two genuinely
// different byte ranges collide, so a hash match is treated as proof the
// content really is identical, not just probably so.
type dedupeLocation struct {
	owner metainfo.Hash
	index int
}

// recordDedupeSource notes that owner's piece index just verified, so any
// other managed torrent that turns out to want this exact piece's content
// can copy it locally instead of asking a peer for it. Called from
// OnPieceVerified, which fires on the owning torrent's own actor goroutine —
// this only ever touches dedupeMu and a plain map write, never calls back
// into any Torrent, so (like broadcast) it needs no detached goroutine of
// its own to stay safe there.
func (e *Engine) recordDedupeSource(owner metainfo.Hash, index int) {
	tr, ok := e.Get(owner)
	if !ok {
		return
	}
	mi := tr.Metadata()
	if mi == nil || index < 0 || index >= len(mi.PieceHashes) {
		return
	}
	e.dedupeMu.Lock()
	e.dedupeIndex[mi.PieceHashes[index]] = dedupeLocation{owner: owner, index: index}
	e.dedupeMu.Unlock()
}

// publishAllDedupeSources records every piece hash's torrent currently has
// as verified, in one pass — the counterpart to recordDedupeSource's
// per-piece version, and the only way a torrent that reached its current
// have-bitfield without ever calling OnPieceVerified (a bulk verify at
// CheckingFiles finding correct data already on disk, or resume data
// trusted outright — both call Picker.SetHave directly, never per piece)
// ever gets its pieces into the index at all. Called on reaching
// Downloading or Seeding; a torrent already covered piece-by-piece by
// recordDedupeSource just gets redundantly (harmlessly) re-recorded.
func (e *Engine) publishAllDedupeSources(hash metainfo.Hash) {
	tr, ok := e.Get(hash)
	if !ok {
		return
	}
	mi := tr.Metadata()
	if mi == nil {
		return
	}
	have := tr.HaveBitfield()

	e.dedupeMu.Lock()
	defer e.dedupeMu.Unlock()
	for index := 0; index < mi.NumPieces(); index++ {
		if have.Has(index) {
			e.dedupeIndex[mi.PieceHashes[index]] = dedupeLocation{owner: hash, index: index}
		}
	}
}

// applyDedupe scans hash's still-missing pieces against the fleet-wide
// dedupe index and copies whatever already-verified matches it finds
// straight from the other torrent's own storage — no network involved at
// all for those pieces. Best-effort throughout: any failure (the source
// torrent no longer has the piece, a read error, a length mismatch that
// would mean the hash match was somehow spurious) is logged and skipped
// rather than treated as fatal, since a normal peer-driven download still
// covers every piece this pass doesn't manage to shortcut. Called from
// OnStateChange on reaching StateDownloading — after openMetadata has
// already built real storage and a real have-bitfield to compare against —
// via a detached goroutine, since ApplyExternalPiece calls back into tr.
func (e *Engine) applyDedupe(hash metainfo.Hash) {
	tr, ok := e.Get(hash)
	if !ok {
		return
	}
	mi := tr.Metadata()
	if mi == nil {
		return
	}
	have := tr.HaveBitfield()

	for index := 0; index < mi.NumPieces(); index++ {
		if have.Has(index) {
			continue
		}
		e.dedupeMu.Lock()
		loc, found := e.dedupeIndex[mi.PieceHashes[index]]
		e.dedupeMu.Unlock()
		if !found || loc.owner == hash {
			continue
		}

		srcTr, ok := e.Get(loc.owner)
		if !ok {
			continue
		}
		srcMi := srcTr.Metadata()
		if srcMi == nil || !srcTr.HaveBitfield().Has(loc.index) {
			continue // stale index entry - the source no longer has this piece
		}

		length := srcMi.PieceLen(loc.index)
		data := make([]byte, length)
		srcOffset := int64(loc.index) * srcMi.Info.PieceLength
		n, err := srcTr.ReadAt(data, srcOffset)
		if err != nil || int64(n) != length {
			continue
		}

		if err := tr.ApplyExternalPiece(index, data); err != nil {
			logger.Warning.Printf("engine: dedupe: applying piece %d of %s from %s: %v\n", index, hash, loc.owner, err)
		}
	}
}
