package mse

import (
	"crypto/rc4"
	"io"
	"net"
)

// newCipher builds an RC4 stream keyed as MSE/PE requires and discards its
// first 1024 keystream bytes before returning it, as the spec mandates —
// RC4's earliest output bytes are statistically weak, and burning them is
// cheap insurance the protocol bakes in rather than leaves optional.
func newCipher(key []byte) (*rc4.Cipher, error) {
	c, err := rc4.NewCipher(key)
	if err != nil {
		return nil, err
	}
	var burn [1024]byte
	c.XORKeyStream(burn[:], burn[:])
	return c, nil
}

// Conn wraps a net.Conn once an MSE negotiation completes. It is returned
// by both InitiateHandshake and ReceiveHandshake for both the RC4 and
// Plaintext outcomes — a nil cipher just means "don't XOR," so callers
// downstream (internal/peer, internal/engine) never need to know or care
// which method was actually negotiated.
//
// Read always goes through r, never the embedded net.Conn's Read
// directly: r may be a *bufio.Reader that buffered bytes during the
// handshake (already off the wire, not yet consumed) — reading from the
// embedded Conn directly here would silently drop them.
//
// pending holds bytes that are already plaintext and must bypass the
// decrypt step entirely — specifically, on the receiver side, a real
// initiator's classic handshake bundled as MSE's optional IA field. Those
// bytes were necessarily decrypted once already, at their correct
// keystream position, while ReceiveHandshake parsed the rest of packet 3
// (it has to be, to even learn IA's own length) — an RC4 keystream is
// strictly ordered and cannot be "rewound" to decrypt them a second time
// through Read's own XOR step the way a fresh, still-encrypted byte from
// the wire needs to be. A first version of this type fed pending bytes
// back through the same decrypt cipher via an io.MultiReader instead,
// which double-decrypted them against the keystream's now-advanced
// position and produced garbage — caught immediately by
// TestHandshakeReceiverSeesABundledIA, not by inspection.
type Conn struct {
	net.Conn
	pending []byte
	r       io.Reader
	encrypt *rc4.Cipher // nil => Write is a plain passthrough
	decrypt *rc4.Cipher // nil => Read is a plain passthrough
}

func (c *Conn) Read(p []byte) (int, error) {
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	n, err := c.r.Read(p)
	if n > 0 && c.decrypt != nil {
		c.decrypt.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

func (c *Conn) Write(p []byte) (int, error) {
	if c.encrypt == nil {
		return c.Conn.Write(p)
	}
	buf := make([]byte, len(p))
	c.encrypt.XORKeyStream(buf, p)
	return c.Conn.Write(buf)
}
