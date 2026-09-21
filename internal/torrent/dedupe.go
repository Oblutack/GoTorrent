package torrent

// ApplyDedupedPiece writes index's bytes straight to this torrent's storage
// from data already sitting on this machine — Phase 8's cross-torrent
// dedupe, "download once, reuse everywhere" for a piece another managed
// torrent already verified with the exact same content hash. See
// internal/engine's dedupe.go for where data actually comes from and how a
// match is found in the first place; this method only ever applies one
// already-matched piece, it never searches for one itself.
func (t *Torrent) ApplyDedupedPiece(index int, data []byte) error {
	resp := make(chan error, 1)
	select {
	case t.control <- controlMsg{kind: ctrlApplyDedupedPiece, dedupeIndex: index, dedupeData: data, errReply: resp}:
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
