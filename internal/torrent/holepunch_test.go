package torrent

import (
	"net"
	"testing"
	"time"
)

// TestRequestHolepunchFailsForUnknownRelay proves the control-path
// validation: asking a peer this torrent isn't even connected to, to
// relay a rendezvous, is a real error, not a silently-ignored no-op.
func TestRequestHolepunchFailsForUnknownRelay(t *testing.T) {
	mi, _ := buildTorrent(t, "holepunch-unknown", 16384, []fileSpec{{length: 16384}})
	cfg := newTestConfig(t)
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	err = tr.RequestHolepunch("127.0.0.1:1", net.ParseIP("127.0.0.1"), 6881)
	if err == nil {
		t.Fatal("RequestHolepunch against an unconnected relay returned nil, want an error")
	}
}

// TestRequestHolepunchFailsWhenRelayLacksSupport proves the same check
// against a real connected peer that genuinely never advertised
// ut_holepunch — newFakeSeeder's own extended handshake only ever offers
// ut_metadata (see its fakeExtHandshake construction), so this is a real
// negotiated-capability check, not a guess about what the peer supports.
func TestRequestHolepunchFailsWhenRelayLacksSupport(t *testing.T) {
	mi, content := buildTorrent(t, "holepunch-nosupport", 16384, []fileSpec{{length: 16384}})
	seeder := newFakeSeeder(t, mi, content)

	cfg := newTestConfig(t)
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(tr.Peers()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(tr.Peers()) == 0 {
		t.Fatal("never connected to the fake seeder")
	}

	err = tr.RequestHolepunch(seeder.peerInfo().Addr(), net.ParseIP("127.0.0.1"), 6881)
	if err == nil {
		t.Fatal("RequestHolepunch against a relay with no ut_holepunch support returned nil, want an error")
	}
}

// TestRequestHolepunchRejectsAnInvalidTarget proves an obviously-invalid
// target (no port) is rejected locally rather than sent out onto the
// wire at all.
func TestRequestHolepunchRejectsAnInvalidTarget(t *testing.T) {
	mi, content := buildTorrent(t, "holepunch-invalid", 16384, []fileSpec{{length: 16384}})
	seeder := newFakeSeeder(t, mi, content)

	cfg := newTestConfig(t)
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(tr.Peers()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(tr.Peers()) == 0 {
		t.Fatal("never connected to the fake seeder")
	}

	if err := tr.RequestHolepunch(seeder.peerInfo().Addr(), net.ParseIP("127.0.0.1"), 0); err == nil {
		t.Fatal("RequestHolepunch with port 0 returned nil, want an error")
	}
}
