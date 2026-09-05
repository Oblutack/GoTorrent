// Package metainfo parses .torrent files.
//
// The infohash is computed over the *raw* bytes of the info dictionary as they
// appeared in the file, never over a re-encoding of the parsed form. Those raw
// bytes are kept on the parsed value because BEP 9 metadata exchange has to
// serve them to peers verbatim.
package metainfo

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Oblutack/GoTorrent/internal/bencode"
)

const (
	// MinPieceLength / MaxPieceLength bound plausible torrent geometry. 16 KiB
	// is the block size, so nothing smaller makes sense; 64 MiB is far above
	// what any real client produces. Rejecting outside this range stops a
	// malformed or hostile torrent from driving absurd allocations downstream.
	MinPieceLength = 16 * 1024
	MaxPieceLength = 64 * 1024 * 1024

	// MaxPieces bounds the piece count. At 16 KiB pieces this still allows a
	// 64 GiB torrent, and it caps the size of every per-piece structure.
	MaxPieces = 4 << 20

	// MaxTorrentFileSize caps what Load will read off disk.
	MaxTorrentFileSize = 32 << 20
)

// ErrNoMetadata is returned by operations that need an info dictionary on a
// torrent that does not have one yet (a magnet link before BEP 9 completes).
var ErrNoMetadata = errors.New("metainfo: torrent has no metadata")

// MetaInfo is a parsed .torrent file.
type MetaInfo struct {
	Announce     string
	AnnounceList [][]string
	Comment      string
	CreatedBy    string
	CreationDate int64

	Info InfoDict

	// InfoBytes is the info dictionary exactly as it appeared in the file.
	// InfoHash is its SHA-1. Keeping the bytes means the hash never depends on
	// our encoder, and lets us serve the metadata to peers over BEP 9.
	InfoBytes []byte
	InfoHash  Hash

	PieceHashes []Hash
	TotalLength int64
}

// InfoDict is the info dictionary of a v1 torrent.
type InfoDict struct {
	Name        string
	PieceLength int64
	Private     bool

	// Length is set for single-file torrents, Files for multi-file ones.
	// Exactly one of them is populated.
	Length int64
	Files  []FileInfo
}

// FileInfo is one file in a multi-file torrent.
type FileInfo struct {
	Length int64
	Path   []string
	Md5sum string
}

// IsMultiFile reports whether the torrent describes a directory of files.
func (d *InfoDict) IsMultiFile() bool { return len(d.Files) > 0 }

// NumPieces returns the number of pieces in the torrent.
func (mi *MetaInfo) NumPieces() int { return len(mi.PieceHashes) }

// PieceLen returns the length of a specific piece, accounting for the short
// final one. Out-of-range indexes return 0.
func (mi *MetaInfo) PieceLen(index int) int64 {
	n := len(mi.PieceHashes)
	if index < 0 || index >= n {
		return 0
	}
	if index == n-1 {
		return mi.TotalLength - int64(n-1)*mi.Info.PieceLength
	}
	return mi.Info.PieceLength
}

// AnnounceURLs flattens announce and announce-list into one deduplicated list,
// preserving tier order. BEP 12 says announce-list supersedes announce, but
// clients are expected to fall back, so both are included.
func (mi *MetaInfo) AnnounceURLs() []string {
	var out []string
	seen := make(map[string]bool)
	add := func(u string) {
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	for _, tier := range mi.AnnounceList {
		for _, u := range tier {
			add(u)
		}
	}
	add(mi.Announce)
	return out
}

// --- wire format ----------------------------------------------------------

// torrentFile mirrors the top level of a .torrent.
type torrentFile struct {
	Announce     string             `bencode:"announce,omitempty"`
	AnnounceList [][]string         `bencode:"announce-list,omitempty"`
	Comment      string             `bencode:"comment,omitempty"`
	CreatedBy    string             `bencode:"created by,omitempty"`
	CreationDate int64              `bencode:"creation date,omitempty"`
	Encoding     string             `bencode:"encoding,omitempty"`
	Info         bencode.RawMessage `bencode:"info"`
}

// infoDictWire mirrors the info dictionary.
type infoDictWire struct {
	Name        string         `bencode:"name"`
	PieceLength int64          `bencode:"piece length"`
	Pieces      []byte         `bencode:"pieces"`
	Private     int64          `bencode:"private,omitempty"`
	Length      int64          `bencode:"length,omitempty"`
	Files       []fileDictWire `bencode:"files,omitempty"`
}

type fileDictWire struct {
	Length int64    `bencode:"length"`
	Path   []string `bencode:"path"`
	Md5sum string   `bencode:"md5sum,omitempty"`
}

// --- parsing --------------------------------------------------------------

// Load reads and parses a .torrent file.
func Load(path string) (*MetaInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("metainfo: could not open %s: %w", path, err)
	}
	defer f.Close()
	return Read(f)
}

// Read parses a .torrent from a stream, refusing anything implausibly large.
func Read(r io.Reader) (*MetaInfo, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxTorrentFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("metainfo: read failed: %w", err)
	}
	if len(data) > MaxTorrentFileSize {
		return nil, fmt.Errorf("metainfo: torrent file is larger than %d bytes", MaxTorrentFileSize)
	}
	return Parse(data)
}

// Parse decodes a .torrent from its bytes.
func Parse(data []byte) (*MetaInfo, error) {
	var tf torrentFile
	if err := bencode.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("metainfo: %w", err)
	}
	if len(tf.Info) == 0 {
		return nil, errors.New("metainfo: 'info' dictionary is missing")
	}

	mi := &MetaInfo{
		Announce:     tf.Announce,
		AnnounceList: tf.AnnounceList,
		Comment:      tf.Comment,
		CreatedBy:    tf.CreatedBy,
		CreationDate: tf.CreationDate,
	}
	if err := mi.setInfo(tf.Info); err != nil {
		return nil, err
	}
	return mi, nil
}

// ParseInfo builds a MetaInfo from an info dictionary alone. This is the entry
// point for BEP 9: a magnet link gives us an infohash, peers give us these
// bytes, and the caller must have already checked that they hash to the
// expected infohash.
func ParseInfo(infoBytes []byte) (*MetaInfo, error) {
	mi := &MetaInfo{}
	if err := mi.setInfo(infoBytes); err != nil {
		return nil, err
	}
	return mi, nil
}

func (mi *MetaInfo) setInfo(infoBytes []byte) error {
	var wire infoDictWire
	if err := bencode.Unmarshal(infoBytes, &wire); err != nil {
		return fmt.Errorf("metainfo: bad 'info' dictionary: %w", err)
	}

	mi.InfoBytes = append([]byte(nil), infoBytes...)
	mi.InfoHash = sha1.Sum(mi.InfoBytes)

	if err := validateName(wire.Name); err != nil {
		return err
	}
	mi.Info.Name = wire.Name
	mi.Info.Private = wire.Private != 0

	if wire.PieceLength < MinPieceLength || wire.PieceLength > MaxPieceLength {
		return fmt.Errorf("metainfo: implausible piece length %d, expected %d..%d",
			wire.PieceLength, MinPieceLength, MaxPieceLength)
	}
	mi.Info.PieceLength = wire.PieceLength

	if len(wire.Pieces)%HashSize != 0 {
		return fmt.Errorf("metainfo: 'pieces' is %d bytes, not a multiple of %d", len(wire.Pieces), HashSize)
	}
	numPieces := len(wire.Pieces) / HashSize
	if numPieces == 0 {
		return errors.New("metainfo: torrent has no pieces")
	}
	if numPieces > MaxPieces {
		return fmt.Errorf("metainfo: torrent claims %d pieces, limit is %d", numPieces, MaxPieces)
	}

	if err := mi.setFiles(&wire); err != nil {
		return err
	}

	// The piece count has to agree with the total length, or the geometry is
	// inconsistent and every offset calculation downstream is wrong.
	expected := (mi.TotalLength + mi.Info.PieceLength - 1) / mi.Info.PieceLength
	if int64(numPieces) != expected {
		return fmt.Errorf("metainfo: 'pieces' describes %d pieces but %d bytes over %d-byte pieces needs %d",
			numPieces, mi.TotalLength, mi.Info.PieceLength, expected)
	}

	mi.PieceHashes = make([]Hash, numPieces)
	for i := range mi.PieceHashes {
		copy(mi.PieceHashes[i][:], wire.Pieces[i*HashSize:(i+1)*HashSize])
	}
	return nil
}

func (mi *MetaInfo) setFiles(wire *infoDictWire) error {
	switch {
	case len(wire.Files) > 0:
		if wire.Length != 0 {
			return errors.New("metainfo: 'info' has both 'length' and 'files'")
		}
		mi.Info.Files = make([]FileInfo, len(wire.Files))
		var total int64
		for i, f := range wire.Files {
			if f.Length < 0 {
				return fmt.Errorf("metainfo: file %d has a negative length", i)
			}
			// Reject traversals, separators, device names and the rest before
			// this path is ever joined onto the download directory.
			if err := ValidatePath(f.Path); err != nil {
				return fmt.Errorf("metainfo: unsafe path in file %d: %w", i, err)
			}
			mi.Info.Files[i] = FileInfo{Length: f.Length, Path: f.Path, Md5sum: f.Md5sum}
			total += f.Length
			if total < 0 {
				return errors.New("metainfo: total length overflows")
			}
		}
		if total == 0 {
			return errors.New("metainfo: multi-file torrent is empty")
		}
		mi.TotalLength = total
		return nil

	case wire.Length > 0:
		mi.Info.Length = wire.Length
		mi.TotalLength = wire.Length
		return nil

	default:
		return errors.New("metainfo: 'info' must contain a positive 'length' or a non-empty 'files'")
	}
}

// validateName checks the torrent name, which becomes a file or directory name
// on disk and so is subject to the same rules as any other path segment.
func validateName(name string) error {
	if err := ValidatePathSegment(name); err != nil {
		return fmt.Errorf("metainfo: unsafe 'name': %w", err)
	}
	return nil
}
