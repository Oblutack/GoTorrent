package torrent

import (
	"bytes"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// waitForConnectedPeer polls tr.Peers() until at least one shows up,
// mirroring holepunch_test.go's own inline pattern.
func waitForConnectedPeer(t *testing.T, tr *Torrent) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if peers := tr.Peers(); len(peers) > 0 {
			return peers[0].Addr
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("never connected to the peer")
	return ""
}

// stripPieceLayers returns a shallow copy of mi with its PieceLayers
// entirely cleared, standing in for what a real BEP 9 metadata exchange
// hands a magnet-sourced torrent - ut_metadata only ever delivers the info
// dict, never the separate top-level piece_layers dict (see v2hash.go's
// own package doc comment) - so a freshly magnet-fetched v2/hybrid
// MetaInfo genuinely has no PieceLayers at all until this exchange runs.
func stripPieceLayers(mi *metainfo.MetaInfo) *metainfo.MetaInfo {
	next := *mi
	next.PieceLayers = nil
	return &next
}

// TestRequestPieceLayerReconstructsAStrippedLayer is the real end-to-end
// proof of this commit's whole point: a v2 torrent whose metadata arrived
// with no 'piece layers' entry (the magnet case) asks a real connected
// peer for fileA's layer via the new hash-request/hashes wire exchange
// (internal/peer's v2wire.go/v2hash.go) and reconstructs it, verified
// against the file's own already-known PiecesRoot via merkle.FileRoot
// (onHashMessage) - and the reconstructed bytes match the seeder's real
// original piece_layers entry exactly, not just "some bytes arrived."
func TestRequestPieceLayerReconstructsAStrippedLayer(t *testing.T) {
	const pieceLength = 32768
	files := map[string][]byte{
		"fileA.bin": genBytesV2(102400), // 4 real pieces -> a real piece_layers entry
		"fileB.bin": genBytesV2(4096),   // sole-piece file -> no piece_layers entry at all
	}
	mi := buildV2TestTorrent(t, pieceLength, false, files)
	if len(mi.V2Files) != 2 {
		t.Fatalf("test setup: expected 2 files, got %d", len(mi.V2Files))
	}
	fileAIndex := -1
	for i, f := range mi.V2Files {
		if f.Path[len(f.Path)-1] == "fileA.bin" {
			fileAIndex = i
		}
	}
	if fileAIndex < 0 {
		t.Fatal("test setup: fileA.bin not found in V2Files")
	}
	root := mi.V2Files[fileAIndex].PiecesRoot
	wantLayer, ok := mi.PieceLayers[root]
	if !ok || len(wantLayer) == 0 {
		t.Fatal("test setup: expected a real piece_layers entry for fileA.bin")
	}

	seederCfg := newTestConfig(t)
	preSeedV2Torrent(t, seederCfg, mi, files)
	seeder, err := New(mi, seederCfg)
	if err != nil {
		t.Fatalf("New (seeder): %v", err)
	}
	_, stopSeeder := runInBackground(t, seeder)
	defer stopSeeder()
	waitForState(t, seeder, StateSeeding, 5*time.Second)

	seederPeer := listenAndRoute(t, seeder)

	leecherMi := stripPieceLayers(mi)
	leecherCfg := newTestConfig(t)
	leecher, err := New(leecherMi, leecherCfg)
	if err != nil {
		t.Fatalf("New (leecher): %v", err)
	}
	_, stopLeecher := runInBackground(t, leecher)
	defer stopLeecher()

	leecher.DialPeer(seederPeer)
	peerAddr := waitForConnectedPeer(t, leecher)

	if err := leecher.RequestPieceLayer(peerAddr, fileAIndex); err != nil {
		t.Fatalf("RequestPieceLayer: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		if layer, ok := leecher.mi.Load().PieceLayers[root]; ok {
			got = layer
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("piece layer was never reconstructed")
	}
	if !bytes.Equal(got, wantLayer) {
		t.Fatalf("reconstructed piece layer differs from the real original:\ngot:  %x\nwant: %x", got, wantLayer)
	}
}

// TestRequestPieceLayerFailsForUnknownPeer proves the control-path
// validation: asking a peer this torrent isn't even connected to returns
// a real error, mirroring RequestHolepunch's own equivalent test.
func TestRequestPieceLayerFailsForUnknownPeer(t *testing.T) {
	files := map[string][]byte{"fileA.bin": genBytesV2(102400)}
	mi := buildV2TestTorrent(t, 32768, false, files)

	cfg := newTestConfig(t)
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	if err := tr.RequestPieceLayer("127.0.0.1:1", 0); err == nil {
		t.Fatal("RequestPieceLayer against an unconnected peer returned nil, want an error")
	}
}

// TestRequestPieceLayerFailsForV1Torrent proves a plain v1 torrent (no
// MetaVersion 2 at all) rejects the request outright rather than
// panicking on a nil/empty V2Files.
func TestRequestPieceLayerFailsForV1Torrent(t *testing.T) {
	mi, _ := buildTorrent(t, "v2hash-v1only", 16384, []fileSpec{{length: 16384}})
	cfg := newTestConfig(t)
	tr, err := New(mi, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	if err := tr.RequestPieceLayer("127.0.0.1:1", 0); err == nil {
		t.Fatal("RequestPieceLayer against a v1 torrent returned nil, want an error")
	}
}

// TestRequestPieceLayerFailsForSinglePieceFile proves the local, no-network
// short-circuit for a file that has only one piece - its PiecesRoot IS its
// own root hash directly, so a separate layer request is never meaningful
// (see requestPieceLayer's own doc comment).
func TestRequestPieceLayerFailsForSinglePieceFile(t *testing.T) {
	const pieceLength = 32768
	files := map[string][]byte{
		"fileA.bin": genBytesV2(102400),
		"fileB.bin": genBytesV2(4096), // sole piece
	}
	mi := buildV2TestTorrent(t, pieceLength, false, files)
	fileBIndex := -1
	for i, f := range mi.V2Files {
		if f.Path[len(f.Path)-1] == "fileB.bin" {
			fileBIndex = i
		}
	}
	if fileBIndex < 0 {
		t.Fatal("test setup: fileB.bin not found in V2Files")
	}

	seederCfg := newTestConfig(t)
	preSeedV2Torrent(t, seederCfg, mi, files)
	seeder, err := New(mi, seederCfg)
	if err != nil {
		t.Fatalf("New (seeder): %v", err)
	}
	_, stopSeeder := runInBackground(t, seeder)
	defer stopSeeder()
	waitForState(t, seeder, StateSeeding, 5*time.Second)
	seederPeer := listenAndRoute(t, seeder)

	leecherCfg := newTestConfig(t)
	leecher, err := New(mi, leecherCfg)
	if err != nil {
		t.Fatalf("New (leecher): %v", err)
	}
	_, stopLeecher := runInBackground(t, leecher)
	defer stopLeecher()
	leecher.DialPeer(seederPeer)
	peerAddr := waitForConnectedPeer(t, leecher)

	if err := leecher.RequestPieceLayer(peerAddr, fileBIndex); err == nil {
		t.Fatal("RequestPieceLayer for a single-piece file returned nil, want an error")
	}
}
