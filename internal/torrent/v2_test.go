package torrent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/storage"
)

// genBytesV2 mirrors internal/merkle's/internal/metainfo's own test
// fixture generator (a Knuth multiplicative hash of the byte index), so
// content built here is real and non-trivial without embedding binary
// blobs or needing to match those packages' own generator byte-for-byte
// (this package never cross-checks hash values directly, only that a real
// transfer completes byte-exact).
func genBytesV2(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		x := uint32(i) * 2654435761
		out[i] = byte(x >> 24)
	}
	return out
}

// buildV2TestTorrent writes real file content to a source directory and
// builds a real v2 or hybrid MetaInfo via metainfo.Build, reusing the
// already-tested BEP 52 write side rather than hand-encoding bencode.
// Every file always gets a real (non-nil) path, so the result is always
// multi-file-shaped even for a single file — see internal/storage's own
// equivalent fixture for why that keeps this simpler without losing real
// coverage (internal/metainfo's own tests already cover the true
// single-file layout case).
func buildV2TestTorrent(t *testing.T, pieceLength int64, hybrid bool, files map[string][]byte) *metainfo.MetaInfo {
	t.Helper()
	srcDir := t.TempDir()

	var names []string
	for name := range files {
		names = append(names, name)
	}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}

	var createFiles []metainfo.CreateFile
	for _, name := range names {
		content := files[name]
		path := filepath.Join(srcDir, name)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		createFiles = append(createFiles, metainfo.CreateFile{
			Path: []string{name}, SourcePath: path, Length: int64(len(content)),
		})
	}

	opts := metainfo.CreateOptions{
		Name: "v2torrenttest", PieceLength: pieceLength, MetaVersion: 2, Hybrid: hybrid, Files: createFiles,
	}
	_, mi, err := metainfo.Build(opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return mi
}

// preSeedV2Torrent writes mi's real content directly to disk under cfg's
// download directory, at exactly the paths a real Torrent's own storage
// construction will look for them — via a standalone storage.Storage built
// purely to Allocate/WriteAt correctly (mirroring openMetadata's own
// construction options), never touched again once this returns. A
// subsequent New(mi, cfg)+Run finds this content already on disk and
// verifies straight to StateSeeding without needing a peer at all — the
// seed side of a real two-actor transfer, the same role
// seedlimit_test.go's newPreSeededTorrent plays for v1, generalized here
// for a torrent whose files.
func preSeedV2Torrent(t *testing.T, cfg Config, mi *metainfo.MetaInfo, files map[string][]byte) {
	t.Helper()
	st, err := storage.New(cfg.DownloadDir, mi, storage.WithContentLayout(cfg.ContentLayout))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer st.Close()
	if err := st.Allocate(t.Context()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	for i, fr := range st.Files() {
		var name string
		switch {
		case mi.IsPureV2():
			p := mi.V2Files[i].Path
			name = p[len(p)-1]
		case mi.Info.IsMultiFile():
			f := mi.Info.Files[i]
			if f.IsPadding() {
				continue // already all-zero via Sparse allocation
			}
			name = f.Path[len(f.Path)-1]
		default:
			name = mi.Info.Name
		}
		content, ok := files[name]
		if !ok {
			t.Fatalf("no fixture content registered for file %q", name)
		}
		if _, err := st.WriteAt(content, fr.Offset); err != nil {
			t.Fatalf("WriteAt(%s, %d): %v", name, fr.Offset, err)
		}
	}
}

// totalBytes sums every file's content, for a leecher's own Downloaded
// stat assertion.
func totalBytes(files map[string][]byte) int64 {
	var n int64
	for _, c := range files {
		n += int64(len(c))
	}
	return n
}

// readBackAndCompare reads every real file the leecher downloaded and
// confirms it matches the source content exactly, resolving each file's
// real on-disk path via a fresh storage.Storage the same way
// preSeedV2Torrent locates the seeder's own files - never assuming a
// fixed layout independently.
func readBackAndCompare(t *testing.T, cfg Config, mi *metainfo.MetaInfo, files map[string][]byte) {
	t.Helper()
	st, err := storage.New(cfg.DownloadDir, mi, storage.WithContentLayout(cfg.ContentLayout))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer st.Close()
	for i, fr := range st.Files() {
		var name string
		switch {
		case mi.IsPureV2():
			p := mi.V2Files[i].Path
			name = p[len(p)-1]
		case mi.Info.IsMultiFile():
			f := mi.Info.Files[i]
			if f.IsPadding() {
				continue
			}
			name = f.Path[len(f.Path)-1]
		default:
			name = mi.Info.Name
		}
		want, ok := files[name]
		if !ok {
			continue
		}
		got := make([]byte, len(want))
		if _, err := st.ReadAt(got, fr.Offset); err != nil {
			t.Fatalf("reading back %s: %v", name, err)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("downloaded content for %s differs from the source at byte %d", name, j)
			}
		}
	}
}

// TestFullDownloadPureV2 is the real proof this feature's whole pipeline
// works end to end for a pure-v2 (no v1 'pieces' at all) multi-file
// torrent: a real seeder and a real leecher, two genuine *Torrent actors
// talking over real TCP loopback, the leecher downloading, merkle-
// verifying (internal/storage, commit 4) and writing every piece via the
// real v2-aware offset resolution this commit added (onBlock/
// readBlockSafe/PieceOffset), reaching StateSeeding with byte-exact
// content.
func TestFullDownloadPureV2(t *testing.T) {
	const pieceLength = 32768
	files := map[string][]byte{
		"fileA.bin": genBytesV2(102400), // needs a real 'piece layers' entry
		"fileB.bin": genBytesV2(4096),   // sole-piece file
	}
	mi := buildV2TestTorrent(t, pieceLength, false, files)

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
	waitForState(t, leecher, StateSeeding, 30*time.Second)

	if got, want := leecher.Stats().Downloaded, totalBytes(files); got != want {
		t.Fatalf("Downloaded = %d, want %d", got, want)
	}
	readBackAndCompare(t, leecherCfg, mi, files)
}

// TestFullDownloadHybridMultiFileWithPadding is the hybrid counterpart,
// deliberately using the two-file/forced-padding geometry that caught a
// real bug in commit 4's own storage-level test (v2's per-file piece
// length differing from v1's flat padded length for the piece a BEP 47
// padding file closes out) - proving that fix holds for a real end-to-end
// transfer, not just a synthetic on-disk verification.
func TestFullDownloadHybridMultiFileWithPadding(t *testing.T) {
	const pieceLength = 32768
	files := map[string][]byte{
		"fileA.bin": genBytesV2(50000), // forces a real BEP 47 padding file
		"fileB.bin": genBytesV2(20000),
	}
	mi := buildV2TestTorrent(t, pieceLength, true, files)
	if len(mi.PieceHashes) == 0 {
		t.Fatal("test setup: expected a real hybrid torrent with v1 pieces")
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
	waitForState(t, leecher, StateSeeding, 30*time.Second)

	readBackAndCompare(t, leecherCfg, mi, files)
}

// TestNewUsesTheV2IdentityForAPureV2Torrent pins down a real bug found
// while writing TestMagnetV2FullDownloadWithHashExchange below: New used
// to unconditionally use mi.InfoHash (v1 SHA-1, computed unconditionally
// over InfoBytes regardless of MetaVersion — see metainfo.go's own setInfo)
// as the wire/tracker/DHT identity, even for a pure-v2 torrent with no v1
// 'pieces' at all. That silently diverged from BEP 52's own truncation
// rule and — the way it was actually caught — made a New()-constructed
// seeder and a NewFromInfoHashV2-constructed leecher (the real magnet
// shape) advertise two different infohashes in their handshakes, so the
// leecher's dial was rejected outright and the whole transfer hung at
// FetchingMetadata forever.
func TestNewUsesTheV2IdentityForAPureV2Torrent(t *testing.T) {
	files := map[string][]byte{"fileA.bin": genBytesV2(102400)}
	mi := buildV2TestTorrent(t, 32768, false, files)
	if mi.InfoHashV2.IsZero() {
		t.Fatal("test setup: expected a real v2 infohash")
	}

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if want := mi.InfoHashV2.Truncated20(); tr.InfoHash() != want {
		t.Fatalf("InfoHash() = %s, want %s (the truncated v2 identity)", tr.InfoHash(), want)
	}
	if tr.InfoHash() == mi.InfoHash {
		t.Fatal("InfoHash() equals the meaningless v1 SHA-1 over a pure-v2 torrent's info bytes")
	}
}

// TestMagnetV2FullDownloadWithHashExchange is the real end-to-end proof the
// "full v2, including magnet support" scope choice was worth taking on: a
// leecher constructed via NewFromInfoHashV2 with zero prior metadata at
// all — the genuine v2-only magnet shape, xt=urn:btmh: with no
// xt=urn:btih: — fetches the info dict from a real seeder over BEP 9
// (metadata.go's onMetadataPiece, its SHA-256 verification branch), finds
// its freshly-parsed metadata missing a piece_layers entry (BEP 9's own
// ut_metadata never carries it — see v2hash.go's own package doc comment),
// reconstructs it for real over the new BEP 52 hash-request/hashes wire
// exchange (registerPeer's proactive requestMissingPieceLayersFrom, since
// the peer connects only after metadata already exists here), and then
// downloads and verifies every piece, reaching StateSeeding byte-exact —
// commit 7's serving/requesting halves and commit 8's magnet/identity
// plumbing, proven together against real sockets rather than in isolation.
func TestMagnetV2FullDownloadWithHashExchange(t *testing.T) {
	const pieceLength = 32768
	files := map[string][]byte{
		"fileA.bin": genBytesV2(102400), // 4 real pieces -> needs a real piece_layers entry
		"fileB.bin": genBytesV2(4096),   // sole-piece file
	}
	mi := buildV2TestTorrent(t, pieceLength, false, files)
	if mi.InfoHashV2.IsZero() {
		t.Fatal("test setup: expected a real v2 infohash")
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
	leecher, err := NewFromInfoHashV2(mi.InfoHashV2, leecherCfg)
	if err != nil {
		t.Fatalf("NewFromInfoHashV2: %v", err)
	}
	_, stopLeecher := runInBackground(t, leecher)
	defer stopLeecher()
	waitForState(t, leecher, StateFetchingMetadata, 2*time.Second)

	leecher.DialPeer(seederPeer)
	waitForState(t, leecher, StateSeeding, 30*time.Second)

	got := leecher.Metadata()
	if got == nil || got.InfoHashV2 != mi.InfoHashV2 {
		t.Fatal("leecher's metadata is missing or does not match the real v2 infohash it was constructed for")
	}
	if got, want := leecher.Stats().Downloaded, totalBytes(files); got != want {
		t.Fatalf("Downloaded = %d, want %d", got, want)
	}
	readBackAndCompare(t, leecherCfg, mi, files)
}
