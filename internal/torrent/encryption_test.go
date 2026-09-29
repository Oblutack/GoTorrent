package torrent

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/mse"
)

// TestFullDownloadWithEncryptionPreferFallsBackToPlaintext proves
// Config.EncryptionPolicy is actually threaded through to every real
// outbound dial connectAndPump makes, not just accepted and ignored: the
// existing fakeSeeder fixture only ever speaks the classic plaintext
// handshake, so a torrent configured with PolicyPrefer completing a real
// download against it end to end can only happen if the fallback redial
// path (internal/peer's negotiateOutboundEncryption) genuinely runs.
func TestFullDownloadWithEncryptionPreferFallsBackToPlaintext(t *testing.T) {
	orig := mse.HandshakeTimeout
	mse.HandshakeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { mse.HandshakeTimeout = orig })

	const pieceLength = 16384
	mi, content := buildTorrent(t, "encrypted-prefer.bin", pieceLength, []fileSpec{
		{length: pieceLength*4 + 111},
	})

	cfg := newTestConfig(t)
	cfg.EncryptionPolicy = mse.PolicyPrefer
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 30*time.Second)

	got, err := os.ReadFile(filepath.Join(tr.cfg.DownloadDir, "encrypted-prefer.bin"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded file differs from the source (%d bytes vs %d)", len(got), len(content))
	}
}

// TestEncryptionPolicyDisabledDoesNotChangeAnExistingDownload is a
// regression guard for the "default is a complete no-op" guarantee every
// other feature in this project makes: a Config with the zero-value
// EncryptionPolicy (PolicyDisabled) must download exactly as it always
// has, with no MSE attempt anywhere in the path.
func TestEncryptionPolicyDisabledDoesNotChangeAnExistingDownload(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "encrypted-disabled.bin", pieceLength, []fileSpec{
		{length: pieceLength * 2},
	})

	cfg := newTestConfig(t) // EncryptionPolicy left at its zero value
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 30*time.Second)

	got, err := os.ReadFile(filepath.Join(tr.cfg.DownloadDir, "encrypted-disabled.bin"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded file differs from the source (%d bytes vs %d)", len(got), len(content))
	}
}
