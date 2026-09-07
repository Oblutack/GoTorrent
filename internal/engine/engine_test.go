package engine

import (
	"context"
	"crypto/sha1"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

func TestMain(m *testing.M) {
	logger.Init(false)
	os.Exit(m.Run())
}

// writeTorrentFile builds a small, valid, single-file .torrent with a dead
// announce URL (nobody needs to actually seed it: these tests only exercise
// add/list/remove/persist, not a real transfer) and returns its path and
// infohash.
func writeTorrentFile(t *testing.T, dir, name string) (path string, hash metainfo.Hash) {
	t.Helper()

	const pieceLength = 16384
	const total = pieceLength*2 + 100
	content := make([]byte, total)
	rand.New(rand.NewSource(7)).Read(content)

	var hashes []byte
	for off := 0; off < total; off += pieceLength {
		end := off + pieceLength
		if end > total {
			end = total
		}
		sum := sha1.Sum(content[off:end])
		hashes = append(hashes, sum[:]...)
	}

	type infoWire struct {
		Length      int64  `bencode:"length"`
		Name        string `bencode:"name"`
		PieceLength int64  `bencode:"piece length"`
		Pieces      []byte `bencode:"pieces"`
	}
	infoBytes, err := bencode.Marshal(infoWire{Length: total, Name: name, PieceLength: pieceLength, Pieces: hashes})
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	torrentBytes, err := bencode.Marshal(struct {
		Announce string             `bencode:"announce"`
		Info     bencode.RawMessage `bencode:"info"`
	}{Announce: "http://127.0.0.1:1/announce", Info: infoBytes})
	if err != nil {
		t.Fatalf("marshal torrent: %v", err)
	}

	mi, err := metainfo.Parse(torrentBytes)
	if err != nil {
		t.Fatalf("parse torrent: %v", err)
	}

	path = filepath.Join(dir, name+".torrent")
	if err := os.WriteFile(path, torrentBytes, 0o644); err != nil {
		t.Fatalf("write torrent file: %v", err)
	}
	return path, mi.InfoHash
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(t.TempDir(), Defaults{
		DownloadDir: t.TempDir(),
		ResumeDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)
	return e
}

func waitForState(t *testing.T, tr *torrent.Torrent, want torrent.State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if tr.State() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("torrent did not reach %s within %s (stuck at %s)", want, timeout, tr.State())
}

func TestAddStartsAndListsTheTorrent(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "one")

	got, err := e.Add(path, "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got != hash {
		t.Fatalf("Add returned %s, want %s", got, hash)
	}

	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get did not find the added torrent")
	}
	// No real content exists on disk, so verification finds every piece
	// missing and the torrent settles into Downloading (with a dead tracker,
	// nothing will ever complete it) rather than Seeding.
	waitForState(t, tr, torrent.StateDownloading, 10*time.Second)

	list := e.List()
	if len(list) != 1 {
		t.Fatalf("List() has %d entries, want 1", len(list))
	}
	if list[0].InfoHash != hash {
		t.Fatalf("List()[0].InfoHash = %s, want %s", list[0].InfoHash, hash)
	}
	if list[0].Source != path {
		t.Fatalf("List()[0].Source = %q, want %q", list[0].Source, path)
	}
}

// TestAddAcceptsMagnetURI proves Add's file-path-vs-magnet branch actually
// takes the magnet path: no .torrent file involved at all, just a
// "magnet:?xt=..." string, and the torrent should come up in
// FetchingMetadata (there is no metadata yet) with the dn= hint as its
// display name until real metadata says otherwise.
func TestAddAcceptsMagnetURI(t *testing.T) {
	e := newTestEngine(t)

	hash := metainfo.Hash{0xaa, 0xbb, 0xcc}
	uri := "magnet:?xt=urn:btih:" + hash.String() + "&dn=A+Cool+Torrent&tr=http%3A%2F%2F127.0.0.1%3A1%2Fannounce"

	got, err := e.Add(uri, "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got != hash {
		t.Fatalf("Add returned %s, want %s", got, hash)
	}

	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get did not find the added torrent")
	}
	if tr.Metadata() != nil {
		t.Fatal("a magnet-sourced torrent already has metadata")
	}
	waitForState(t, tr, torrent.StateFetchingMetadata, 5*time.Second)

	list := e.List()
	if len(list) != 1 {
		t.Fatalf("List() has %d entries, want 1", len(list))
	}
	if list[0].Source != uri {
		t.Fatalf("List()[0].Source = %q, want the original magnet URI", list[0].Source)
	}
	if list[0].Name != "A Cool Torrent" {
		t.Fatalf("List()[0].Name = %q, want the magnet's dn= hint", list[0].Name)
	}
}

func TestAddRejectsBadMagnetURI(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.Add("magnet:?dn=NoInfoHash", ""); err == nil {
		t.Fatal("Add accepted a magnet with no infohash, want an error")
	}
	if len(e.List()) != 0 {
		t.Fatal("a rejected magnet Add left an entry behind")
	}
}

func TestAddRejectsDuplicateInfoHash(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, _ := writeTorrentFile(t, torrentDir, "dup")

	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if _, err := e.Add(path, ""); err == nil {
		t.Fatal("second Add of the same torrent succeeded, want an error")
	}
	if len(e.List()) != 1 {
		t.Fatalf("List() has %d entries after a rejected duplicate, want 1", len(e.List()))
	}
}

func TestAddRequiresADownloadDirectory(t *testing.T) {
	e, err := New(t.TempDir(), Defaults{}) // no default DownloadDir
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	path, _ := writeTorrentFile(t, t.TempDir(), "no-dir")
	if _, err := e.Add(path, ""); err == nil {
		t.Fatal("Add with no download directory anywhere succeeded, want an error")
	}
	if len(e.List()) != 0 {
		t.Fatal("a rejected Add left an entry behind")
	}
}

func TestRemoveStopsAndUnlistsTheTorrent(t *testing.T) {
	e := newTestEngine(t)
	path, hash := writeTorrentFile(t, t.TempDir(), "remove-me")

	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Remove blocks on Torrent.Stop, which blocks on Run returning; if
	// Remove ever stopped doing that, this call would hang and the test
	// would time out rather than fail cleanly, but that is still a real
	// signal something regressed.
	if err := e.Remove(hash); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, ok := e.Get(hash); ok {
		t.Fatal("Get still finds a removed torrent")
	}
	if len(e.List()) != 0 {
		t.Fatalf("List() has %d entries after Remove, want 0", len(e.List()))
	}
}

func TestRemoveUnknownHashErrors(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Remove(metainfo.Hash{}); err == nil {
		t.Fatal("Remove of an unmanaged hash succeeded, want an error")
	}
}

// TestLoadReconstructsTheFleet proves the manifest survives across two
// independent Engine instances pointed at the same state directory — the
// "persist across torrents" half of the fleet-manager requirement.
func TestLoadReconstructsTheFleet(t *testing.T) {
	stateDir := t.TempDir()
	downloadDir := t.TempDir()
	torrentDir := t.TempDir()

	pathA, hashA := writeTorrentFile(t, torrentDir, "a")
	pathB, hashB := writeTorrentFile(t, torrentDir, "b")

	e1, err := New(stateDir, Defaults{DownloadDir: downloadDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := e1.Add(pathA, ""); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	if _, err := e1.Add(pathB, ""); err != nil {
		t.Fatalf("Add b: %v", err)
	}
	e1.Shutdown()

	e2, err := New(stateDir, Defaults{DownloadDir: downloadDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New (second engine): %v", err)
	}
	t.Cleanup(e2.Shutdown)
	if err := e2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	list := e2.List()
	if len(list) != 2 {
		t.Fatalf("List() after Load has %d entries, want 2", len(list))
	}
	if _, ok := e2.Get(hashA); !ok {
		t.Fatal("Load did not reconstruct torrent a")
	}
	if _, ok := e2.Get(hashB); !ok {
		t.Fatal("Load did not reconstruct torrent b")
	}
}

// TestListenRoutesInboundConnectionToTheRightTorrent proves the engine's
// shared listener actually reaches a managed torrent's peer set: a fake
// remote peer dials in, completes a handshake against a real infohash this
// engine manages, and the torrent's own Stats().PeerCount should reflect it
// - exactly the path a real inbound connection from outside a NAT would take
// once port-forwarded, minus the NAT.
func TestListenRoutesInboundConnectionToTheRightTorrent(t *testing.T) {
	e := newTestEngine(t)

	// Borrow an ephemeral port from the OS, then hand it to the engine: Listen
	// itself always binds a fixed (if arbitrary) port rather than 0, since 0
	// is reserved by Defaults to mean "don't listen at all".
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probing for a free port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	e.defaults.ListenPort = uint16(port)

	if err := e.Listen(context.Background()); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	path, hash := writeTorrentFile(t, t.TempDir(), "inbound")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get did not find the added torrent")
	}
	waitForState(t, tr, torrent.StateDownloading, 10*time.Second)

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dialing the engine's listener: %v", err)
	}
	defer conn.Close()

	var remoteID [20]byte
	copy(remoteID[:], "-TEST01-inbound00000")
	if _, err := conn.Write(peer.NewHandshake(hash, remoteID).Serialize()); err != nil {
		t.Fatalf("writing handshake: %v", err)
	}
	reply, err := peer.ReadHandshake(conn)
	if err != nil {
		t.Fatalf("reading reply handshake: %v", err)
	}
	if reply.InfoHash != hash {
		t.Fatalf("reply infohash = %x, want %x", reply.InfoHash, hash)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if tr.Stats().PeerCount == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("torrent's PeerCount never reached 1 after an inbound connection, got %d", tr.Stats().PeerCount)
}

// TestListenClosesConnectionForUnmanagedInfoHash proves an inbound
// connection for a torrent this engine does not manage gets its connection
// closed rather than silently held open or routed nowhere.
func TestListenClosesConnectionForUnmanagedInfoHash(t *testing.T) {
	e := newTestEngine(t)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probing for a free port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	e.defaults.ListenPort = uint16(port)

	if err := e.Listen(context.Background()); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dialing the engine's listener: %v", err)
	}
	defer conn.Close()

	var remoteID [20]byte
	copy(remoteID[:], "-TEST01-nobody000000")
	unmanaged := metainfo.Hash{0xde, 0xad, 0xbe, 0xef}
	if _, err := conn.Write(peer.NewHandshake(unmanaged, remoteID).Serialize()); err != nil {
		t.Fatalf("writing handshake: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected the connection to be closed for an unmanaged infohash, got data instead")
	}
}

// dialLoopback opens n TCP connections to addr, all necessarily from the
// same source IP (127.0.0.1) since that's how loopback works — exactly what
// maxInboundPerIP counts by.
func dialLoopback(t *testing.T, addr string, n int) []net.Conn {
	t.Helper()
	conns := make([]net.Conn, n)
	for i := range conns {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		conns[i] = c
	}
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close()
		}
	})
	return conns
}

// TestHandleIncomingAllowsUpToPerIPLimit proves a connection within the
// per-IP budget is kept open, waiting for a handshake, rather than closed
// outright — the read below times out (nothing was ever sent) rather than
// failing with a closed-connection error, which is how "still open" and
// "rejected" are told apart without a real peer.Client on the other end.
func TestHandleIncomingAllowsUpToPerIPLimit(t *testing.T) {
	e := newTestEngine(t)
	port := freeTCPPort(t)
	e.defaults.ListenPort = port
	if err := e.Listen(context.Background()); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	conns := dialLoopback(t, fmt.Sprintf("127.0.0.1:%d", port), maxInboundPerIP)
	last := conns[len(conns)-1]

	last.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 1)
	_, err := last.Read(buf)
	ne, ok := err.(net.Error)
	if !ok || !ne.Timeout() {
		t.Fatalf("got err=%v, want a read timeout (the connection should still be open, waiting for a handshake)", err)
	}
}

// TestHandleIncomingRejectsConnectionsOverThePerIPLimit proves the
// (maxInboundPerIP+1)th concurrent connection from the same source IP gets
// closed immediately, before it can even tie up a goroutine reading a
// handshake that will never come from a hostile or malfunctioning peer.
func TestHandleIncomingRejectsConnectionsOverThePerIPLimit(t *testing.T) {
	e := newTestEngine(t)
	port := freeTCPPort(t)
	e.defaults.ListenPort = port
	if err := e.Listen(context.Background()); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	dialLoopback(t, addr, maxInboundPerIP)

	// Give the engine's goroutines a moment to actually reserve each of the
	// first maxInboundPerIP slots before this one more is expected to be
	// rejected — otherwise this dial could race ahead of them.
	time.Sleep(100 * time.Millisecond)

	extra, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer extra.Close()

	extra.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := extra.Read(buf); err == nil {
		t.Fatal("expected the over-limit connection to be closed, got data instead")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the over-limit connection was left open (read timed out) instead of being closed")
	}
}

// freeTCPPort borrows an ephemeral TCP port from the OS and gives it back,
// for tests that need a real, fixed (non-zero) port number — 0 is reserved
// to mean "don't listen at all", the same convention Listen uses for
// Defaults.ListenPort.
func freeTCPPort(t *testing.T) uint16 {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probing for a free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	return uint16(port)
}

// freeUDPPort borrows an ephemeral UDP port from the OS and gives it back,
// for tests that need a real, fixed (non-zero) port number to hand to
// StartDHT — 0 is reserved to mean "don't start DHT at all", the same
// convention Listen uses for Defaults.ListenPort.
func freeUDPPort(t *testing.T) uint16 {
	t.Helper()
	probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("probing for a free UDP port: %v", err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()
	return uint16(port)
}

func TestStartDHTIsANoOpAtZeroPort(t *testing.T) {
	e := newTestEngine(t)
	if err := e.StartDHT(context.Background(), 0); err != nil {
		t.Fatalf("StartDHT(0): %v", err)
	}
	if e.dhtNode != nil {
		t.Fatal("StartDHT(0) started a node; want a no-op")
	}
	if cfg := e.torrentConfig(t.TempDir()); cfg.DHT != nil {
		t.Fatal("torrentConfig set a DHT client when StartDHT was never really started")
	}
}

// TestStartDHTWiresANodeIntoTorrentConfig proves the actual plumbing: once
// StartDHT has bound a real node, every subsequently-built torrent Config
// carries it. The DHT wire protocol and peer-discovery behavior themselves
// are covered by internal/dht's own tests and internal/torrent's
// dht_test.go against a fake DHTClient; this only checks the seam between
// them is connected.
func TestStartDHTWiresANodeIntoTorrentConfig(t *testing.T) {
	e := newTestEngine(t)
	port := freeUDPPort(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := e.StartDHT(ctx, port); err != nil {
		t.Fatalf("StartDHT: %v", err)
	}
	if e.dhtNode == nil {
		t.Fatal("StartDHT did not set a node")
	}
	if cfg := e.torrentConfig(t.TempDir()); cfg.DHT == nil {
		t.Fatal("torrentConfig did not carry the started DHT node")
	}
}

// TestShutdownClosesTheDHTNode proves Shutdown tears the DHT node down
// alongside every torrent, rather than leaking its UDP socket.
func TestShutdownClosesTheDHTNode(t *testing.T) {
	e, err := New(t.TempDir(), Defaults{DownloadDir: t.TempDir(), ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.StartDHT(context.Background(), freeUDPPort(t)); err != nil {
		t.Fatalf("StartDHT: %v", err)
	}

	e.Shutdown()

	if e.dhtNode != nil {
		t.Fatal("Shutdown left dhtNode set")
	}
}

func TestStartPortMappingIsANoOpAtZeroPort(t *testing.T) {
	e := newTestEngine(t)
	if err := e.StartPortMapping(context.Background(), 0); err != nil {
		t.Fatalf("StartPortMapping(0): %v", err)
	}
	if e.portmapClient != nil {
		t.Fatal("StartPortMapping(0) started a client; want a no-op")
	}
}

// TestStartPortMappingFailsGracefullyWithNoGateway exercises the real
// discovery path. It assumes — as any CI environment or most sandboxed dev
// environments do — that no actual UPnP or NAT-PMP gateway is reachable, so
// mapping fails; what this checks is that a failure is reported as an error
// rather than a panic, and leaves the engine's state untouched (no client
// stored, ListenPort not rewritten to a mapping that never happened). See
// internal/portmap's own TestStartReturnsErrNoGatewayWhenNoneIsReachable for
// the same assumption spelled out in more detail.
func TestStartPortMappingFailsGracefullyWithNoGateway(t *testing.T) {
	e := newTestEngine(t)
	e.defaults.ListenPort = 6881

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := e.StartPortMapping(ctx, 6881)
	if err == nil {
		t.Fatal("StartPortMapping succeeded with no gateway reachable, want an error")
	}
	if e.portmapClient != nil {
		t.Fatal("a failed StartPortMapping left a client set")
	}
	if e.defaults.ListenPort != 6881 {
		t.Fatalf("ListenPort = %d after a failed mapping, want it unchanged at 6881", e.defaults.ListenPort)
	}
}

// TestLoadWithNoManifestIsNotAnError covers the first-run case: nothing has
// ever been added, so there is no manifest file yet.
func TestLoadWithNoManifestIsNotAnError(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Load(); err != nil {
		t.Fatalf("Load with no manifest: %v", err)
	}
	if len(e.List()) != 0 {
		t.Fatal("Load with no manifest fabricated an entry")
	}
}
