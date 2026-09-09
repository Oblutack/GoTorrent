package metainfo

import (
	"encoding/hex"
	"fmt"
)

// HashSize is the length of a SHA-1 digest in bytes.
const HashSize = 20

// Hash is a 20-byte SHA-1 digest — either a torrent's infohash or a single
// piece hash. It is a value type on purpose: a torrent is identified by its
// infohash, and that identity gets copied into maps, channels and log lines
// constantly.
type Hash [HashSize]byte

// String returns the lowercase hex form, which is how infohashes appear in
// magnet links, tracker URLs and file names.
func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// IsZero reports whether the hash is unset.
func (h Hash) IsZero() bool {
	return h == Hash{}
}

// MarshalText and UnmarshalText make Hash encode as its familiar 40-char
// hex string in JSON (a bare [20]byte would otherwise serialize as an
// array of 20 numbers — encoding/json's []byte-to-base64 special case only
// applies to slices, not fixed-size arrays) and usable as a JSON object
// key, which requires exactly this interface rather than MarshalJSON.
func (h Hash) MarshalText() ([]byte, error) {
	return []byte(h.String()), nil
}

func (h *Hash) UnmarshalText(text []byte) error {
	parsed, err := ParseHash(string(text))
	if err != nil {
		return err
	}
	*h = parsed
	return nil
}

// ParseHash decodes a 40-character hex infohash.
func ParseHash(s string) (Hash, error) {
	var h Hash
	if len(s) != 2*HashSize {
		return h, fmt.Errorf("metainfo: infohash must be %d hex characters, got %d", 2*HashSize, len(s))
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return h, fmt.Errorf("metainfo: invalid infohash %q: %w", s, err)
	}
	copy(h[:], raw)
	return h, nil
}

// HashFrom builds a Hash from exactly HashSize bytes.
func HashFrom(b []byte) (Hash, error) {
	var h Hash
	if len(b) != HashSize {
		return h, fmt.Errorf("metainfo: hash must be %d bytes, got %d", HashSize, len(b))
	}
	copy(h[:], b)
	return h, nil
}
