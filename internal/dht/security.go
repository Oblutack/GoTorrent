package dht

import (
	"crypto/rand"
	"fmt"
	"hash/crc32"
	"net"
)

// castagnoliTable is BEP 42's own choice of CRC32 variant (Castagnoli, not
// the IEEE polynomial hash/crc32 defaults to) — the same choice, and the
// same reason to be explicit about it, as BEP 40's CanonicalPriority
// (internal/peer/canonicalpriority.go): the wrong polynomial produces a
// value that looks like a real checksum but never matches another
// compliant client's.
var castagnoliTable = crc32.MakeTable(crc32.Castagnoli)

// v4Mask and v6Mask are BEP 42's own bit masks, applied to an IP address
// before hashing it — coarsening the address to (roughly) its containing
// subnet, so a node's ID stays valid even behind a NAT or after a minor
// renumbering, and so the scheme doesn't just degenerate into "hash the
// exact address." IPv6 only ever hashes its first 8 bytes (a /64, the
// spec's own granularity — matches how IPv6 is actually allocated in
// practice), never the full 16.
var (
	v4Mask = []byte{0x03, 0x0f, 0x3f, 0xff}
	v6Mask = []byte{0x01, 0x03, 0x07, 0x0f, 0x1f, 0x3f, 0x7f, 0xff}
)

// maskForIP returns the mask to use and the number of leading bytes of ip
// it applies to (4 for an IPv4 address, 8 for the first /64 of an IPv6
// one), or ok=false for an address this scheme has no defined behavior
// for (this should not happen for anything net.IP itself would call valid,
// but a defensive false beats a panic on an unexpected length).
func maskForIP(ip net.IP) (mask []byte, octets int, ok bool) {
	if ip4 := ip.To4(); ip4 != nil {
		return v4Mask, 4, true
	}
	if ip16 := ip.To16(); ip16 != nil {
		return v6Mask, 8, true
	}
	return nil, 0, false
}

// nodeIDCRCPrefix computes BEP 42's deterministic portion of a node ID for
// ip, given the low-order randomization byte r (the spec's own "rand",
// 0-255 — only its bottom 3 bits actually feed the hash; the full byte is
// what ends up stored verbatim as the ID's last byte). Returns the CRC32C
// checksum of the masked address with r's bottom 3 bits folded into its
// top byte, exactly as BEP 42's own C listing computes it — this is the
// one piece of real, checkable math in the whole scheme, split out so both
// generation and verification share it rather than risking the two
// silently drifting apart.
func nodeIDCRCPrefix(ip net.IP, r byte) (uint32, error) {
	mask, octets, ok := maskForIP(ip)
	if !ok {
		return 0, fmt.Errorf("dht: %v is not a usable IPv4 or IPv6 address for BEP 42", ip)
	}
	masked := make([]byte, octets)
	src := ip.To4()
	if src == nil {
		src = ip.To16()
	}
	for i := 0; i < octets; i++ {
		masked[i] = src[i] & mask[i]
	}
	masked[0] |= (r & 0x7) << 5
	return crc32.Checksum(masked, castagnoliTable), nil
}

// GenerateNodeIDForIP implements BEP 42 (DHT Security Extension): derives
// a node ID tied to ip, the way this client's own externally-visible
// address should generate its DHT identity once that address is known
// (see Config.ExternalIP) rather than a purely random one. A compliant ID
// lets any *other* node that itself checks BEP 42 (VerifyNodeID) see this
// client as legitimate rather than a suspicious, arbitrarily-chosen
// identity planted near a target key — the classic Sybil-attack shape
// BEP 42 exists to raise the cost of. Never a downside for a peer that
// doesn't check: the result is still a full 160-bit value indistinguishable
// from an ordinary random ID to anything not specifically validating it.
//
// **Verified against BEP 42's own 5 published test vectors** (IP, rand,
// and the resulting ID's deterministic bytes) — see the test file. Only
// bytes 0, 1, the top 5 bits of byte 2, and byte 19 are ever deterministic
// (byte 19 is r itself, stored verbatim); everything else, including the
// bottom 3 bits of byte 2, is genuinely random per the spec's own C
// listing and gets a fresh crypto/rand draw here, not a fixed value.
func GenerateNodeIDForIP(ip net.IP) (NodeID, error) {
	var id NodeID
	var rBuf [1]byte
	if _, err := rand.Read(rBuf[:]); err != nil {
		return id, fmt.Errorf("dht: generating BEP 42 random byte: %w", err)
	}
	r := rBuf[0]

	crc, err := nodeIDCRCPrefix(ip, r)
	if err != nil {
		return id, err
	}

	if _, err := rand.Read(id[2:19]); err != nil {
		return id, fmt.Errorf("dht: generating BEP 42 random padding: %w", err)
	}
	id[0] = byte(crc >> 24)
	id[1] = byte(crc >> 16)
	id[2] = (byte(crc>>8) & 0xf8) | (id[2] & 0x7)
	id[19] = r
	return id, nil
}

// isExemptFromVerification reports whether ip is one of BEP 42's own
// explicitly listed exemptions (RFC 1918 private ranges, RFC 3927 link-
// local, and loopback) — addresses a node's ID can never meaningfully be
// derived from in the first place, since they say nothing about how that
// node is actually reachable on the real internet.
func isExemptFromVerification(ip net.IP) bool {
	for _, cidr := range []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"127.0.0.0/8",
	} {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue // unreachable: every entry above is a valid literal CIDR
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// VerifyNodeID reports whether id is a plausible BEP 42 identity for a
// node observed at ip — recomputing the same deterministic bytes
// GenerateNodeIDForIP produces (using id's own trailing byte as r, exactly
// as any node applying this check must: the sender never sends r
// separately, only the ID it implies) and comparing them. Always true for
// an address in isExemptFromVerification's list, per the spec's own
// exemptions, and always true for a malformed/unrecognized IP - failing
// open, since this is a plausibility heuristic layered on top of a DHT
// that must keep working even for the (currently large) majority of real
// nodes that don't implement BEP 42 at all.
//
// **Deliberately not wired into any live trust decision yet** - unlike
// BEP 40's CanonicalPriority, which had exactly one concrete, safe
// recommendation from the spec text (rank discovered peers before
// dialing), BEP 42's own recommendation ("responses whose node ID doesn't
// match its IP should be considered to not contain a token") would, if
// applied unconditionally today, discard the announce token from nearly
// every real DHT peer - most of the live mainline DHT does not implement
// this extension, and an ID that simply looks random is indistinguishable
// from one BEP 42 never asked to be checked. Enforcing it now would
// measurably hurt this client's own DHT announceability for a security
// property the wider network mostly doesn't provide yet, the same "real
// motivating use case, deliberately left unimplemented rather than
// guessed at" call BEP 40's own peer-connection-arbitration case already
// made. VerifyNodeID exists, real and tested, for whenever a caller with
// an actual use for it - logging, a future opt-in strict mode - needs it.
func VerifyNodeID(id NodeID, ip net.IP) bool {
	if ip == nil || isExemptFromVerification(ip) {
		return true
	}
	crc, err := nodeIDCRCPrefix(ip, id[19])
	if err != nil {
		return true // can't evaluate this address at all; fail open
	}
	return id[0] == byte(crc>>24) &&
		id[1] == byte(crc>>16) &&
		(id[2]&0xf8) == (byte(crc>>8)&0xf8)
}
