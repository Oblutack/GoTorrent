package session

import (
	"bytes"
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

	"github.com/Oblutack/GoTorrent/internal/gobencode"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

func TestMain(m *testing.M) {
	logger.Init(false)
	os.Exit(m.Run())
}

// --- synthetic torrent -----------------------------------------------------

type fileSpec struct {
	path   []string
	length int64
}

// buildTorrent produces a real .torrent byte stream plus the content it
// describes, so tests exercise the actual metainfo parser rather than a
// hand-built struct.
func buildTorrent(t *testing.T, name string, pieceLength int64, files []fileSpec) (*metainfo.MetaInfo, []byte) {
	t.Helper()

	var total int64
	for _, f := range files {
		total += f.length
	}

	content := make([]byte, total)
	rng := rand.New(rand.NewSource(1))
	rng.Read(content)

	var hashes []byte
	for off := int64(0); off < total; off += pieceLength {
		end := off + pieceLength
		if end > total {
			end = total
		}
		sum := sha1.Sum(content[off:end])
		hashes = append(hashes, sum[:]...)
	}

	info := map[string]interface{}{
		"name":         name,
		"piece length": pieceLength,
		"pieces":       string(hashes),
	}
	if len(files) == 1 && len(files[0].path) == 0 {
		info["length"] = files[0].length
	} else {
		list := make([]interface{}, 0, len(files))
		for _, f := range files {
			parts := make([]interface{}, len(f.path))
			for i, p := range f.path {
				parts[i] = p
			}
			list = append(list, map[string]interface{}{
				"length": f.length,
				"path":   parts,
			})
		}
		info["files"] = list
	}

	torrent := map[string]interface{}{
		"announce": "http://127.0.0.1:1/announce",
		"info":     info,
	}

	var buf bytes.Buffer
	if err := gobencode.Encode(&buf, torrent); err != nil {
		t.Fatalf("encode torrent: %v", err)
	}

	mi, err := metainfo.New(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("parse torrent: %v", err)
	}
	if mi.TotalLength != total {
		t.Fatalf("metainfo total length %d, expected %d", mi.TotalLength, total)
	}
	return mi, content
}

// --- fake seeder -----------------------------------------------------------

// fakeSeeder is a minimal BitTorrent peer that has the whole torrent and
// answers every request from an in-memory copy of the content.
type fakeSeeder struct {
	t       *testing.T
	ln      net.Listener
	mi      *metainfo.MetaInfo
	content []byte

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

	// Advertise the complete torrent, then unchoke unconditionally.
	numPieces := len(f.mi.PieceHashes)
	bf := peer.NewBitfield(numPieces)
	for i := 0; i < numPieces; i++ {
		bf.SetPiece(uint32(i))
	}
	if err := writeMsg(conn, peer.MsgBitfield, bf); err != nil {
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
		return 0, nil, nil // keep-alive
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(conn, body); err != nil {
		return 0, nil, err
	}
	return peer.MessageID(body[0]), body[1:], nil
}

// --- the tests -------------------------------------------------------------

// runDownload drives a real TorrentSession against a loopback seeder and
// returns once downloadLoop reports the torrent complete.
func runDownload(t *testing.T, mi *metainfo.MetaInfo, content []byte, dir string, watch func(*TorrentSession)) *TorrentSession {
	t.Helper()

	s, err := New(mi, 6881, dir)
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	if err := s.preallocateFiles(); err != nil {
		t.Fatalf("preallocateFiles: %v", err)
	}
	s.populateWorkQueue()

	seeder := newFakeSeeder(t, mi, content)
	go s.connectToPeer(seeder.peerInfo())

	if watch != nil {
		go watch(s)
	}

	done := make(chan error, 1)
	go func() { done <- s.downloadLoop() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("downloadLoop: %v", err)
		}
	case <-time.After(60 * time.Second):
		s.mu.Lock()
		left, active := s.TrackerRequest.Left, len(s.ActivePieces)
		s.mu.Unlock()
		t.Fatalf("download did not finish: %d bytes left, %d active pieces, %d blocks served",
			left, active, seeder.blocksServed())
	}

	if seeder.blocksServed() == 0 {
		t.Fatal("the seeder never served a block")
	}
	return s
}

// TestDownloadSingleFile is the end-to-end path: metainfo parse, work queue,
// wire protocol, SHA-1 verification and disk write, checked byte for byte.
func TestDownloadSingleFile(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "payload.bin", pieceLength, []fileSpec{
		{length: pieceLength*20 + 777}, // a deliberately short final piece
	})

	dir := t.TempDir()
	runDownload(t, mi, content, dir, nil)

	got, err := os.ReadFile(filepath.Join(dir, "payload.bin"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded file differs from the source (%d bytes vs %d)", len(got), len(content))
	}
}

// TestDownloadMultiFile also covers the storage.Layout write path and nested
// directory creation.
func TestDownloadMultiFile(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "MyTorrent", pieceLength, []fileSpec{
		{path: []string{"readme.txt"}, length: 1000},
		{path: []string{"data", "part1.bin"}, length: pieceLength * 3},
		{path: []string{"data", "nested", "part2.bin"}, length: pieceLength*2 + 55},
	})

	dir := t.TempDir()
	runDownload(t, mi, content, dir, nil)

	base := filepath.Join(dir, "MyTorrent")
	var assembled []byte
	for _, rel := range [][]string{
		{"readme.txt"},
		{"data", "part1.bin"},
		{"data", "nested", "part2.bin"},
	} {
		b, err := os.ReadFile(filepath.Join(append([]string{base}, rel...)...))
		if err != nil {
			t.Fatalf("read %v: %v", rel, err)
		}
		assembled = append(assembled, b...)
	}
	if !bytes.Equal(assembled, content) {
		t.Fatalf("reassembled files differ from the source (%d bytes vs %d)", len(assembled), len(content))
	}
}

// TestActivePiecesStayCapped is the memory bound from roadmap 0.1: every active
// piece holds a full piece buffer, so ActivePieces must never grow past
// maxInFlightPieces no matter how many pieces the torrent has.
func TestActivePiecesStayCapped(t *testing.T) {
	const pieceLength = 16384
	numPieces := maxInFlightPieces * 5

	mi, content := buildTorrent(t, "big.bin", pieceLength, []fileSpec{
		{length: pieceLength * int64(numPieces)},
	})
	if len(mi.PieceHashes) != numPieces {
		t.Fatalf("fixture has %d pieces, expected %d", len(mi.PieceHashes), numPieces)
	}

	var mu sync.Mutex
	peak := 0
	stop := make(chan struct{})
	defer close(stop)

	watch := func(s *TorrentSession) {
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				n := len(s.ActivePieces)
				buffered := 0
				for _, pw := range s.ActivePieces {
					if pw.Buffer != nil {
						buffered++
					}
				}
				s.mu.Unlock()

				mu.Lock()
				if n > peak {
					peak = n
				}
				mu.Unlock()

				if buffered > maxInFlightPieces {
					t.Errorf("%d pieces held a buffer, cap is %d", buffered, maxInFlightPieces)
					return
				}
			}
		}
	}

	s := runDownload(t, mi, content, t.TempDir(), watch)

	mu.Lock()
	observed := peak
	mu.Unlock()

	if observed > maxInFlightPieces {
		t.Fatalf("ActivePieces peaked at %d, cap is %d", observed, maxInFlightPieces)
	}
	if observed == 0 {
		t.Fatal("the watcher never observed an active piece; the test proves nothing")
	}
	t.Logf("peak ActivePieces was %d of %d pieces (cap %d)", observed, numPieces, maxInFlightPieces)

	// Every buffer must have gone back to the pool by the end.
	s.mu.Lock()
	leftOver := len(s.ActivePieces)
	s.mu.Unlock()
	if leftOver != 0 {
		t.Fatalf("%d pieces were still active after the download finished", leftOver)
	}
}

// TestSeedingReadPath checks the other direction: once the data is on disk,
// readBlockFromDisk must hand back exactly what a peer asked for. This is the
// whole upload path, including the multi-file offset arithmetic.
func TestSeedingReadPath(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "MyTorrent", pieceLength, []fileSpec{
		{path: []string{"a.bin"}, length: 5000},
		{path: []string{"b.bin"}, length: pieceLength * 4},
	})

	dir := t.TempDir()
	s := runDownload(t, mi, content, dir, nil)

	for _, tc := range []struct {
		index, begin, length uint32
	}{
		{0, 0, 4096},                // inside the first file
		{0, 4096, 4096},             // spans the a.bin/b.bin boundary
		{1, 0, peer.MaxBlockLength}, // wholly inside b.bin
		{4, 0, uint32(mi.TotalLength - 4*pieceLength)}, // the short final piece
	} {
		got, err := s.readBlockFromDisk(tc.index, tc.begin, tc.length)
		if err != nil {
			t.Fatalf("readBlockFromDisk(%d, %d, %d): %v", tc.index, tc.begin, tc.length, err)
		}
		start := int64(tc.index)*pieceLength + int64(tc.begin)
		want := content[start : start+int64(tc.length)]
		if !bytes.Equal(got, want) {
			t.Fatalf("readBlockFromDisk(%d, %d, %d) returned the wrong bytes", tc.index, tc.begin, tc.length)
		}
	}
}

// TestResumeFromState checks that a partially complete download picks up where
// it left off instead of re-fetching everything.
func TestResumeFromState(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "resume.bin", pieceLength, []fileSpec{
		{length: pieceLength * 10},
	})

	dir := t.TempDir()
	runDownload(t, mi, content, dir, nil)

	// Persist the completed bitfield, then start a fresh session over it.
	s, err := New(mi, 6881, dir)
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	for i := range mi.PieceHashes {
		s.OurBitfield.SetPiece(uint32(i))
	}
	if err := s.saveState(); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	resumed, err := New(mi, 6881, dir)
	if err != nil {
		t.Fatalf("session.New on resume: %v", err)
	}
	if resumed.TrackerRequest.Left != 0 {
		t.Fatalf("resumed session still wants %d bytes", resumed.TrackerRequest.Left)
	}
	if resumed.TrackerRequest.Downloaded != mi.TotalLength {
		t.Fatalf("resumed session reports %d downloaded, expected %d",
			resumed.TrackerRequest.Downloaded, mi.TotalLength)
	}
	_ = content
}
