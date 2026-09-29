package mse

import (
	"crypto/rand"
	"math/big"
)

// dhP is the 768-bit prime MSE/PE's Diffie-Hellman exchange is defined
// over — a fixed constant of the scheme, not something a real
// implementation ever chooses itself. Cross-checked byte-for-byte against
// two independent real-world sources during implementation (see doc.go).
var dhP = mustHexBig("FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245E485B576625E7EC6F44C42E9A63A36210000000000090563")

// dhG is the generator.
var dhG = big.NewInt(2)

// dhByteLen is the fixed width every DH public value (Y) and the shared
// secret (S) are encoded to — on the wire, and before being fed into any
// HASH/keyA/keyB construction — left-padded with zero bytes. This is the
// single detail most likely to silently break interop if guessed wrong: a
// variable/minimal-length encoding (e.g. Go's plain big.Int.Bytes()) would
// still "work" against another instance of this same package but produce
// completely different bytes than a real peer expects whenever the actual
// numeric value happens to have a leading zero byte. Confirmed against a
// second, independently-authored implementation's source during research,
// not assumed.
const dhByteLen = 96

func mustHexBig(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 16)
	if !ok {
		panic("mse: invalid hex constant")
	}
	return n
}

// keypair is one side's DH private/public value pair.
type keypair struct {
	priv *big.Int
	pub  *big.Int
}

// generateKeypair picks a private key of the spec's own recommended size
// — 160 bits ("at least 128 bits long; using more than 180 bits is not
// believed to add further security") — and computes the corresponding
// public value.
func generateKeypair() (keypair, error) {
	buf := make([]byte, 20) // 160 bits
	if _, err := rand.Read(buf); err != nil {
		return keypair{}, err
	}
	priv := new(big.Int).SetBytes(buf)
	if priv.Sign() == 0 {
		// Vanishingly unlikely (2^-160), but a zero private key would make
		// every public value 1 — defend against it rather than trust the
		// odds never come up.
		priv.SetInt64(1)
	}
	pub := new(big.Int).Exp(dhG, priv, dhP)
	return keypair{priv: priv, pub: pub}, nil
}

// encodeFixed renders n as exactly dhByteLen big-endian bytes, left-padded
// with zeroes — see dhByteLen's own doc comment for why this exact width
// matters.
func encodeFixed(n *big.Int) []byte {
	out := make([]byte, dhByteLen)
	b := n.Bytes()
	copy(out[dhByteLen-len(b):], b)
	return out
}

// sharedSecret computes S = peerPub^priv mod P, fixed-width encoded.
func sharedSecret(priv, peerPub *big.Int) []byte {
	s := new(big.Int).Exp(peerPub, priv, dhP)
	return encodeFixed(s)
}
