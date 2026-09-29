package mse

import (
	"bytes"
	"errors"
	"io"
)

// ErrSyncFailed is returned by readUntil when marker never appears within
// the bounded window.
var ErrSyncFailed = errors.New("mse: failed to synchronize on marker within window")

// readUntil reads single bytes from r until the most recently read
// len(marker) bytes equal marker, or window bytes have been read without
// a match — the mechanism both sides of a handshake use to locate a
// boundary whose preceding padding has an unknown, randomly-chosen
// length: B searches for HASH('req1', S) after A's PadA, and A searches
// for the ciphertext pattern its own VC will appear as, after B's PadB.
//
// It stops reading the instant a match is found, leaving r positioned
// exactly on the byte immediately following the marker — critical for A's
// search in particular, since the same cipher instance used to compute
// the searched-for pattern must go on decrypting whatever comes right
// after it, with no gap or overlap in the keystream position.
//
// A simple rolling-buffer linear scan (rather than an incremental
// suffix-matching algorithm like KMP, which some real implementations
// use) is a deliberate simplification: the marker is always short (8 or
// 20 bytes) and the window always small (<=532 bytes), so the asymptotic
// difference is irrelevant at handshake time, and this is easier to
// verify obviously correct.
func readUntil(r io.Reader, marker []byte, window int) error {
	buf := make([]byte, 0, len(marker))
	one := make([]byte, 1)
	for read := 0; read < window; read++ {
		if _, err := io.ReadFull(r, one); err != nil {
			return err
		}
		if len(buf) == len(marker) {
			copy(buf, buf[1:])
			buf = buf[:len(marker)-1]
		}
		buf = append(buf, one[0])
		if len(buf) == len(marker) && bytes.Equal(buf, marker) {
			return nil
		}
	}
	return ErrSyncFailed
}
