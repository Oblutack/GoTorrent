package torrent

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/utp"
)

// newTestUTPSocket builds a real *utp.Socket on a real UDP loopback port,
// closed automatically at test cleanup.
func newTestUTPSocket(t *testing.T) *utp.Socket {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	sock := utp.NewSocket(pc)
	t.Cleanup(func() { sock.Close() })
	return sock
}

// TestFullDownloadWithUTPPreferFallsBackToTCP proves Config.UTPPolicy/
// UTPSocket are actually threaded through to every real outbound dial
// connectAndPump makes, not just accepted and ignored: the existing
// fakeSeeder fixture only ever listens on TCP, so a torrent configured
// with PolicyPrefer completing a real download against it end to end can
// only happen if the fallback-to-TCP path (internal/peer's
// dialTransport) genuinely runs.
func TestFullDownloadWithUTPPreferFallsBackToTCP(t *testing.T) {
	orig := utp.SynRetries
	utp.SynRetries = 1
	t.Cleanup(func() { utp.SynRetries = orig })

	const pieceLength = 16384
	mi, content := buildTorrent(t, "utp-prefer.bin", pieceLength, []fileSpec{
		{length: pieceLength*4 + 222},
	})

	cfg := newTestConfig(t)
	cfg.UTPPolicy = utp.PolicyPrefer
	cfg.UTPSocket = newTestUTPSocket(t)
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 30*time.Second)

	got, err := os.ReadFile(filepath.Join(tr.cfg.DownloadDir, "utp-prefer.bin"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded file differs from the source (%d bytes vs %d)", len(got), len(content))
	}
}

// TestUTPPolicyDisabledDoesNotChangeAnExistingDownload is the same
// "default is a complete no-op" regression guard MSE's own equivalent
// test already established, applied to µTP: a Config with the zero-value
// UTPPolicy (and no UTPSocket at all) must download exactly as it always
// has.
func TestUTPPolicyDisabledDoesNotChangeAnExistingDownload(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "utp-disabled.bin", pieceLength, []fileSpec{
		{length: pieceLength * 2},
	})

	cfg := newTestConfig(t) // UTPPolicy/UTPSocket left at their zero values
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 30*time.Second)

	got, err := os.ReadFile(filepath.Join(tr.cfg.DownloadDir, "utp-disabled.bin"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded file differs from the source (%d bytes vs %d)", len(got), len(content))
	}
}
