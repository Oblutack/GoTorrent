package metainfo

import (
	"encoding/hex"
	"fmt"
)

// Hash256Size is the length of a SHA-256 digest in bytes.
const Hash256Size = 32

// Hash256 is a 32-byte SHA-256 digest — a BitTorrent v2 (BEP 52) infohash,
// a file's "pieces root", or a "piece layers" key. A distinct type from
// Hash (v1's 20-byte SHA-1) rather than reusing internal/merkle.Hash
// directly, the same "a type crossing a package boundary gets its own
// name" discipline internal/api's DTOs already follow — nothing stops a
// caller converting between the two where they meet (both are plain
// [32]byte underneath), but neither package needs to know the other's
// type name.
type Hash256 [Hash256Size]byte

// String returns the lowercase hex form, which is how a v2 infohash
// appears in an xt=urn:btmh: magnet link (after the multihash prefix).
func (h Hash256) String() string {
	return hex.EncodeToString(h[:])
}

// IsZero reports whether the hash is unset.
func (h Hash256) IsZero() bool {
	return h == Hash256{}
}

// MarshalText and UnmarshalText mirror Hash's own reasoning: a fixed-size
// byte array would otherwise serialize as a JSON array of 32 numbers.
func (h Hash256) MarshalText() ([]byte, error) {
	return []byte(h.String()), nil
}

func (h *Hash256) UnmarshalText(text []byte) error {
	parsed, err := ParseHash256(string(text))
	if err != nil {
		return err
	}
	*h = parsed
	return nil
}

// ParseHash256 decodes a 64-character hex v2 infohash.
func ParseHash256(s string) (Hash256, error) {
	var h Hash256
	if len(s) != 2*Hash256Size {
		return h, fmt.Errorf("metainfo: v2 infohash must be %d hex characters, got %d", 2*Hash256Size, len(s))
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return h, fmt.Errorf("metainfo: invalid v2 infohash %q: %w", s, err)
	}
	copy(h[:], raw)
	return h, nil
}

// Hash256From builds a Hash256 from exactly Hash256Size bytes.
func Hash256From(b []byte) (Hash256, error) {
	var h Hash256
	if len(b) != Hash256Size {
		return h, fmt.Errorf("metainfo: v2 hash must be %d bytes, got %d", Hash256Size, len(b))
	}
	copy(h[:], b)
	return h, nil
}

// Truncated20 returns the first 20 bytes of h as a v1-shaped Hash — BEP
// 52's own "for some uses as torrent identifier it is truncated to 20
// bytes" rule, used for the peer-wire handshake and tracker announces of
// a v2-only torrent, both of which are still defined in terms of a
// 20-byte infohash.
func (h Hash256) Truncated20() Hash {
	var t Hash
	copy(t[:], h[:HashSize])
	return t
}
