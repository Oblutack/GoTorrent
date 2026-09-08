package ipfilter

import (
	"net"
	"strings"
	"testing"
)

func mustIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad test IP %q", s)
	}
	return ip
}

func TestBlockedInsideAndOutsideARange(t *testing.T) {
	f := New()
	f.Load([]Range{
		{Start: mustIP(t, "1.2.4.0"), End: mustIP(t, "1.2.4.255")},
		{Start: mustIP(t, "10.0.0.0"), End: mustIP(t, "10.255.255.255")},
	})

	cases := []struct {
		ip   string
		want bool
	}{
		{"1.2.4.0", true},   // range start, inclusive
		{"1.2.4.255", true}, // range end, inclusive
		{"1.2.4.128", true}, // middle
		{"1.2.3.255", false},
		{"1.2.5.0", false},
		{"10.5.5.5", true},
		{"192.168.1.1", false},
	}
	for _, c := range cases {
		if got := f.Blocked(mustIP(t, c.ip)); got != c.want {
			t.Errorf("Blocked(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestNilFilterNeverBlocks(t *testing.T) {
	var f *Filter
	if f.Blocked(mustIP(t, "1.2.3.4")) {
		t.Fatal("nil *Filter reported blocked")
	}
	if f.Count() != 0 {
		t.Fatal("nil *Filter reported a nonzero count")
	}
}

func TestEmptyFilterNeverBlocks(t *testing.T) {
	f := New()
	if f.Blocked(mustIP(t, "1.2.3.4")) {
		t.Fatal("empty Filter reported blocked")
	}
}

func TestParseEmuleDAT(t *testing.T) {
	data := `# comment line, ignored
001.002.004.000 - 001.002.004.255 , 100 , Some description

010.000.000.000-010.255.255.255,050,Another one, with a comma in the description
not a valid line at all
`
	ranges := ParseEmuleDAT(strings.NewReader(data))
	if len(ranges) != 2 {
		t.Fatalf("got %d ranges, want 2: %v", len(ranges), ranges)
	}
	f := New()
	f.Load(ranges)
	if !f.Blocked(mustIP(t, "1.2.4.128")) {
		t.Fatal("first eMule range not blocked")
	}
	if !f.Blocked(mustIP(t, "10.1.1.1")) {
		t.Fatal("second eMule range not blocked")
	}
	if f.Blocked(mustIP(t, "8.8.8.8")) {
		t.Fatal("unrelated IP incorrectly blocked")
	}
}

func TestParsePeerGuardianP2P(t *testing.T) {
	data := `# comment
Some Company:001.002.004.000-001.002.004.255
Another One : 010.000.000.000 - 010.255.255.255
malformed line with no colon range
`
	ranges := ParsePeerGuardianP2P(strings.NewReader(data))
	if len(ranges) != 2 {
		t.Fatalf("got %d ranges, want 2: %v", len(ranges), ranges)
	}
	f := New()
	f.Load(ranges)
	if !f.Blocked(mustIP(t, "1.2.4.1")) {
		t.Fatal("first p2p range not blocked")
	}
	if !f.Blocked(mustIP(t, "10.9.9.9")) {
		t.Fatal("second p2p range not blocked")
	}
}

// TestParseEmuleDATToleratesZeroPaddedOctets is a regression test: real
// ipfilter.dat files commonly zero-pad every octet ("001.002.004.000"),
// which net.ParseIP has deliberately rejected since Go 1.17. Without
// parseLenientIP, ParseEmuleDAT silently drops every such line.
func TestParseEmuleDATToleratesZeroPaddedOctets(t *testing.T) {
	ranges := ParseEmuleDAT(strings.NewReader("001.002.004.000 - 001.002.004.255 , 100 , padded\n"))
	if len(ranges) != 1 {
		t.Fatalf("got %d ranges from a zero-padded line, want 1", len(ranges))
	}
	f := New()
	f.Load(ranges)
	if !f.Blocked(mustIP(t, "1.2.4.10")) {
		t.Fatal("zero-padded range did not block an IP inside it")
	}
}

func TestLoadReplacesThePreviousSet(t *testing.T) {
	f := New()
	f.Load([]Range{{Start: mustIP(t, "1.1.1.1"), End: mustIP(t, "1.1.1.1")}})
	if !f.Blocked(mustIP(t, "1.1.1.1")) {
		t.Fatal("first Load didn't take effect")
	}
	f.Load([]Range{{Start: mustIP(t, "2.2.2.2"), End: mustIP(t, "2.2.2.2")}})
	if f.Blocked(mustIP(t, "1.1.1.1")) {
		t.Fatal("second Load did not replace the first set")
	}
	if !f.Blocked(mustIP(t, "2.2.2.2")) {
		t.Fatal("second Load's range not blocked")
	}
}
