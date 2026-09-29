package engine

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/mse"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// newListeningEngineWithPolicy is listeningEngine (holepunch_test.go) with
// a caller-chosen EncryptionPolicy — every test below needs that, which
// listeningEngine's own fixed Defaults don't expose.
func newListeningEngineWithPolicy(t *testing.T, downloadDir string, policy mse.Policy) (e *Engine, port uint16) {
	t.Helper()
	e, err := New(t.TempDir(), Defaults{
		DownloadDir:      downloadDir,
		ResumeDir:        t.TempDir(),
		EncryptionPolicy: policy,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probing for a free port: %v", err)
	}
	p := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	e.defaults.ListenPort = uint16(p)
	if err := e.Listen(context.Background()); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	return e, uint16(p)
}

// TestRealTransferWithEncryptionPreferCompletesEncrypted is the real,
// end-to-end proof both halves of this feature work together: a seeder
// and a leecher, both configured with PolicyPrefer, complete a real
// transfer over loopback — the leecher's own outbound MSE negotiation
// (internal/peer) and the seeder's own inbound MSE acceptance
// (Engine.handleIncoming) both have to work correctly for this to
// succeed, since neither side is running with encryption disabled.
func TestRealTransferWithEncryptionPreferCompletesEncrypted(t *testing.T) {
	seederDownloadDir := t.TempDir()
	torrentDir := t.TempDir()
	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "mse-prefer")
	if err := os.WriteFile(filepath.Join(seederDownloadDir, "mse-prefer"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}

	seeder, port := newListeningEngineWithPolicy(t, seederDownloadDir, mse.PolicyPrefer)
	if _, err := seeder.Add(path, ""); err != nil {
		t.Fatalf("seeder Add: %v", err)
	}
	seederTr, ok := seeder.Get(hash)
	if !ok {
		t.Fatal("seeder torrent missing right after Add")
	}
	waitForState(t, seederTr, torrent.StateSeeding, 5*time.Second)

	leecher, err := New(t.TempDir(), Defaults{
		DownloadDir:      t.TempDir(),
		ResumeDir:        t.TempDir(),
		EncryptionPolicy: mse.PolicyPrefer,
	})
	if err != nil {
		t.Fatalf("New (leecher): %v", err)
	}
	t.Cleanup(leecher.Shutdown)

	if _, err := leecher.Add(path, ""); err != nil {
		t.Fatalf("leecher Add: %v", err)
	}
	leecherTr, ok := leecher.Get(hash)
	if !ok {
		t.Fatal("leecher torrent missing right after Add")
	}
	leecherTr.DialPeer(loopback(port))

	waitForState(t, leecherTr, torrent.StateSeeding, 30*time.Second)

	got, err := os.ReadFile(filepath.Join(leecher.defaults.DownloadDir, "mse-prefer"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if len(got) != len(content) {
		t.Fatalf("downloaded %d bytes, want %d", len(got), len(content))
	}
	for i := range got {
		if got[i] != content[i] {
			t.Fatalf("downloaded content differs from the source at byte %d", i)
		}
	}
}

// TestRequiredEngineRefusesAPlaintextInboundConnection proves the inbound
// half of PolicyRequired actually enforces something: a raw connection
// that sends a real classic (unencrypted) handshake — never touching MSE
// at all, standing in for a peer that doesn't support encryption — must
// be refused before ever reaching AcceptPeer, not just ignored.
func TestRequiredEngineRefusesAPlaintextInboundConnection(t *testing.T) {
	e, port := newListeningEngineWithPolicy(t, t.TempDir(), mse.PolicyRequired)

	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "mse-required-refuses")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("torrent missing right after Add")
	}

	conn, err := net.Dial("tcp", loopbackAddr(port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	var peerID [20]byte
	copy(peerID[:], "-TEST01-plaintextonl")
	hs := peer.NewHandshake([20]byte(hash), peerID)
	if _, err := conn.Write(hs.Serialize()); err != nil {
		t.Fatalf("writing classic handshake: %v", err)
	}

	// A Required engine must close the connection rather than reply with
	// its own handshake — read deliberately expects EOF/an error, not a
	// real 68-byte reply.
	buf := make([]byte, 68)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(buf)
	if err == nil {
		t.Fatalf("Required engine replied to a plaintext handshake with %d bytes, want the connection closed instead", n)
	}

	// And the torrent itself must never have registered this as a real
	// peer.
	time.Sleep(100 * time.Millisecond)
	if len(tr.Peers()) != 0 {
		t.Fatalf("torrent has %d peers, want 0 (the plaintext connection should never have been routed)", len(tr.Peers()))
	}
}

// TestPreferEngineConnectsToBothEncryptedAndPlaintextPeers proves the
// fallback in PolicyPrefer works against a genuinely different real peer
// in the same run, not just in isolation: one leecher engine reaches
// StateSeeding against two independent seeders — one that itself supports
// MSE, one that only ever speaks the classic handshake.
func TestPreferEngineConnectsToBothEncryptedAndPlaintextPeers(t *testing.T) {
	orig := mse.HandshakeTimeout
	mse.HandshakeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { mse.HandshakeTimeout = orig })

	encSeederDir, plainSeederDir := t.TempDir(), t.TempDir()
	torrentDir := t.TempDir()
	pathEnc, hashEnc, contentEnc := writeSingleFileTorrentWithContent(t, torrentDir, "mse-mixed-enc")
	pathPlain, hashPlain, contentPlain := writeSingleFileTorrentWithContent(t, torrentDir, "mse-mixed-plain")
	if err := os.WriteFile(filepath.Join(encSeederDir, "mse-mixed-enc"), contentEnc, 0o644); err != nil {
		t.Fatalf("pre-seeding encrypted-seeder content: %v", err)
	}
	if err := os.WriteFile(filepath.Join(plainSeederDir, "mse-mixed-plain"), contentPlain, 0o644); err != nil {
		t.Fatalf("pre-seeding plaintext-seeder content: %v", err)
	}

	encSeeder, encPort := newListeningEngineWithPolicy(t, encSeederDir, mse.PolicyPrefer)
	plainSeeder, plainPort := newListeningEngineWithPolicy(t, plainSeederDir, mse.PolicyDisabled)

	if _, err := encSeeder.Add(pathEnc, ""); err != nil {
		t.Fatalf("encSeeder Add: %v", err)
	}
	if _, err := plainSeeder.Add(pathPlain, ""); err != nil {
		t.Fatalf("plainSeeder Add: %v", err)
	}
	encSeederTr, _ := encSeeder.Get(hashEnc)
	plainSeederTr, _ := plainSeeder.Get(hashPlain)
	waitForState(t, encSeederTr, torrent.StateSeeding, 5*time.Second)
	waitForState(t, plainSeederTr, torrent.StateSeeding, 5*time.Second)

	leecher, err := New(t.TempDir(), Defaults{
		DownloadDir:      t.TempDir(),
		ResumeDir:        t.TempDir(),
		EncryptionPolicy: mse.PolicyPrefer,
	})
	if err != nil {
		t.Fatalf("New (leecher): %v", err)
	}
	t.Cleanup(leecher.Shutdown)

	if _, err := leecher.Add(pathEnc, ""); err != nil {
		t.Fatalf("leecher Add (enc): %v", err)
	}
	if _, err := leecher.Add(pathPlain, ""); err != nil {
		t.Fatalf("leecher Add (plain): %v", err)
	}
	leecherEncTr, _ := leecher.Get(hashEnc)
	leecherPlainTr, _ := leecher.Get(hashPlain)

	leecherEncTr.DialPeer(loopback(encPort))
	leecherPlainTr.DialPeer(loopback(plainPort))

	waitForState(t, leecherEncTr, torrent.StateSeeding, 30*time.Second)
	waitForState(t, leecherPlainTr, torrent.StateSeeding, 30*time.Second)
}

func loopbackAddr(port uint16) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))
}
