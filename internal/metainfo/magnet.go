package metainfo

import (
	"encoding/base32"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ErrNotAMagnetURI is returned by ParseMagnet when the string does not use
// the magnet: scheme at all.
var ErrNotAMagnetURI = errors.New("metainfo: not a magnet URI")

// Magnet is a parsed magnet link (BEP 9's magnet: scheme). It carries only
// what identifies a torrent and where to start looking for it — the info
// dictionary itself has to come from a peer (BEP 9) or a .torrent file.
type Magnet struct {
	// InfoHash identifies the torrent. Always set on a successful parse.
	InfoHash Hash
	// DisplayName is the dn= hint. It is exactly that — a hint from
	// whoever authored the link, unverified against anything — so it must
	// never be trusted the way a name from the (hash-verified) info
	// dictionary can be.
	DisplayName string
	// Trackers is every tr= parameter, in the order they appeared.
	Trackers []string
	// WebSeeds is every ws= parameter (BEP 19), unused until Phase 3.
	WebSeeds []string
	// PeerAddrs is every x.pe= parameter: "host:port" hints for peers to
	// dial directly, bypassing tracker/DHT discovery entirely.
	PeerAddrs []string
	// SelectedFiles is the so= parameter (BEP 53) expanded into individual
	// 0-based file indices. Nil means "no preference stated" — every file
	// selected, or the very concept of files, not yet known.
	SelectedFiles []int
	// HasV2 records whether a urn:btmh: topic was present alongside (or
	// instead of) the v1 urn:btih: one. When true, InfoHashV2 is set.
	HasV2 bool
	// InfoHashV2 is the BEP 52 v2 infohash decoded from a urn:btmh:1220...
	// topic, valid only when HasV2 is true. For a hybrid magnet (both
	// topics present) this is the same torrent's v2 identity alongside
	// InfoHash's v1 one; for a v2-only magnet (no urn:btih: at all),
	// InfoHash is the zero value and InfoHashV2 alone identifies the
	// torrent — torrent.NewFromInfoHashV2 is the constructor for that case.
	InfoHashV2 Hash256
}

// ParseMagnet parses a magnet: URI. It requires at least one usable
// identity — a v1 (urn:btih:) topic, a v2 (urn:btmh:) topic, or both
// (hybrid) — and rejects a magnet with neither.
func ParseMagnet(uri string) (*Magnet, error) {
	// url.Parse handles "magnet:?xt=..." as an opaque URI with the query
	// string still split out correctly — RawQuery is scheme-agnostic in the
	// standard library, so this needs no special-casing.
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("metainfo: invalid magnet URI: %w", err)
	}
	if u.Scheme != "magnet" {
		return nil, ErrNotAMagnetURI
	}
	q := u.Query()

	m := &Magnet{}
	haveV1 := false
	for _, xt := range q["xt"] {
		hashV1, hashV2, isV1, isV2, err := parseExactTopic(xt)
		if err != nil {
			return nil, err
		}
		if isV1 {
			m.InfoHash = hashV1
			haveV1 = true
		}
		if isV2 {
			m.InfoHashV2 = hashV2
			m.HasV2 = true
		}
	}
	if !haveV1 && !m.HasV2 {
		return nil, errors.New("metainfo: magnet URI has no xt=urn:btih: or xt=urn:btmh: parameter")
	}

	m.DisplayName = q.Get("dn")
	m.Trackers = q["tr"]
	m.WebSeeds = q["ws"]
	m.PeerAddrs = q["x.pe"]

	if so := q.Get("so"); so != "" {
		sel, err := parseSelectedFiles(so)
		if err != nil {
			return nil, fmt.Errorf("metainfo: invalid 'so' parameter: %w", err)
		}
		m.SelectedFiles = sel
	}

	return m, nil
}

// sha256MultihashPrefix is the multihash code+length prefix BEP 52's own
// urn:btmh: topic uses: 0x12 (SHA-256) + 0x20 (32-byte length), hex-encoded.
// This client only ever recognizes this one multihash shape — the only one
// BEP 52 itself defines — and treats anything else as an error rather than
// silently ignoring a hash it can't act on.
const sha256MultihashPrefix = "1220"

// parseExactTopic decodes one xt= value. An xt topic this client does not
// recognise at all (neither btih nor btmh) is not an error by itself — it is
// simply not counted toward haveV1/HasV2 — since a magnet may carry
// namespaces meant for other clients.
func parseExactTopic(raw string) (hashV1 Hash, hashV2 Hash256, isV1, isV2 bool, err error) {
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "urn:btih:"):
		h := raw[len("urn:btih:"):]
		switch len(h) {
		case 2 * HashSize: // 40 hex characters
			hashV1, err = ParseHash(h)
		case 32: // base32, no padding: 32 chars * 5 bits = 160 bits = 20 bytes
			hashV1, err = parseBase32Hash(h)
		default:
			err = fmt.Errorf("metainfo: xt btih value %q has %d characters, want %d (hex) or 32 (base32)",
				h, len(h), 2*HashSize)
		}
		if err != nil {
			return Hash{}, Hash256{}, false, false, err
		}
		return hashV1, Hash256{}, true, false, nil

	case strings.HasPrefix(lower, "urn:btmh:"):
		h := raw[len("urn:btmh:"):]
		wantLen := len(sha256MultihashPrefix) + 2*Hash256Size
		if len(h) != wantLen {
			return Hash{}, Hash256{}, false, false, fmt.Errorf(
				"metainfo: xt btmh value %q has %d characters, want %d (multihash prefix %s + %d hex)",
				h, len(h), wantLen, sha256MultihashPrefix, 2*Hash256Size)
		}
		if !strings.HasPrefix(strings.ToLower(h), sha256MultihashPrefix) {
			return Hash{}, Hash256{}, false, false, fmt.Errorf(
				"metainfo: xt btmh value %q does not use the SHA-256 multihash prefix %q this client supports",
				h, sha256MultihashPrefix)
		}
		hashV2, err = ParseHash256(h[len(sha256MultihashPrefix):])
		if err != nil {
			return Hash{}, Hash256{}, false, false, fmt.Errorf("metainfo: invalid btmh hash %q: %w", h, err)
		}
		return Hash{}, hashV2, false, true, nil

	default:
		return Hash{}, Hash256{}, false, false, nil
	}
}

func parseBase32Hash(s string) (Hash, error) {
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(s))
	if err != nil {
		return Hash{}, fmt.Errorf("metainfo: invalid base32 infohash %q: %w", s, err)
	}
	return HashFrom(raw)
}

// parseSelectedFiles expands a BEP 53 so= value ("0,2,4-8") into individual
// indices. Ranges are inclusive on both ends, per the BEP.
func parseSelectedFiles(s string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		before, after, isRange := strings.Cut(part, "-")
		if !isRange {
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid file index %q", part)
			}
			out = append(out, n)
			continue
		}
		lo, err := strconv.Atoi(before)
		if err != nil || lo < 0 {
			return nil, fmt.Errorf("invalid range start in %q", part)
		}
		hi, err := strconv.Atoi(after)
		if err != nil || hi < lo {
			return nil, fmt.Errorf("invalid range end in %q", part)
		}
		for i := lo; i <= hi; i++ {
			out = append(out, i)
		}
	}
	return out, nil
}
