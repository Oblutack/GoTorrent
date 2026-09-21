package peer

import (
	"bytes"
	"hash/crc32"
	"net"
)

// castagnoliTable is BEP 40's own choice of CRC32 variant (Castagnoli, not
// the more common IEEE polynomial `hash/crc32.IEEETable` most Go code
// defaults to) — using the wrong one would silently produce a value that
// looks like a real checksum but never matches another compliant client's.
var castagnoliTable = crc32.MakeTable(crc32.Castagnoli)

// CanonicalPriority implements BEP 40 (Canonical Peer Priority): a
// deterministic formula every compliant client computes the same way,
// from nothing but two endpoints' own IP:port — no wire-protocol
// negotiation needed. BEP 40's own stated purpose is the "barrier to
// entry" and connection-slot-exhaustion-DDoS problems a swarm gets when
// peers only ever connect in first-come-first-served order forever; its
// own "Notes" section recommends using this specifically for *ranking
// candidate peers before connecting* — see internal/torrent's own use of
// this to order tracker/DHT/PEX-discovered peers before dialing them.
//
// The result is symmetric — CanonicalPriority(a, b) == CanonicalPriority(b,
// a) — since the formula itself sorts the two masked addresses before
// hashing; which argument is "us" and which is "them" does not matter.
//
// **Verification note, worth keeping honest:** the IPv4 masking rules and
// the sort-then-CRC32C(Castagnoli) combination below are verified against
// BEP 40's own two published test vectors (see the test file) — genuine
// confidence, not a guess. The IPv6 masks and the same-IP port fallback
// follow the identical structural pattern the spec describes for those
// cases, but the spec provides no worked test vectors for either, so
// those two paths carry real-but-unverified confidence rather than
// vector-proven confidence.
func CanonicalPriority(aIP net.IP, aPort uint16, bIP net.IP, bPort uint16) uint32 {
	a4, aOK := to4(aIP)
	b4, bOK := to4(bIP)
	if aOK && bOK {
		return canonicalPriorityV4(a4, aPort, b4, bPort)
	}
	a16, b16 := aIP.To16(), bIP.To16()
	if a16 == nil || b16 == nil {
		return 0 // not a valid IP either way; nothing meaningful to compute
	}
	return canonicalPriorityV6(a16, aPort, b16, bPort)
}

func to4(ip net.IP) ([4]byte, bool) {
	v4 := ip.To4()
	if v4 == nil {
		return [4]byte{}, false
	}
	var out [4]byte
	copy(out[:], v4)
	return out, true
}

// canonicalPriorityV4 masks each address per the spec's subnet-overlap
// rule (more of the address is kept the closer together the two peers
// already are, since two peers sharing a /24 would otherwise mostly
// collapse to the same masked value and lose all discriminating power),
// sorts the two masked 4-byte results, and CRC32C's the 8-byte
// concatenation. Falls back to comparing ports instead of IPs when the two
// raw IPs are literally identical (two peers behind the same public
// address) — an IP-only comparison there would be a constant, useless
// value for every pair sharing that address.
func canonicalPriorityV4(a [4]byte, aPort uint16, b [4]byte, bPort uint16) uint32 {
	if a == b {
		return sortedCRC(portBytes(aPort), portBytes(bPort))
	}
	aIP, bIP := net.IP(a[:]), net.IP(b[:])
	var mask [4]byte
	switch {
	case sameSubnet(aIP, bIP, 24):
		mask = [4]byte{0xFF, 0xFF, 0xFF, 0xFF}
	case sameSubnet(aIP, bIP, 16):
		mask = [4]byte{0xFF, 0xFF, 0xFF, 0x55}
	default:
		mask = [4]byte{0xFF, 0xFF, 0x55, 0x55}
	}
	return sortedCRC(applyMask(a[:], mask[:]), applyMask(b[:], mask[:]))
}

// canonicalPriorityV6 mirrors canonicalPriorityV4 for 16-byte addresses,
// using the spec's IPv6 mask tiers (/48 and /56, in place of IPv4's /16
// and /24 — see the package doc comment's verification note: unlike the
// IPv4 path, these exact mask values have no published test vector to
// confirm against).
func canonicalPriorityV6(a net.IP, aPort uint16, b net.IP, bPort uint16) uint32 {
	if a.Equal(b) {
		return sortedCRC(portBytes(aPort), portBytes(bPort))
	}
	var mask [16]byte
	switch {
	case sameSubnet(a, b, 56):
		mask = [16]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55}
	case sameSubnet(a, b, 48):
		mask = [16]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55}
	default:
		mask = [16]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55}
	}
	return sortedCRC(applyMask(a, mask[:]), applyMask(b, mask[:]))
}

func sameSubnet(a, b net.IP, bits int) bool {
	bitLen := len(a) * 8
	if len(a) != len(b) {
		return false
	}
	m := net.CIDRMask(bits, bitLen)
	return a.Mask(m).Equal(b.Mask(m))
}

func applyMask(ip net.IP, mask []byte) []byte {
	out := make([]byte, len(mask))
	for i := range mask {
		out[i] = ip[i] & mask[i]
	}
	return out
}

func portBytes(port uint16) []byte {
	return []byte{byte(port >> 8), byte(port)}
}

// sortedCRC concatenates x and y in ascending byte-order (making the whole
// function order-independent) and returns their CRC32C checksum.
func sortedCRC(x, y []byte) uint32 {
	buf := make([]byte, 0, len(x)+len(y))
	if bytes.Compare(x, y) <= 0 {
		buf = append(buf, x...)
		buf = append(buf, y...)
	} else {
		buf = append(buf, y...)
		buf = append(buf, x...)
	}
	return crc32.Checksum(buf, castagnoliTable)
}
