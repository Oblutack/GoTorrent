package torrent

// ApplyExternalPiece writes index's bytes straight to this torrent's
// storage from data already trusted correct by whichever caller obtained
// it — the shared write-then-verify primitive behind two of Phase 8's
// differentiators: cross-torrent dedupe (internal/engine's dedupe.go finds
// the match, this method applies it) and web seeds (internal/torrent's own
// webseed.go fetches a piece over plain HTTP instead of the BitTorrent wire
// protocol, then applies it the exact same way). Neither caller's data is
// blindly trusted regardless of how it got here — see doApplyExternalPiece
// for the real hash re-verify this still goes through.
func (t *Torrent) ApplyExternalPiece(index int, data []byte) error {
	resp := make(chan error, 1)
	select {
	case t.control <- controlMsg{kind: ctrlApplyExternalPiece, externalPieceIndex: index, externalPieceData: data, errReply: resp}:
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
