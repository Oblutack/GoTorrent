package torrent

import (
	"net"
	"testing"
	"time"
)

// TestFullDownloadOverIPv6 proves peer TCP connections work over IPv6
// end to end — dialing, the wire protocol, and a full transfer — not just
// "net.Dial accepts an IPv6 address" in the abstract. TCP peer connections
// never assumed IPv4 anywhere in this codebase (tracker.PeerInfo.IP is a
// plain net.IP, and net.Dial/net.Listen are already address-family
// agnostic), so this is a verification that already-correct code handles
// IPv6 correctly, not new IPv6-specific code. Skips (not fails) if this
// machine's IPv6 loopback isn't available — some sandboxes disable it,
// same reasoning as internal/lsd's own multicast-unavailable skip.
func TestFullDownloadOverIPv6(t *testing.T) {
	if _, err := net.Listen("tcp", "[::1]:0"); err != nil {
		t.Skipf("IPv6 loopback unavailable on this machine: %v", err)
	}

	const pieceLength = 16384
	mi, content := buildTorrent(t, "ipv6.bin", pieceLength, []fileSpec{
		{length: pieceLength*3 + 111},
	})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	seeder := newFakeSeederAt(t, "[::1]:0", mi, content)
	pi := seeder.peerInfo()
	if pi.IP.To4() != nil || !pi.IP.Equal(net.ParseIP("::1")) {
		t.Fatalf("test setup: fake seeder's own peerInfo() = %v, want the IPv6 loopback address", pi.IP)
	}
	tr.DialPeer(pi)

	waitForState(t, tr, StateSeeding, 15*time.Second)
	if got := tr.Stats().Downloaded; got != mi.TotalLength {
		t.Fatalf("Downloaded = %d, want %d", got, mi.TotalLength)
	}
}
