package metainfo

import (
	"crypto/sha1"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/bencode"
)

// buildInfo returns a bencoded info dictionary for a single-file torrent of
// the given geometry.
func buildInfo(t *testing.T, name string, pieceLength int64, numPieces int, total int64) []byte {
	t.Helper()
	info := struct {
		Length      int64  `bencode:"length"`
		Name        string `bencode:"name"`
		PieceLength int64  `bencode:"piece length"`
		Pieces      []byte `bencode:"pieces"`
	}{
		Length:      total,
		Name:        name,
		PieceLength: pieceLength,
		Pieces:      make([]byte, numPieces*HashSize),
	}
	raw, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	return raw
}

func wrapTorrent(t *testing.T, infoBytes []byte) []byte {
	t.Helper()
	raw, err := bencode.Marshal(struct {
		Announce string             `bencode:"announce"`
		Info     bencode.RawMessage `bencode:"info"`
	}{Announce: "http://tracker.test/announce", Info: infoBytes})
	if err != nil {
		t.Fatalf("marshal torrent: %v", err)
	}
	return raw
}

// TestInfoHashComesFromRawBytes is the guarantee the parser is built on. The
// infohash must be SHA-1 over the bytes that were in the file, not over a
// re-encoding of the parsed structure.
func TestInfoHashComesFromRawBytes(t *testing.T) {
	// Keys deliberately out of lexicographic order, and an unknown key in the
	// middle. A parser that re-encodes would normalise both away and compute a
	// different, wrong hash. The old decoder rejected this file outright.
	infoDict := "d" +
		"4:name8:test.bin" +
		"12:piece lengthi16384e" +
		"6:lengthi16384e" +
		"6:pieces20:" + strings.Repeat("\x00", 20) +
		"7:unknown4:junk" +
		"e"
	torrent := "d8:announce9:http://x.4:info" + infoDict + "e"

	mi, err := Parse([]byte(torrent))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := Hash(sha1.Sum([]byte(infoDict)))
	if mi.InfoHash != want {
		t.Fatalf("InfoHash = %s, want %s", mi.InfoHash, want)
	}
	if string(mi.InfoBytes) != infoDict {
		t.Fatalf("InfoBytes = %q, want %q", mi.InfoBytes, infoDict)
	}
	if mi.Info.Name != "test.bin" {
		t.Fatalf("Name = %q", mi.Info.Name)
	}
}

// TestInfoBytesServeMetadataExchange checks the other direction: the raw bytes
// we keep must be re-parseable on their own, which is what BEP 9 needs once a
// magnet link has fetched them from a peer.
func TestInfoBytesRoundTripThroughParseInfo(t *testing.T) {
	original, err := Parse(wrapTorrent(t, buildInfo(t, "round.bin", 16384, 3, 16384*3)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	fromInfoAlone, err := ParseInfo(original.InfoBytes)
	if err != nil {
		t.Fatalf("ParseInfo: %v", err)
	}
	if fromInfoAlone.InfoHash != original.InfoHash {
		t.Fatalf("infohash changed: %s vs %s", fromInfoAlone.InfoHash, original.InfoHash)
	}
	if fromInfoAlone.TotalLength != original.TotalLength {
		t.Fatalf("total length changed: %d vs %d", fromInfoAlone.TotalLength, original.TotalLength)
	}
	if fromInfoAlone.NumPieces() != original.NumPieces() {
		t.Fatalf("piece count changed: %d vs %d", fromInfoAlone.NumPieces(), original.NumPieces())
	}
}

func TestParseRejectsBadGeometry(t *testing.T) {
	tests := []struct {
		name      string
		infoBytes []byte
		wantErr   string
	}{
		{
			name:      "piece length below the floor",
			infoBytes: buildInfo(t, "a.bin", 1024, 1, 1024),
			wantErr:   "implausible piece length",
		},
		{
			name:      "piece length above the ceiling",
			infoBytes: buildInfo(t, "a.bin", 128<<20, 1, 128<<20),
			wantErr:   "implausible piece length",
		},
		{
			name:      "piece count disagrees with total length",
			infoBytes: buildInfo(t, "a.bin", 16384, 5, 16384*2),
			wantErr:   "describes 5 pieces",
		},
		{
			name:      "no pieces at all",
			infoBytes: buildInfo(t, "a.bin", 16384, 0, 16384),
			wantErr:   "no pieces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(wrapTorrent(t, tt.infoBytes))
			if err == nil {
				t.Fatalf("Parse accepted %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Parse error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseRejectsMalformedInfo(t *testing.T) {
	pieces := make([]byte, HashSize)

	tests := []struct {
		name string
		info any
	}{
		{
			name: "both length and files",
			info: struct {
				Files []struct {
					Length int64    `bencode:"length"`
					Path   []string `bencode:"path"`
				} `bencode:"files"`
				Length      int64  `bencode:"length"`
				Name        string `bencode:"name"`
				PieceLength int64  `bencode:"piece length"`
				Pieces      []byte `bencode:"pieces"`
			}{
				Files: []struct {
					Length int64    `bencode:"length"`
					Path   []string `bencode:"path"`
				}{{Length: 16384, Path: []string{"a"}}},
				Length: 16384, Name: "x", PieceLength: 16384, Pieces: pieces,
			},
		},
		{
			name: "neither length nor files",
			info: struct {
				Name        string `bencode:"name"`
				PieceLength int64  `bencode:"piece length"`
				Pieces      []byte `bencode:"pieces"`
			}{Name: "x", PieceLength: 16384, Pieces: pieces},
		},
		{
			name: "pieces not a multiple of 20",
			info: struct {
				Length      int64  `bencode:"length"`
				Name        string `bencode:"name"`
				PieceLength int64  `bencode:"piece length"`
				Pieces      []byte `bencode:"pieces"`
			}{Length: 16384, Name: "x", PieceLength: 16384, Pieces: make([]byte, 25)},
		},
		{
			name: "traversal in the torrent name",
			info: struct {
				Length      int64  `bencode:"length"`
				Name        string `bencode:"name"`
				PieceLength int64  `bencode:"piece length"`
				Pieces      []byte `bencode:"pieces"`
			}{Length: 16384, Name: "..", PieceLength: 16384, Pieces: pieces},
		},
		{
			name: "traversal in a file path",
			info: struct {
				Files []struct {
					Length int64    `bencode:"length"`
					Path   []string `bencode:"path"`
				} `bencode:"files"`
				Name        string `bencode:"name"`
				PieceLength int64  `bencode:"piece length"`
				Pieces      []byte `bencode:"pieces"`
			}{
				Files: []struct {
					Length int64    `bencode:"length"`
					Path   []string `bencode:"path"`
				}{{Length: 16384, Path: []string{"..", "..", "evil.dll"}}},
				Name: "x", PieceLength: 16384, Pieces: pieces,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := bencode.Marshal(tt.info)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if _, err := Parse(wrapTorrent(t, raw)); err == nil {
				t.Fatalf("Parse accepted %s", tt.name)
			}
		})
	}
}

func TestParseMissingInfo(t *testing.T) {
	if _, err := Parse([]byte("d8:announce4:httpe")); err == nil {
		t.Fatal("Parse accepted a torrent with no info dictionary")
	}
}

func TestPieceLen(t *testing.T) {
	// 3 full pieces plus a 100-byte remainder.
	const pieceLength = 16384
	total := int64(pieceLength*3 + 100)
	mi, err := Parse(wrapTorrent(t, buildInfo(t, "x.bin", pieceLength, 4, total)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	for i := 0; i < 3; i++ {
		if got := mi.PieceLen(i); got != pieceLength {
			t.Fatalf("PieceLen(%d) = %d, want %d", i, got, pieceLength)
		}
	}
	if got := mi.PieceLen(3); got != 100 {
		t.Fatalf("PieceLen(3) = %d, want 100", got)
	}
	for _, bad := range []int{-1, 4, 1000} {
		if got := mi.PieceLen(bad); got != 0 {
			t.Fatalf("PieceLen(%d) = %d, want 0", bad, got)
		}
	}

	// The sum of every piece length must equal the torrent length.
	var sum int64
	for i := 0; i < mi.NumPieces(); i++ {
		sum += mi.PieceLen(i)
	}
	if sum != total {
		t.Fatalf("piece lengths sum to %d, want %d", sum, total)
	}
}

func TestAnnounceURLs(t *testing.T) {
	mi := &MetaInfo{
		Announce: "http://primary/announce",
		AnnounceList: [][]string{
			{"http://tier1a/announce", "http://tier1b/announce"},
			{"udp://tier2/announce", "http://primary/announce"},
		},
	}
	got := mi.AnnounceURLs()
	want := []string{
		"http://tier1a/announce",
		"http://tier1b/announce",
		"udp://tier2/announce",
		"http://primary/announce",
	}
	if len(got) != len(want) {
		t.Fatalf("AnnounceURLs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AnnounceURLs()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMultiFileTotals(t *testing.T) {
	type fileWire struct {
		Length int64    `bencode:"length"`
		Path   []string `bencode:"path"`
	}
	raw, err := bencode.Marshal(struct {
		Files       []fileWire `bencode:"files"`
		Name        string     `bencode:"name"`
		PieceLength int64      `bencode:"piece length"`
		Pieces      []byte     `bencode:"pieces"`
	}{
		Files: []fileWire{
			{Length: 16384, Path: []string{"a.txt"}},
			{Length: 16384, Path: []string{"sub", "b.bin"}},
		},
		Name:        "Bundle",
		PieceLength: 16384,
		Pieces:      make([]byte, 2*HashSize),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	mi, err := Parse(wrapTorrent(t, raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !mi.Info.IsMultiFile() {
		t.Fatal("IsMultiFile() = false")
	}
	if mi.TotalLength != 32768 {
		t.Fatalf("TotalLength = %d, want 32768", mi.TotalLength)
	}
	if len(mi.Info.Files) != 2 || mi.Info.Files[1].Path[1] != "b.bin" {
		t.Fatalf("Files = %+v", mi.Info.Files)
	}
}

func TestLoadRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.torrent")
	if err := os.WriteFile(path, make([]byte, MaxTorrentFileSize+1), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted a file over the size cap")
	}
}

func TestHash(t *testing.T) {
	h, err := ParseHash("0102030405060708090a0b0c0d0e0f1011121314")
	if err != nil {
		t.Fatalf("ParseHash: %v", err)
	}
	if h[0] != 1 || h[19] != 0x14 {
		t.Fatalf("ParseHash gave %v", h)
	}
	if h.String() != "0102030405060708090a0b0c0d0e0f1011121314" {
		t.Fatalf("String() = %q", h.String())
	}
	if h.IsZero() {
		t.Fatal("IsZero() = true for a non-zero hash")
	}
	if !(Hash{}).IsZero() {
		t.Fatal("IsZero() = false for the zero hash")
	}

	for _, bad := range []string{"", "abc", strings.Repeat("z", 40), strings.Repeat("a", 39)} {
		if _, err := ParseHash(bad); err == nil {
			t.Fatalf("ParseHash(%q) returned no error", bad)
		}
	}
}
