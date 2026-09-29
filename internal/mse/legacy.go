package mse

import (
	"bufio"
	"bytes"
)

// classicPrefix is the first 20 bytes of every classic (unencrypted) BT
// handshake: pstrlen (19) followed by the literal protocol string. An MSE
// Ya value is effectively random 96 bytes, so comparing the full 20 bytes
// (rather than just the first, pstrlen, byte) makes misclassifying a real
// MSE connection as legacy astronomically unlikely rather than merely
// unlikely (~1/256 for a 1-byte sniff) — the same 20 bytes are needed to
// actually read a classic handshake anyway, so there is no extra cost to
// checking all of them.
var classicPrefix = append([]byte{19}, []byte("BitTorrent protocol")...)

// LooksLikeHandshake peeks (without consuming) whether the next bytes on r
// are a classic plaintext BitTorrent handshake, so a caller accepting an
// inbound connection can decide whether to route it as legacy or attempt
// it as MSE.
func LooksLikeHandshake(r *bufio.Reader) (bool, error) {
	peeked, err := r.Peek(len(classicPrefix))
	if err != nil {
		// A short read (fewer than 20 bytes ever arrive) can't be a valid
		// classic handshake either way — treat it as "not legacy" rather
		// than surfacing the read error here, so the caller's own
		// subsequent read attempt (whichever path it takes) is what
		// actually reports the real failure.
		return false, nil
	}
	return bytes.Equal(peeked, classicPrefix), nil
}
