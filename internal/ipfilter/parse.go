package ipfilter

import (
	"bufio"
	"io"
	"net"
	"strings"
)

// ParseEmuleDAT parses eMule's ipfilter.dat format: one range per line,
// "start - end , level[, description]" (the access level and description
// are optional past the range itself). The access level is parsed as part
// of the line's shape but its value is not acted on — every parsed range
// is treated as blocked outright, matching how modern clients actually
// treat this format in practice; the graduated-severity use of "level" is
// a legacy eMule convention nothing downstream of this package honors.
// Malformed lines are skipped rather than aborting the whole file — a
// blocklist with a handful of bad lines should still load the good ones,
// especially one fetched from a URL this package doesn't control.
func ParseEmuleDAT(r io.Reader) []Range {
	var out []Range
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rangeField, _, _ := strings.Cut(line, ",")
		if rng, ok := parseRangeField(rangeField, "-"); ok {
			out = append(out, rng)
		}
	}
	return out
}

// ParsePeerGuardianP2P parses PeerGuardian's .p2p format:
// "description:start-end" per line, description first (and never
// containing a colon in practice, so splitting on the first one is
// correct). Malformed lines are skipped, same reasoning as ParseEmuleDAT.
func ParsePeerGuardianP2P(r io.Reader) []Range {
	var out []Range
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		_, rangeField, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if rng, ok := parseRangeField(rangeField, "-"); ok {
			out = append(out, rng)
		}
	}
	return out
}

// parseRangeField parses "start<sep>end" (whitespace around either side of
// sep is tolerated) into a Range.
func parseRangeField(field, sep string) (Range, bool) {
	start, end, ok := strings.Cut(field, sep)
	if !ok {
		return Range{}, false
	}
	startIP := parseLenientIP(strings.TrimSpace(start))
	endIP := parseLenientIP(strings.TrimSpace(end))
	if startIP == nil || endIP == nil {
		return Range{}, false
	}
	return Range{Start: startIP, End: endIP}, true
}

// parseLenientIP parses s as an IP, tolerating IPv4 octets padded with
// leading zeros (e.g. "001.002.004.000") — net.ParseIP deliberately
// rejects these since Go 1.17 (hardening against the octal-vs-decimal
// ambiguity a leading zero can imply elsewhere), but real ipfilter.dat
// files use exactly this zero-padded style throughout. There is no octal
// ambiguity to worry about here: this package only ever treats the parsed
// IP as an opaque comparison key, never re-interprets the original digits.
func parseLenientIP(s string) net.IP {
	if ip := net.ParseIP(s); ip != nil {
		return ip
	}
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return nil
	}
	for i, p := range parts {
		trimmed := strings.TrimLeft(p, "0")
		if trimmed == "" {
			trimmed = "0"
		}
		parts[i] = trimmed
	}
	return net.ParseIP(strings.Join(parts, "."))
}
