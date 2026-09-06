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
	// instead of) the v1 urn:btih: one. This client cannot act on it yet —
	// no v2 (BitTorrent v2 / BEP 52) support exists — but a hybrid magnet
	// still works fine via InfoHash, and rejecting it outright would be
	// wrong.
	HasV2 bool
}

// ParseMagnet parses a magnet: URI. It requires a usable v1 (urn:btih:)
// topic; a v2-only magnet (urn:btmh: with no urn:btih:) is rejected with a
// clear error rather than silently producing a zero InfoHash, since nothing
// in this client can act on a v2-only identity yet.
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
		hash, isV1, isV2, err := parseExactTopic(xt)
		if err != nil {
			return nil, err
		}
		if isV1 {
			m.InfoHash = hash
			haveV1 = true
		}
		if isV2 {
			m.HasV2 = true
		}
	}
	if !haveV1 {
		if m.HasV2 {
			return nil, errors.New("metainfo: magnet URI is v2-only (urn:btmh:), which this client does not support")
		}
		return nil, errors.New("metainfo: magnet URI has no xt=urn:btih: parameter")
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

// parseExactTopic decodes one xt= value. An xt topic this client does not
// recognise at all (neither btih nor btmh) is not an error by itself — it is
// simply not counted toward haveV1/HasV2 — since a magnet may carry
// namespaces meant for other clients.
func parseExactTopic(raw string) (hash Hash, isV1, isV2 bool, err error) {
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "urn:btih:"):
		h := raw[len("urn:btih:"):]
		switch len(h) {
		case 2 * HashSize: // 40 hex characters
			hash, err = ParseHash(h)
		case 32: // base32, no padding: 32 chars * 5 bits = 160 bits = 20 bytes
			hash, err = parseBase32Hash(h)
		default:
			err = fmt.Errorf("metainfo: xt btih value %q has %d characters, want %d (hex) or 32 (base32)",
				h, len(h), 2*HashSize)
		}
		if err != nil {
			return Hash{}, false, false, err
		}
		return hash, true, false, nil

	case strings.HasPrefix(lower, "urn:btmh:"):
		return Hash{}, false, true, nil

	default:
		return Hash{}, false, false, nil
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
