package mse

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"math/big"
)

// randomPadLen picks a uniform random length in [0, max].
func randomPadLen(max int) (int, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)+1))
	if err != nil {
		return 0, err
	}
	return int(n.Int64()), nil
}

// randomPad builds one of the handshake's PadA/PadB/PadC/PadD fields: a
// random length in [0, max] of random bytes. Its content is never
// interpreted by either side — only its length-obfuscating presence
// matters — but real random bytes (not e.g. all-zero) are used anyway,
// since a fixed, recognizable pattern in the padding would itself be a
// weaker obfuscation than the scheme intends.
func randomPad(max int) ([]byte, error) {
	n, err := randomPadLen(max)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeUint16(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}

func writeUint32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}
