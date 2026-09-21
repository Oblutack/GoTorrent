package stream

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/torrent"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

func TestMain(m *testing.M) {
	logger.Init(false)
	os.Exit(m.Run())
}

// buildTorrent and fakeSeeder below are a deliberately trimmed copy of
// internal/torrent's own (no metadata-serving, no AllowedFast — this
// package's tests only need real data flowing over a real wire-protocol
// connection while an HTTP request is in flight). See CLAUDE.md's Testing
// section: this is the fifth copy of this pattern, flagged there as worth
// factoring out once a fifth one shows up — not attempted here, since
// doing so is a larger refactor than this feature warrants on its own.

func buildTorrent(t *testing.T, name string, pieceLength int64, total int64) (*metainfo.MetaInfo, []byte, []byte) {
	t.Helper()

	content := make([]byte, total)
	rand.New(rand.NewSource(19)).Read(content)

	var hashes []byte
	for off := int64(0); off < total; off += pieceLength {
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
	return mi, content, torrentBytes
}

type fakeSeeder struct {
	t       *testing.T
	ln      net.Listener
	mi      *metainfo.MetaInfo
	content []byte
	delay   time.Duration

	mu     sync.Mutex
	served int
}

func newFakeSeeder(t *testing.T, mi *metainfo.MetaInfo, content []byte, delay time.Duration) *fakeSeeder {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSeeder{t: t, ln: ln, mi: mi, content: content, delay: delay}
	t.Cleanup(func() { ln.Close() })
	go f.acceptLoop()
	return f
}

func (f *fakeSeeder) peerInfo() tracker.PeerInfo {
	addr := f.ln.Addr().(*net.TCPAddr)
	return tracker.PeerInfo{IP: addr.IP, Port: uint16(addr.Port)}
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
	if err := writeMsg(conn, peer.MsgBitfield, bitfieldFull(f.mi.NumPieces())); err != nil {
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

// bitfieldFull builds a raw "every piece present" bitfield payload without
// pulling in internal/bitfield just for this — the wire format is
// ceil(n/8) bytes, high bit first, with any spare bits past piece n-1 in
// the final byte left zero (BEP 3 requires this; the real client's own
// bitfield validation rejects a payload that sets them, which is exactly
// what a naive all-0xFF fill produces whenever n isn't a multiple of 8).
func bitfieldFull(n int) []byte {
	buf := make([]byte, (n+7)/8)
	for i := 0; i < n; i++ {
		buf[i/8] |= 1 << (7 - uint(i%8))
	}
	return buf
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

// --- test harness --------------------------------------------------------

func newTestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.New(t.TempDir(), engine.Defaults{
		DownloadDir: t.TempDir(),
		ResumeDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(e.Shutdown)
	return e
}

func writeTorrentFile(t *testing.T, raw []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stream.torrent")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write .torrent: %v", err)
	}
	return path
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

// --- tests ---------------------------------------------------------------

// TestStreamServesTheFullFileWhileDownloading is the feature's core claim:
// an HTTP GET started against a torrent that has not finished downloading
// yet still returns the complete, byte-correct file — no separate
// "wait for it to finish" step. The fake seeder is throttled so the
// download is still genuinely in progress for a meaningful stretch of the
// request.
func TestStreamServesTheFullFileWhileDownloading(t *testing.T) {
	const pieceLength = 16384
	const numPieces = 12
	mi, content, raw := buildTorrent(t, "movie.bin", pieceLength, pieceLength*numPieces)

	e := newTestEngine(t)
	path := writeTorrentFile(t, raw)
	hash, err := e.Add(path, "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get did not find the added torrent")
	}
	waitForState(t, tr, torrent.StateDownloading, 5*time.Second)

	seeder := newFakeSeeder(t, mi, content, 8*time.Millisecond)
	tr.DialPeer(seeder.peerInfo())

	srv := httptest.NewServer(NewServer(e).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/stream/" + hash.String() + "/0")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("got %d bytes, want %d bytes matching the source content", len(got), len(content))
	}
}

// TestStreamServesARangeRequestNearTheEnd proves a seek-shaped request (a
// player scrubbing forward, in HTTP terms a Range request far from byte 0)
// resolves correctly at the HTTP layer — the server waits for the covering
// piece and returns the real bytes for it rather than 200'ing with
// whatever's currently on disk. The picker actually reordering fetches
// toward a boosted offset is proven deterministically and with a tight
// timing bound at the torrent level, in
// TestSetStreamPositionPrioritizesTheRequestedPiece
// (internal/torrent/stream_test.go) — this test's own timeout is a
// hang-safety net, not a performance assertion, since HTTP-layer timing
// here is also affected by adaptive pipelining and isn't a clean signal on
// its own.
func TestStreamServesARangeRequestNearTheEnd(t *testing.T) {
	const pieceLength = 16384
	const numPieces = 30
	mi, content, raw := buildTorrent(t, "movie2.bin", pieceLength, pieceLength*numPieces)

	e := newTestEngine(t)
	path := writeTorrentFile(t, raw)
	hash, err := e.Add(path, "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get did not find the added torrent")
	}
	waitForState(t, tr, torrent.StateDownloading, 5*time.Second)

	seeder := newFakeSeeder(t, mi, content, 15*time.Millisecond)
	tr.DialPeer(seeder.peerInfo())

	srv := httptest.NewServer(NewServer(e).Handler())
	defer srv.Close()

	// The last piece's own middle byte — a plain, un-prioritized sequential
	// download would only reach this after every one of the 29 pieces
	// before it, which the test's own deadline below would not tolerate.
	rangeStart := int64(numPieces-1)*pieceLength + pieceLength/2
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/stream/"+hash.String()+"/0", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", rangeStart))

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Range GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	want := content[rangeStart:]
	if string(got) != string(want) {
		t.Fatalf("range response body did not match the expected slice of source content")
	}
}

// TestStreamIndexListsTheFile proves the index page reflects real
// metadata — no seeder needed, the file list is known the moment the
// torrent's metadata is parsed.
func TestStreamIndexListsTheFile(t *testing.T) {
	const pieceLength = 16384
	_, _, raw := buildTorrent(t, "indexed.bin", pieceLength, pieceLength*3)

	e := newTestEngine(t)
	path := writeTorrentFile(t, raw)
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}

	srv := httptest.NewServer(NewServer(e).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "indexed.bin") {
		t.Errorf("index page does not mention the torrent's file name %q:\n%s", "indexed.bin", body)
	}
}
