package mse

import "crypto/sha1"

// hash20 is HASH(x) in the spec: the 20-byte SHA-1 digest of the
// concatenation of every argument. SHA-1 is mandated by the protocol
// itself, not a choice made here — the same "protocol-mandated, not a
// vulnerability" reasoning CLAUDE.md already documents for BEP 3's piece
// hashes and RFC 6455's WebSocket handshake.
func hash20(parts ...[]byte) []byte {
	h := sha1.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

func req1(s []byte) []byte    { return hash20([]byte("req1"), s) }
func req2(skey []byte) []byte { return hash20([]byte("req2"), skey) }
func req3(s []byte) []byte    { return hash20([]byte("req3"), s) }

// req2Xor3 is the 20-byte value packet 3 carries so B can identify which
// SKEY (infohash) A intends without the infohash ever appearing on the
// wire in the clear — computed identically on both sides, by A once (it
// already knows its own SKEY) and by B once per candidate SKEY it tries.
func req2Xor3(skey, s []byte) []byte {
	a, b := req2(skey), req3(s)
	out := make([]byte, len(a))
	for i := range out {
		out[i] = a[i] ^ b[i]
	}
	return out
}

// keyA is the RC4 key for A's outgoing / B's incoming stream.
func keyA(s, skey []byte) []byte { return hash20([]byte("keyA"), s, skey) }

// keyB is the RC4 key for B's outgoing / A's incoming stream.
func keyB(s, skey []byte) []byte { return hash20([]byte("keyB"), s, skey) }
