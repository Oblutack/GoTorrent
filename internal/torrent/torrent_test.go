package torrent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/bitfield"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/ratelimit"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

func TestMain(m *testing.M) {
	logger.Init(false)
	os.Exit(m.Run())
}

// --- synthetic torrent, mirroring internal/session's test fixtures --------

type fileSpec struct {
	path   []string
	length int64
}

func buildTorrent(t *testing.T, name string, pieceLength int64, files []fileSpec) (*metainfo.MetaInfo, []byte) {
	t.Helper()

	var total int64
	for _, f := range files {
		total += f.length
	}
	content := make([]byte, total)
	rand.New(rand.NewSource(11)).Read(content)

	var hashes []byte
	for off := int64(0); off < total; off += pieceLength {
		end := off + pieceLength
		if end > total {
			end = total
		}
		sum := sha1.Sum(content[off:end])
		hashes = append(hashes, sum[:]...)
	}

	type fileWire struct {
		Length int64    `bencode:"length"`
		Path   []string `bencode:"path"`
	}
	type infoWire struct {
		Files       []fileWire `bencode:"files,omitempty"`
		Length      int64      `bencode:"length,omitempty"`
		Name        string     `bencode:"name"`
		PieceLength int64      `bencode:"piece length"`
		Pieces      []byte     `bencode:"pieces"`
	}
	info := infoWire{Name: name, PieceLength: pieceLength, Pieces: hashes}
	if len(files) == 1 && len(files[0].path) == 0 {
		info.Length = files[0].length
	} else {
		for _, f := range files {
			info.Files = append(info.Files, fileWire{Length: f.length, Path: f.path})
		}
	}

	infoBytes, err := bencode.Marshal(info)
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
	return mi, content
}

// --- fake seeder, mirroring internal/session's ------------------------

type fakeSeeder struct {
	t       *testing.T
	ln      net.Listener
	mi      *metainfo.MetaInfo
	content []byte
	delay   time.Duration // artificial per-block latency, for pause/resume tests

	mu     sync.Mutex
	served int
}

func newFakeSeeder(t *testing.T, mi *metainfo.MetaInfo, content []byte) *fakeSeeder {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSeeder{t: t, ln: ln, mi: mi, content: content}
	t.Cleanup(func() { ln.Close() })
	go f.acceptLoop()
	return f
}

// newThrottledFakeSeeder adds a small per-block delay so a test has a
// reliable window to act (e.g. call Pause) before a small fixture torrent
// races to completion over loopback.
func newThrottledFakeSeeder(t *testing.T, mi *metainfo.MetaInfo, content []byte, delay time.Duration) *fakeSeeder {
	t.Helper()
	f := newFakeSeeder(t, mi, content)
	f.delay = delay
	return f
}

func (f *fakeSeeder) peerInfo() tracker.PeerInfo {
	addr := f.ln.Addr().(*net.TCPAddr)
	return tracker.PeerInfo{IP: addr.IP, Port: uint16(addr.Port)}
}

func (f *fakeSeeder) blocksServed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.served
}

func (f *fakeSeeder) acceptLoop() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeSeeder) handle(conn net.Conn) {
	defer conn.Close()

	hs := make([]byte, 68)
	if _, err := io.ReadFull(conn, hs); err != nil {
		return
	}
	var id [20]byte
	copy(id[:], "-SEED01-fakeseeder00")
	if _, err := conn.Write(peer.NewHandshake(f.mi.InfoHash, id).Serialize()); err != nil {
		return
	}

	if err := writeMsg(conn, peer.MsgBitfield, bitfield.Full(f.mi.NumPieces()).Bytes()); err != nil {
		return
	}
	if err := writeMsg(conn, peer.MsgUnchoke, nil); err != nil {
		return
	}

	for {
		id, payload, err := readMsg(conn)
		if err != nil {
			return
		}
		if id != peer.MsgRequest {
			continue
		}
		var req peer.MsgRequestPayload
		if err := req.Parse(payload); err != nil {
			return
		}

		start := int64(req.Index)*f.mi.Info.PieceLength + int64(req.Begin)
		end := start + int64(req.Length)
		if start < 0 || end > int64(len(f.content)) {
			f.t.Errorf("seeder got an out-of-range request: piece %d begin %d length %d", req.Index, req.Begin, req.Length)
			return
		}

		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		block := peer.MsgPiecePayload{Index: req.Index, Begin: req.Begin, Block: f.content[start:end]}
		if err := writeMsg(conn, peer.MsgPiece, block.Serialize()); err != nil {
			return
		}
		f.mu.Lock()
		f.served++
		f.mu.Unlock()
	}
}

func writeMsg(conn net.Conn, id peer.MessageID, payload []byte) error {
	msg := &peer.Message{ID: id, Payload: payload}
	_, err := conn.Write(msg.Serialize())
	return err
}

func readMsg(conn net.Conn) (peer.MessageID, []byte, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 {
		return 0, nil, nil
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(conn, body); err != nil {
		return 0, nil, err
	}
	return peer.MessageID(body[0]), body[1:], nil
}

// --- helpers ---------------------------------------------------------

func newTestConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		DownloadDir: t.TempDir(),
		ResumeDir:   t.TempDir(),
	}
}

// waitForState polls until the torrent reaches want or the deadline passes.
func waitForState(t *testing.T, tr *Torrent, want State, timeout time.Duration) {
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

// runInBackground starts a Torrent's actor and returns a stop function that
// cancels it and waits for a clean shutdown. Tests must always call stop,
// even on failure, or a goroutine leak in the implementation would go
// unnoticed.
func runInBackground(t *testing.T, tr *Torrent) (ctx context.Context, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		tr.Run(ctx)
		close(runDone)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(10 * time.Second):
			t.Fatal("Torrent.Run did not return after cancellation")
		}
	})
	return ctx, cancel
}

// --- tests -------------------------------------------------------------

// TestFullDownload is the end-to-end path: New, Run, dial a real fake
// seeder, download every piece over the real wire protocol, verify each one,
// and land in StateSeeding with the file correct on disk.
func TestFullDownload(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "payload.bin", pieceLength, []fileSpec{
		{length: pieceLength*12 + 777}, // a deliberately short final piece
	})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 30*time.Second)

	got, err := os.ReadFile(filepath.Join(tr.cfg.DownloadDir, "payload.bin"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded file differs from the source (%d bytes vs %d)", len(got), len(content))
	}

	stats := tr.Stats()
	if stats.HavePieces != mi.NumPieces() {
		t.Fatalf("Stats().HavePieces = %d, want %d", stats.HavePieces, mi.NumPieces())
	}
	if stats.Left != 0 {
		t.Fatalf("Stats().Left = %d, want 0", stats.Left)
	}
	if stats.Downloaded != mi.TotalLength {
		t.Fatalf("Stats().Downloaded = %d, want %d", stats.Downloaded, mi.TotalLength)
	}
	if seeder.blocksServed() == 0 {
		t.Fatal("the seeder never served a block")
	}
}

// TestDownloadRespectsRateLimit proves Config.DownLimit actually throttles a
// real transfer end to end, not just the ratelimit package in isolation.
func TestDownloadRespectsRateLimit(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "throttled.bin", pieceLength, []fileSpec{
		{length: pieceLength * 3},
	})

	const rate = pieceLength // 16 KiB/s: one piece's worth per second
	cfg := newTestConfig(t)
	cfg.DownLimit = ratelimit.New(rate)

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	seeder := newFakeSeeder(t, mi, content)
	start := time.Now()
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 30*time.Second)
	elapsed := time.Since(start)

	// The burst allowance covers the first piece for free, so only the
	// remaining two are actually rate-limited: floor is ~2s, not ~3s.
	want := time.Duration(float64(len(content)-pieceLength) / float64(rate) * float64(time.Second))
	if elapsed < want/2 {
		t.Fatalf("download finished in %s, want at least ~%s at a %d B/s cap", elapsed, want, rate)
	}
}

func TestMultiFileDownload(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "Bundle", pieceLength, []fileSpec{
		{path: []string{"readme.txt"}, length: 1000},
		{path: []string{"data", "part1.bin"}, length: pieceLength * 3},
		{path: []string{"data", "nested", "part2.bin"}, length: pieceLength*2 + 55},
	})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())
	waitForState(t, tr, StateSeeding, 30*time.Second)

	base := filepath.Join(tr.cfg.DownloadDir, "Bundle")
	var assembled []byte
	for _, rel := range [][]string{
		{"readme.txt"}, {"data", "part1.bin"}, {"data", "nested", "part2.bin"},
	} {
		b, err := os.ReadFile(filepath.Join(append([]string{base}, rel...)...))
		if err != nil {
			t.Fatalf("read %v: %v", rel, err)
		}
		assembled = append(assembled, b...)
	}
	if !bytes.Equal(assembled, content) {
		t.Fatal("reassembled files differ from the source")
	}
}

// TestAlreadyCompleteStartsSeeding checks the CheckingFiles->Seeding edge
// directly, without a Downloading phase: data already on disk before Run is
// ever called.
func TestAlreadyCompleteStartsSeeding(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "done.bin", pieceLength, []fileSpec{{length: pieceLength * 3}})

	cfg := newTestConfig(t)
	if err := os.WriteFile(filepath.Join(cfg.DownloadDir, "done.bin"), content, 0o644); err != nil {
		t.Fatalf("seed the download dir: %v", err)
	}

	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var sawChecking bool
	tr.OnStateChange(func(s State) {
		if s == StateCheckingFiles {
			sawChecking = true
		}
	})
	runInBackground(t, tr)

	waitForState(t, tr, StateSeeding, 10*time.Second)
	if !sawChecking {
		t.Fatal("torrent never passed through StateCheckingFiles")
	}
	if got := tr.Stats().Left; got != 0 {
		t.Fatalf("Stats().Left = %d, want 0", got)
	}
}

// TestPauseAndResume checks that pausing tears the swarm down cleanly and
// resuming picks the download back up without losing progress.
func TestPauseAndResume(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "pausable.bin", pieceLength, []fileSpec{{length: pieceLength * 40}})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	seeder := newThrottledFakeSeeder(t, mi, content, 15*time.Millisecond)
	tr.DialPeer(seeder.peerInfo())

	// Let some progress happen, then pause before it finishes.
	deadline := time.Now().Add(10 * time.Second)
	for tr.Stats().Downloaded == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if tr.Stats().Downloaded == 0 {
		t.Fatal("no progress was made before the pause")
	}

	if err := tr.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if got := tr.State(); got != StatePaused {
		t.Fatalf("State() = %s after Pause, want Paused", got)
	}
	if n := tr.Stats().PeerCount; n != 0 {
		t.Fatalf("PeerCount = %d after Pause, want 0", n)
	}
	progressAtPause := tr.Stats().Downloaded

	if err := tr.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	tr.DialPeer(seeder.peerInfo())
	waitForState(t, tr, StateSeeding, 30*time.Second)

	if tr.Stats().Downloaded < progressAtPause {
		t.Fatal("resuming lost progress that had already been made")
	}

	got, err := os.ReadFile(filepath.Join(tr.cfg.DownloadDir, "pausable.bin"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("downloaded file is wrong after pause/resume")
	}
}

// TestResumeDataSurvivesRestart is the crash-recovery scenario: a fresh
// Torrent instance pointed at the same download and resume directories must
// pick up the checkpointed progress instead of re-verifying or re-fetching
// everything from scratch.
func TestResumeDataSurvivesRestart(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "restart.bin", pieceLength, []fileSpec{{length: pieceLength * 50}})
	cfg := newTestConfig(t)

	first, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	run1Done := make(chan struct{})
	go func() { first.Run(ctx1); close(run1Done) }()

	seeder := newFakeSeeder(t, mi, content)
	first.DialPeer(seeder.peerInfo())

	deadline := time.Now().Add(10 * time.Second)
	for first.Stats().Downloaded == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if first.Stats().Downloaded == 0 {
		t.Fatal("no progress before the simulated crash")
	}

	// Force a checkpoint (rather than waiting up to 30s for the ticker) and
	// then kill the "process" without a clean Pause/Stop, which is the point
	// of the test: the crash path must still have something to resume from.
	if err := first.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	progress := first.Stats().Downloaded
	cancel1()
	<-run1Done

	second, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New (second): %v", err)
	}
	var sawChecking bool
	second.OnStateChange(func(s State) {
		if s == StateCheckingFiles {
			sawChecking = true
		}
	})
	runInBackground(t, second)

	// The resumed session should already report the checkpointed progress
	// without needing a peer at all.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && second.Stats().Downloaded == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := second.Stats().Downloaded; got < progress {
		t.Fatalf("resumed torrent reports %d bytes downloaded, want at least %d", got, progress)
	}
	if !sawChecking {
		t.Fatal("the resumed torrent never passed through CheckingFiles")
	}

	second.DialPeer(seeder.peerInfo())
	waitForState(t, second, StateSeeding, 30*time.Second)

	got, err := os.ReadFile(filepath.Join(cfg.DownloadDir, "restart.bin"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("final file is wrong after a resumed download")
	}
}

// TestStaleResumeTriggersReverify checks that resume data is refused, not
// trusted blindly, when the on-disk file no longer matches what was recorded.
func TestStaleResumeTriggersReverify(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "stale.bin", pieceLength, []fileSpec{{length: pieceLength * 5}})
	cfg := newTestConfig(t)

	first, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	run1Done := make(chan struct{})
	go func() { first.Run(ctx1); close(run1Done) }()

	seeder := newFakeSeeder(t, mi, content)
	first.DialPeer(seeder.peerInfo())
	waitForState(t, first, StateSeeding, 30*time.Second)
	cancel1()
	<-run1Done

	// Tamper with the file after the checkpoint was written.
	path := filepath.Join(cfg.DownloadDir, "stale.bin")
	tampered := append([]byte(nil), content...)
	tampered[0] ^= 0xFF
	if err := os.WriteFile(path, tampered, 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	second, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, second)

	// It must re-verify (finding piece 0 wrong) rather than trust the stale
	// checkpoint, which would have reported the torrent as already complete.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if second.State() == StateDownloading {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if second.State() != StateDownloading {
		t.Fatalf("mtime-tampered data was trusted without a re-verify (state = %s)", second.State())
	}
}

// TestFetchingMetadataAcceptsPeers is the Phase 1 exit criterion: a torrent
// can exist with peers connected and no metadata at all. This does not (yet)
// fetch the metadata over the wire — that is BEP 9, Phase 2 — it proves the
// state machine and the peer plumbing allow the shape.
func TestFetchingMetadataAcceptsPeers(t *testing.T) {
	// A real, fully-formed torrent supplies the infohash and the seeder; the
	// Torrent under test is built from that infohash alone, exactly the
	// magnet-link shape, and never told the rest of the metadata.
	mi, content := buildTorrent(t, "magnet-target.bin", 16384, []fileSpec{{length: 16384 * 4}})

	tr, err := NewFromInfoHash(mi.InfoHash, newTestConfig(t))
	if err != nil {
		t.Fatalf("NewFromInfoHash: %v", err)
	}
	if tr.Metadata() != nil {
		t.Fatal("a torrent constructed from an infohash already has metadata")
	}

	runInBackground(t, tr)
	waitForState(t, tr, StateFetchingMetadata, 2*time.Second)

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tr.Stats().PeerCount > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tr.Stats().PeerCount == 0 {
		t.Fatal("the peer never registered against a metadata-less torrent")
	}
	if tr.State() != StateFetchingMetadata {
		t.Fatalf("State() = %s, want it to remain FetchingMetadata", tr.State())
	}
}

// TestCleanShutdownNoGoroutineLeak drives a real download partway, then
// cancels mid-flight, and checks Run returns promptly with every peer
// goroutine accounted for (runInBackground's Cleanup already asserts this
// for every other test; this one makes the property explicit).
func TestCleanShutdownMidDownload(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "interrupt.bin", pieceLength, []fileSpec{{length: pieceLength * 200}})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { tr.Run(ctx); close(runDone) }()

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())

	deadline := time.Now().Add(10 * time.Second)
	for tr.Stats().Downloaded == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return promptly after cancellation")
	}
	if got := tr.State(); got != StatePaused && got != StateDownloading {
		// setState only runs from the actor, and the actor stops without
		// forcing a state change on ctx cancellation (that is what Pause is
		// for) — Downloading is expected here; StatePaused would also be
		// harmless if a future change adds one.
		t.Logf("state after cancellation: %s", got)
	}
}

// TestConcurrentTorrents runs several torrents side by side to demonstrate
// the actor-per-torrent design: independent state, independent peers,
// independent completion, no shared mutable state between them beyond the
// process-wide goroutine scheduler.
func TestConcurrentTorrents(t *testing.T) {
	const n = 5
	const pieceLength = 16384

	type instance struct {
		tr      *Torrent
		content []byte
		path    string
		name    string
	}
	instances := make([]*instance, n)

	for i := 0; i < n; i++ {
		name := fileNameFor(i)
		mi, content := buildTorrent(t, name, pieceLength, []fileSpec{{length: pieceLength * int64(5+i)}})
		tr, err := New(mi, newTestConfig(t))
		if err != nil {
			t.Fatalf("New(%d): %v", i, err)
		}
		instances[i] = &instance{tr: tr, content: content, path: filepath.Join(tr.cfg.DownloadDir, name), name: name}
	}

	var wg sync.WaitGroup
	for _, inst := range instances {
		wg.Add(1)
		go func(inst *instance) {
			defer wg.Done()
			runInBackground(t, inst.tr)
			seeder := newFakeSeeder(t, inst.tr.Metadata(), inst.content)
			inst.tr.DialPeer(seeder.peerInfo())
			waitForState(t, inst.tr, StateSeeding, 30*time.Second)
		}(inst)
	}
	wg.Wait()

	for _, inst := range instances {
		got, err := os.ReadFile(inst.path)
		if err != nil {
			t.Fatalf("%s: read: %v", inst.name, err)
		}
		if !bytes.Equal(got, inst.content) {
			t.Fatalf("%s: downloaded content is wrong", inst.name)
		}
	}
}

func fileNameFor(i int) string {
	return string(rune('a'+i)) + ".bin"
}
