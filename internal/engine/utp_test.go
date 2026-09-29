package engine

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/dht"
	"github.com/Oblutack/GoTorrent/internal/torrent"
	"github.com/Oblutack/GoTorrent/internal/utp"
)

// newListeningEngineWithUTP is newListeningEngineWithPolicy's (encryption_test.go)
// µTP counterpart: a real Engine, its TCP listener bound, plus StartUTP run
// on that exact same port number — the real shape internal/bootstrap.Engine
// establishes (StartUTP before StartDHT, sharing whatever port Listen
// settled on), reproduced by hand here since these tests build an Engine
// directly rather than going through bootstrap.
func newListeningEngineWithUTP(t *testing.T, downloadDir string, policy utp.Policy) (e *Engine, port uint16) {
	t.Helper()
	e, err := New(t.TempDir(), Defaults{
		DownloadDir: downloadDir,
		ResumeDir:   t.TempDir(),
		UTPPolicy:   policy,
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
	ctx := context.Background()
	if err := e.Listen(ctx); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if err := e.StartUTP(ctx, uint16(p)); err != nil {
		t.Fatalf("StartUTP: %v", err)
	}
	return e, uint16(p)
}

// TestRealTransferOverUTPCompletesEndToEnd is the real, end-to-end proof
// this feature's outbound and inbound halves work together: a seeder and a
// leecher, both configured with utp.PolicyRequired (so there is no TCP
// fallback to silently paper over a broken µTP path), complete a real
// transfer over loopback. The leecher's own outbound µTP dial
// (internal/peer's dialTransport, fed by torrentConfig's UTPSocket) and the
// seeder's own inbound µTP accept (StartUTP's utpAcceptLoop, routed through
// the same handleIncoming path a TCP connection already uses) both have to
// work correctly for this to succeed.
func TestRealTransferOverUTPCompletesEndToEnd(t *testing.T) {
	seederDownloadDir := t.TempDir()
	torrentDir := t.TempDir()
	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "utp-e2e")
	if err := os.WriteFile(filepath.Join(seederDownloadDir, "utp-e2e"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}

	seeder, port := newListeningEngineWithUTP(t, seederDownloadDir, utp.PolicyRequired)
	if _, err := seeder.Add(path, ""); err != nil {
		t.Fatalf("seeder Add: %v", err)
	}
	seederTr, ok := seeder.Get(hash)
	if !ok {
		t.Fatal("seeder torrent missing right after Add")
	}
	waitForState(t, seederTr, torrent.StateSeeding, 5*time.Second)

	leecher, leecherPort := newListeningEngineWithUTP(t, t.TempDir(), utp.PolicyRequired)
	_ = leecherPort // the leecher never needs to be dialed in this test

	if _, err := leecher.Add(path, ""); err != nil {
		t.Fatalf("leecher Add: %v", err)
	}
	leecherTr, ok := leecher.Get(hash)
	if !ok {
		t.Fatal("leecher torrent missing right after Add")
	}
	leecherTr.DialPeer(loopback(port))

	waitForState(t, leecherTr, torrent.StateSeeding, 30*time.Second)

	got, err := os.ReadFile(filepath.Join(leecher.defaults.DownloadDir, "utp-e2e"))
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

// TestDHTAndUTPGenuinelyShareOnePort is the real proof StartUTP/StartDHT's
// port-sharing actually works, not just compiles: one Engine, one port,
// both a real KRPC round trip (a second, independent *dht.DHT node
// bootstrapping against the Engine's shared-port node) and a real µTP
// connection (a torrent added to the Engine, dialed by a second Engine
// using its own separate µTP socket) succeed against that identical port
// number, in the same test run.
func TestDHTAndUTPGenuinelyShareOnePort(t *testing.T) {
	torrentDir := t.TempDir()
	path, hash, content := writeSingleFileTorrentWithContent(t, torrentDir, "utp-dht-share")
	seederDownloadDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(seederDownloadDir, "utp-dht-share"), content, 0o644); err != nil {
		t.Fatalf("pre-seeding content: %v", err)
	}

	e, err := New(t.TempDir(), Defaults{
		DownloadDir: seederDownloadDir,
		ResumeDir:   t.TempDir(),
		UTPPolicy:   utp.PolicyRequired,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probing for a free port: %v", err)
	}
	port := uint16(probe.Addr().(*net.TCPAddr).Port)
	probe.Close()
	e.defaults.ListenPort = port
	ctx := context.Background()
	if err := e.Listen(ctx); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	// StartUTP before StartDHT, exactly like bootstrap.Engine sequences
	// them — this is the ordering the whole feature depends on.
	if err := e.StartUTP(ctx, port); err != nil {
		t.Fatalf("StartUTP: %v", err)
	}
	if err := e.StartDHT(ctx, port); err != nil {
		t.Fatalf("StartDHT: %v", err)
	}
	if e.udpMux == nil {
		t.Fatal("e.udpMux is nil after StartUTP; expected the shared mux to be bound")
	}

	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("torrent missing right after Add")
	}
	waitForState(t, tr, torrent.StateSeeding, 5*time.Second)

	// --- Half 1: a real, independent DHT node pings this Engine's
	// shared-port node and gets a real reply, populating its own routing
	// table — proof DHT is genuinely alive on the shared port.
	dhtNode, err := dht.New(dht.Config{Port: 0})
	if err != nil {
		t.Fatalf("dht.New: %v", err)
	}
	t.Cleanup(func() { dhtNode.Close() })

	bootstrapCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dhtNode.Bootstrap(bootstrapCtx, []string{net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))})
	if dhtNode.NodeCount() == 0 {
		t.Fatal("dhtNode's routing table is empty after bootstrapping against the Engine's shared-port DHT node — no real reply arrived")
	}

	// --- Half 2: a real second Engine, using its own separate µTP socket,
	// dials this Engine's torrent over µTP on that identical port and
	// completes a real transfer — proof µTP is genuinely alive on the same
	// shared port at the same time.
	leecher, err := New(t.TempDir(), Defaults{
		DownloadDir: t.TempDir(),
		ResumeDir:   t.TempDir(),
		UTPPolicy:   utp.PolicyRequired,
	})
	if err != nil {
		t.Fatalf("New (leecher): %v", err)
	}
	t.Cleanup(leecher.Shutdown)
	// The leecher only ever dials out in this test — StartUTP still needs
	// a real (OS-assigned) port number to bind its own local µTP socket to
	// dial FROM, since port 0 is StartUTP's own "don't start" no-op, same
	// convention Listen/StartDHT/StartLSD already use.
	leecherProbe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probing for a free leecher port: %v", err)
	}
	leecherPort := uint16(leecherProbe.Addr().(*net.TCPAddr).Port)
	leecherProbe.Close()
	if err := leecher.StartUTP(context.Background(), leecherPort); err != nil {
		t.Fatalf("StartUTP (leecher): %v", err)
	}

	if _, err := leecher.Add(path, ""); err != nil {
		t.Fatalf("leecher Add: %v", err)
	}
	leecherTr, ok := leecher.Get(hash)
	if !ok {
		t.Fatal("leecher torrent missing right after Add")
	}
	leecherTr.DialPeer(loopback(port))

	waitForState(t, leecherTr, torrent.StateSeeding, 30*time.Second)

	got, err := os.ReadFile(filepath.Join(leecher.defaults.DownloadDir, "utp-dht-share"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if len(got) != len(content) {
		t.Fatalf("downloaded %d bytes, want %d", len(got), len(content))
	}
}
