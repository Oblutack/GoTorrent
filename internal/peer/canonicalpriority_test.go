package peer

import (
	"fmt"
	"net"
	"testing"
)

// TestCanonicalPriorityMatchesTheOfficialTestVectors is the whole point:
// BEP 40's own spec page publishes two worked examples. Getting these two
// exact 32-bit values is the only real evidence this implementation
// matches what every other compliant client computes — the spec text
// itself doesn't give a full reference implementation, only the formula in
// prose, so these vectors are the actual ground truth this was built
// against (see the package doc comment's own verification note).
func TestCanonicalPriorityMatchesTheOfficialTestVectors(t *testing.T) {
	cases := []struct {
		aIP, bIP string
		want     uint32
	}{
		{"123.213.32.10", "98.76.54.32", 0xec2d7224},
		{"123.213.32.10", "123.213.32.234", 0x99568189},
	}
	for _, c := range cases {
		a := net.ParseIP(c.aIP)
		b := net.ParseIP(c.bIP)
		got := CanonicalPriority(a, 6881, b, 6881)
		if got != c.want {
			t.Errorf("CanonicalPriority(%s, %s) = %08x, want %08x", c.aIP, c.bIP, got, c.want)
		}
	}
}

// TestCanonicalPriorityIsSymmetric proves the property both sides of a
// real connection actually depend on: swapping which address is "a" and
// which is "b" must never change the result, since two different
// processes each call this with themselves listed first.
func TestCanonicalPriorityIsSymmetric(t *testing.T) {
	a := net.ParseIP("123.213.32.10")
	b := net.ParseIP("98.76.54.32")
	forward := CanonicalPriority(a, 6881, b, 51413)
	backward := CanonicalPriority(b, 51413, a, 6881)
	if forward != backward {
		t.Errorf("CanonicalPriority is not symmetric: %08x vs %08x", forward, backward)
	}
}

// TestCanonicalPriorityFallsBackToPortsForIdenticalIPs covers the spec's
// other stated case (two peers sharing one public IP, e.g. both behind the
// same NAT) — an IP-only comparison would return the same constant value
// for every such pair, defeating the whole point.
func TestCanonicalPriorityFallsBackToPortsForIdenticalIPs(t *testing.T) {
	ip := net.ParseIP("203.0.113.5")
	p1 := CanonicalPriority(ip, 6881, ip, 6882)
	p2 := CanonicalPriority(ip, 6881, ip, 6883)
	if p1 == 0 || p2 == 0 {
		t.Fatal("got a zero priority for a same-IP, different-port pair")
	}
	if p1 == p2 {
		t.Error("two different port pairs on the same IP produced the same priority — the port fallback isn't discriminating")
	}
	// Symmetry must hold here too.
	if CanonicalPriority(ip, 6881, ip, 6882) != CanonicalPriority(ip, 6882, ip, 6881) {
		t.Error("same-IP port fallback is not symmetric")
	}
}

// TestCanonicalPriorityHandlesIPv6 proves the IPv6 path at least runs
// without panicking and stays symmetric — there is no official test vector
// to check the exact value against (see the package doc comment), so this
// cannot claim more than "internally consistent."
func TestCanonicalPriorityHandlesIPv6(t *testing.T) {
	a := net.ParseIP("2001:db8::1")
	b := net.ParseIP("2001:db8:1::2")
	forward := CanonicalPriority(a, 6881, b, 6881)
	backward := CanonicalPriority(b, 6881, a, 6881)
	if forward != backward {
		t.Errorf("IPv6 CanonicalPriority is not symmetric: %08x vs %08x", forward, backward)
	}
	if forward == 0 {
		t.Error("IPv6 CanonicalPriority returned 0, suspicious for two real distinct addresses")
	}
}

func ExampleCanonicalPriority() {
	a := net.ParseIP("123.213.32.10")
	b := net.ParseIP("98.76.54.32")
	fmt.Printf("%08x\n", CanonicalPriority(a, 6881, b, 6881))
	// Output: ec2d7224
}
