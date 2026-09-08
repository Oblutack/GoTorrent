package metainfo

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
)

// CreateFile describes one file to include when building a torrent (see
// CreateOptions). Path is the file's path inside the torrent (BEP 3's file
// "path" list) — nil for a single-file torrent, where Name alone names the
// file. SourcePath is where Build actually reads the bytes from.
type CreateFile struct {
	Path       []string
	SourcePath string
	Length     int64
}

// CreateOptions configures Build.
type CreateOptions struct {
	// Name is the torrent's name: the file name for a single-file torrent,
	// the directory name for a multi-file one. Required.
	Name string
	// PieceLength is the piece size in bytes. 0 selects one automatically —
	// see ChoosePieceLength.
	PieceLength int64
	Private     bool
	Comment     string
	CreatedBy   string
	// Announce and AnnounceList follow the same BEP 12 shape Parse produces
	// — Announce alone for a single tracker, AnnounceList for real tiers.
	Announce     string
	AnnounceList [][]string
	// UrlList is BEP 19 web seeds — see MetaInfo.UrlList's doc comment.
	UrlList []string
	// Files is every file to include, in the order they'll appear in the
	// torrent. A single-file torrent has exactly one entry with a nil Path.
	// Required, and at least one file must have a positive Length.
	Files []CreateFile
}

// ChoosePieceLength picks a piece length for a torrent of the given total
// size when the caller has no preference of their own — the same rough
// ladder every mainstream client uses, aiming for somewhere in the
// low-thousands of pieces regardless of torrent size, clamped to
// [MinPieceLength, MaxPieceLength].
func ChoosePieceLength(totalLength int64) int64 {
	const (
		mib = 1 << 20
		gib = 1 << 30
	)
	switch {
	case totalLength <= 50*mib:
		return 256 * 1024
	case totalLength <= 500*mib:
		return 512 * 1024
	case totalLength <= 2*gib:
		return 1 * mib
	case totalLength <= 8*gib:
		return 2 * mib
	case totalLength <= 16*gib:
		return 4 * mib
	default:
		return 8 * mib
	}
}

// Build reads every file in opts.Files off disk, hashes each piece, and
// returns the finished .torrent's raw bytes plus the parsed MetaInfo view
// of them — Parse is called on Build's own output before returning, so a
// caller gets exactly the same validation and structure Load would give a
// file written to disk, without a redundant round trip through one.
func Build(opts CreateOptions) (raw []byte, mi *MetaInfo, err error) {
	if err := validateName(opts.Name); err != nil {
		return nil, nil, err
	}
	if len(opts.Files) == 0 {
		return nil, nil, errors.New("metainfo: at least one file is required")
	}

	var total int64
	for _, f := range opts.Files {
		if f.Length < 0 {
			return nil, nil, fmt.Errorf("metainfo: file %q has a negative length", f.SourcePath)
		}
		total += f.Length
	}
	if total <= 0 {
		return nil, nil, errors.New("metainfo: total content length is zero")
	}

	pieceLength := opts.PieceLength
	if pieceLength == 0 {
		pieceLength = ChoosePieceLength(total)
	}
	if pieceLength < MinPieceLength || pieceLength > MaxPieceLength {
		return nil, nil, fmt.Errorf("metainfo: piece length %d out of range %d..%d", pieceLength, MinPieceLength, MaxPieceLength)
	}

	pieces, err := hashPieces(opts.Files, pieceLength)
	if err != nil {
		return nil, nil, err
	}

	var infoWire infoDictWire
	infoWire.Name = opts.Name
	infoWire.PieceLength = pieceLength
	infoWire.Pieces = pieces
	if opts.Private {
		infoWire.Private = 1
	}

	single := len(opts.Files) == 1 && len(opts.Files[0].Path) == 0
	if single {
		infoWire.Length = opts.Files[0].Length
	} else {
		infoWire.Files = make([]fileDictWire, len(opts.Files))
		for i, f := range opts.Files {
			if len(f.Path) == 0 {
				return nil, nil, fmt.Errorf("metainfo: file %d (%s) has no in-torrent path", i, f.SourcePath)
			}
			if err := ValidatePath(f.Path); err != nil {
				return nil, nil, fmt.Errorf("metainfo: file %d: %w", i, err)
			}
			infoWire.Files[i] = fileDictWire{Length: f.Length, Path: f.Path}
		}
	}

	infoBytes, err := bencode.Marshal(infoWire)
	if err != nil {
		return nil, nil, fmt.Errorf("metainfo: encoding info dict: %w", err)
	}

	tf := torrentFile{
		Announce:     opts.Announce,
		AnnounceList: opts.AnnounceList,
		Comment:      opts.Comment,
		CreatedBy:    opts.CreatedBy,
		CreationDate: time.Now().Unix(),
		UrlList:      opts.UrlList,
		Info:         infoBytes,
	}
	raw, err = bencode.Marshal(tf)
	if err != nil {
		return nil, nil, fmt.Errorf("metainfo: encoding torrent: %w", err)
	}

	mi, err = Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("metainfo: built torrent failed to parse back: %w", err)
	}
	return raw, mi, nil
}

// hashPieces reads every file in order and returns the concatenated SHA-1
// of each pieceLength-sized chunk of the whole concatenated byte stream —
// BEP 3's definition of a piece, which does not respect file boundaries.
func hashPieces(files []CreateFile, pieceLength int64) ([]byte, error) {
	var pieces []byte
	h := sha1.New()
	var filled int64

	flush := func() {
		pieces = append(pieces, h.Sum(nil)...)
		h.Reset()
		filled = 0
	}

	for _, f := range files {
		if f.Length == 0 {
			continue
		}
		src, err := os.Open(f.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("metainfo: opening %s: %w", f.SourcePath, err)
		}
		remaining := f.Length
		for remaining > 0 {
			want := pieceLength - filled
			if want > remaining {
				want = remaining
			}
			n, err := io.CopyN(h, src, want)
			filled += n
			remaining -= n
			if err != nil && !errors.Is(err, io.EOF) {
				src.Close()
				return nil, fmt.Errorf("metainfo: reading %s: %w", f.SourcePath, err)
			}
			if filled == pieceLength {
				flush()
			}
			if n == 0 {
				src.Close()
				return nil, fmt.Errorf("metainfo: %s is shorter than its declared length (%d bytes short)", f.SourcePath, remaining)
			}
		}
		src.Close()
	}
	if filled > 0 {
		flush()
	}
	return pieces, nil
}
