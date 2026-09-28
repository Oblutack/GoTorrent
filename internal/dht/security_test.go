package dht

import (
	"encoding/hex"
	"net"
	"testing"
)

// TestNodeIDCRCPrefixMatchesBEP42PublishedTestVectors is the real ground
// truth this whole file rests on: BEP 42's own spec page publishes 5
// worked (IP, rand, resulting node ID) triples. Only bytes 0, 1, the top 5
// bits of byte 2, and byte 19 (rand itself) of a published ID are ever
// deterministic - the rest (the bottom 3 bits of byte 2, and all of bytes
// 3-18) are genuinely random per the spec's own C listing, so this checks
// exactly the bits that are actually checkable, not the full 20 bytes.
func TestNodeIDCRCPrefixMatchesBEP42PublishedTestVectors(t *testing.T) {
	cases := []struct {
		ip     string
		rand   byte
		wantID string // full published ID; only its deterministic bits are checked
	}{
		{"124.31.75.21", 1, "5fbfbff10c5d6a4ec8a88e4c6ab4c28b95eee401"},
		{"21.75.31.124", 86, "5a3ce9c14e7a08645677bbd1cfe7d8f956d53256"},
		{"65.23.51.170", 22, "a5d43220bc8f112a3d426c84764f8c2a1150e616"},
		{"84.124.73.14", 65, "1b0321dd1bb1fe518101ceef99462b947a01ff41"},
		{"43.213.53.83", 90, "e56f6cbf5b7c4be0237986d5243b87aa6d51305a"},
	}

	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("net.ParseIP(%q) returned nil", c.ip)
		}
		wantBytes, err := hex.DecodeString(c.wantID)
		if err != nil {
			t.Fatalf("decoding published ID %q: %v", c.wantID, err)
		}

		crc, err := nodeIDCRCPrefix(ip, c.rand)
		if err != nil {
			t.Fatalf("nodeIDCRCPrefix(%s, %d): %v", c.ip, c.rand, err)
		}
		got0 := byte(crc >> 24)
		got1 := byte(crc >> 16)
		got2top5 := byte(crc>>8) & 0xf8

		if got0 != wantBytes[0] || got1 != wantBytes[1] || got2top5 != (wantBytes[2]&0xf8) {
			t.Fatalf("%s rand=%d: got deterministic prefix %02x%02x%02x.., want %02x%02x%02x..",
				c.ip, c.rand, got0, got1, got2top5, wantBytes[0], wantBytes[1], wantBytes[2]&0xf8)
		}
	}
}

// TestGenerateNodeIDForIPProducesAVerifiableID proves the generator and
// verifier agree with each other - a real round trip, not just the two
// sharing nodeIDCRCPrefix by construction (a bug in how either applies it
// could still make them agree with each other while disagreeing with the
// spec, which is exactly what the test above rules out).
func TestGenerateNodeIDForIPProducesAVerifiableID(t *testing.T) {
	ip := net.ParseIP("203.0.113.42")
	id, err := GenerateNodeIDForIP(ip)
	if err != nil {
		t.Fatalf("GenerateNodeIDForIP: %v", err)
	}
	if !VerifyNodeID(id, ip) {
		t.Fatalf("VerifyNodeID rejected an ID GenerateNodeIDForIP just produced for the same IP: %s", id)
	}
}

// TestGenerateNodeIDForIPVariesItsRandomPortion proves GenerateNodeIDForIP
// actually draws fresh randomness each call (the per-call "rand" byte, the
// low 3 bits of byte 2, and all of bytes 3-18) rather than some fixed
// value that happened to pass the round-trip test above by coincidence.
// Both resulting IDs must still independently verify against the same IP
// - each call's own "rand" byte (stored at id[19]) feeds back into its
// own deterministic bytes 0-2, so two calls for the same IP are expected
// to differ there too, not just in the padding.
func TestGenerateNodeIDForIPVariesItsRandomPortion(t *testing.T) {
	ip := net.ParseIP("203.0.113.42")
	id1, err := GenerateNodeIDForIP(ip)
	if err != nil {
		t.Fatalf("GenerateNodeIDForIP (1): %v", err)
	}
	id2, err := GenerateNodeIDForIP(ip)
	if err != nil {
		t.Fatalf("GenerateNodeIDForIP (2): %v", err)
	}
	if id1 == id2 {
		t.Fatal("two GenerateNodeIDForIP calls for the same IP produced identical IDs - the random portion isn't actually random")
	}
	if !VerifyNodeID(id1, ip) || !VerifyNodeID(id2, ip) {
		t.Fatalf("one of two independently-generated IDs failed to verify: %s / %s", id1, id2)
	}
}

func TestVerifyNodeIDRejectsAnIDForTheWrongIP(t *testing.T) {
	id, err := GenerateNodeIDForIP(net.ParseIP("203.0.113.42"))
	if err != nil {
		t.Fatalf("GenerateNodeIDForIP: %v", err)
	}
	if VerifyNodeID(id, net.ParseIP("198.51.100.7")) {
		t.Fatal("VerifyNodeID accepted an ID generated for a completely different IP")
	}
}

// TestVerifyNodeIDExemptsPrivateAndLoopbackAddresses pins BEP 42's own
// published exemption list - a node ID can never meaningfully be derived
// from an address that says nothing about real internet reachability.
func TestVerifyNodeIDExemptsPrivateAndLoopbackAddresses(t *testing.T) {
	// A purely random ID would fail verification against any real IP -
	// using one here is what actually proves the exemption is doing the
	// work, not a coincidentally-valid ID.
	var randomID NodeID
	for i := range randomID {
		randomID[i] = 0xAB
	}

	for _, ip := range []string{
		"10.1.2.3",
		"172.16.5.6",
		"192.168.1.1",
		"169.254.1.1",
		"127.0.0.1",
	} {
		if !VerifyNodeID(randomID, net.ParseIP(ip)) {
			t.Fatalf("VerifyNodeID rejected a request for exempt address %s, want it to pass regardless of the ID", ip)
		}
	}
}

// TestNewDerivesItsOwnIDFromConfigExternalIP proves the actual wiring, not
// just the underlying algorithm: a real *DHT started with Config.ExternalIP
// set has an ID() that verifies against that IP, and one started without
// it (the default, matching every other test in this package) does not -
// a purely random ID has no reason to happen to satisfy BEP 42 for any
// particular IP.
func TestNewDerivesItsOwnIDFromConfigExternalIP(t *testing.T) {
	ip := net.ParseIP("203.0.113.99")
	withIP, err := New(Config{Port: 0, ExternalIP: ip})
	if err != nil {
		t.Fatalf("New with ExternalIP: %v", err)
	}
	defer withIP.Close()
	if !VerifyNodeID(withIP.ID(), ip) {
		t.Fatalf("a node started with Config.ExternalIP = %s has an ID that doesn't verify against it: %s", ip, withIP.ID())
	}

	withoutIP := newTestNode(t)
	if VerifyNodeID(withoutIP.ID(), ip) {
		t.Fatalf("a node started with no ExternalIP happened to produce an ID that verifies against %s - Config.ExternalIP wiring may not be doing anything", ip)
	}
}

func TestVerifyNodeIDForAnIPv6AddressRoundTrips(t *testing.T) {
	ip := net.ParseIP("2001:db8::1")
	id, err := GenerateNodeIDForIP(ip)
	if err != nil {
		t.Fatalf("GenerateNodeIDForIP: %v", err)
	}
	if !VerifyNodeID(id, ip) {
		t.Fatalf("VerifyNodeID rejected an IPv6 ID GenerateNodeIDForIP just produced: %s", id)
	}
	if VerifyNodeID(id, net.ParseIP("2001:db8:ffff::1")) {
		t.Fatal("VerifyNodeID accepted an IPv6 ID against a different /64")
	}
}
