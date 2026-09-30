package metainfo

import (
	"crypto/sha1"
	"fmt"
	"hash"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/merkle"
)

// pendingV1Hash is a v1 SHA-1 piece hasher left open because the file that
// fed it ended mid-piece — resolved either by a following real file's own
// BEP 47 padding file (multi-file hybrid) or finalized as-is once nothing
// more will ever feed it (the last file, or a single-file torrent, which
// never pads at all).
type pendingV1Hash struct {
	hasher  hash.Hash
	residue int64
}

// hashOneFileV2 hashes one file's content into its v2 merkle data (per-
// piece roots, their combined file root, and the concatenated piece-layer
// bytes) and, when v1 is true, its v1 SHA-1 piece hashes for every piece
// this file's own content fully closes. The file always starts a fresh
// piece at piece boundary zero — by construction, whatever the caller did
// with a previous file's own pendingV1Hash already closed that piece
// exactly, so there is never a partial piece carried *into* a file, only
// ever *out of* one.
func hashOneFileV2(f CreateFile, pieceLength int64, blocksPerPiece int, v1 bool) (
	root merkle.Hash, pieceLayerBytes []byte, numPieces int, v1Pieces []byte, pending *pendingV1Hash, err error,
) {
	src, err := os.Open(f.SourcePath)
	if err != nil {
		return merkle.Hash{}, nil, 0, nil, nil, fmt.Errorf("metainfo: opening %s: %w", f.SourcePath, err)
	}
	defer src.Close()

	var pieceRoots []merkle.Hash
	remaining := f.Length
	var v1Hasher hash.Hash
	if v1 {
		v1Hasher = sha1.New()
	}
	var leaves []merkle.Hash
	var pieceFilled int64

	for remaining > 0 {
		blockLen := int64(merkle.BlockSize)
		if blockLen > remaining {
			blockLen = remaining
		}
		buf := make([]byte, blockLen)
		n, rerr := io.ReadFull(src, buf)
		if rerr != nil {
			return merkle.Hash{}, nil, 0, nil, nil, fmt.Errorf("metainfo: reading %s: %w", f.SourcePath, rerr)
		}
		leaves = append(leaves, merkle.Leaf(buf[:n]))
		if v1 {
			v1Hasher.Write(buf[:n])
		}
		pieceFilled += int64(n)
		remaining -= int64(n)

		if pieceFilled != pieceLength && remaining != 0 {
			continue // more blocks needed before this piece is done
		}

		soleFilePiece := len(pieceRoots) == 0 && f.Length <= pieceLength
		pieceRoots = append(pieceRoots, merkle.PieceRoot(leaves, blocksPerPiece, soleFilePiece))
		leaves = nil

		if v1 {
			if pieceFilled == pieceLength {
				v1Pieces = append(v1Pieces, v1Hasher.Sum(nil)...)
				v1Hasher = sha1.New()
			} else {
				// The file ended mid-piece - leave this one open for the
				// caller to resolve via padding or finalize as-is.
				pending = &pendingV1Hash{hasher: v1Hasher, residue: pieceLength - pieceFilled}
			}
		}
		pieceFilled = 0
	}

	fileRoot := merkle.FileRoot(pieceRoots, blocksPerPiece)
	numPieces = len(pieceRoots)
	for _, pr := range pieceRoots {
		pieceLayerBytes = append(pieceLayerBytes, pr[:]...)
	}
	return fileRoot, pieceLayerBytes, numPieces, v1Pieces, pending, nil
}

// setFileTreeEntry inserts one file into the file-tree map being built,
// creating intermediate directory maps as needed — the write-side mirror
// of parseFileTree's own read-side walk.
func setFileTreeEntry(tree map[string]any, path []string, length int64, root Hash256) {
	node := tree
	for _, seg := range path[:len(path)-1] {
		child, ok := node[seg].(map[string]any)
		if !ok {
			child = map[string]any{}
			node[seg] = child
		}
		node = child
	}
	leaf := map[string]any{"length": length}
	if length > 0 {
		leaf["pieces root"] = string(root[:])
	}
	node[path[len(path)-1]] = map[string]any{"": leaf}
}

// buildV2 is Build's v2/hybrid path, called once opts.MetaVersion == 2 is
// confirmed and pieceLength has already been chosen/validated. It mirrors
// BEP 52's own reference implementation (bep_0052_torrent_creator.py)
// closely enough to share internal/merkle's own reference-cross-checked
// fixtures conceptually — see that package's doc comment for the exact
// padding-rule detail this depends on getting right, and the "one info
// dict, two hash functions" info-hash model this relies on (Parse, called
// at the end, is what actually computes InfoHash/InfoHashV2 from the
// finished bytes — this function only ever builds the wire structures).
func buildV2(opts CreateOptions, pieceLength int64) (raw []byte, mi *MetaInfo, err error) {
	if pieceLength&(pieceLength-1) != 0 {
		return nil, nil, fmt.Errorf("metainfo: v2 piece length %d is not a power of two", pieceLength)
	}
	single := len(opts.Files) == 1 && len(opts.Files[0].Path) == 0
	multiFile := !single
	blocksPerPiece := int(pieceLength / merkle.BlockSize)

	fileTree := map[string]any{}
	pieceLayers := map[string][]byte{}
	var v1Pieces []byte
	var v1Files []fileDictWire
	var v1SingleLength int64

	var pending *pendingV1Hash

	for i, f := range opts.Files {
		if f.Length < 0 {
			return nil, nil, fmt.Errorf("metainfo: file %q has a negative length", f.SourcePath)
		}
		path := f.Path
		if single {
			path = []string{opts.Name}
		} else if len(path) == 0 {
			return nil, nil, fmt.Errorf("metainfo: file %d (%s) has no in-torrent path", i, f.SourcePath)
		}

		// Resolve whatever the previous file left open, before touching
		// this file's own bytes at all - a BEP 47 padding file of exactly
		// the residue, its zero bytes finishing the previous file's own
		// last piece.
		if opts.Hybrid && multiFile && pending != nil {
			padLen := pending.residue
			pending.hasher.Write(make([]byte, padLen))
			v1Pieces = append(v1Pieces, pending.hasher.Sum(nil)...)
			v1Files = append(v1Files, fileDictWire{
				Length: padLen,
				Path:   []string{".pad", strconv.FormatInt(padLen, 10)},
				Attr:   "p",
			})
			pending = nil
		}

		if f.Length == 0 {
			setFileTreeEntry(fileTree, path, 0, Hash256{})
			if opts.Hybrid && multiFile {
				v1Files = append(v1Files, fileDictWire{Length: 0, Path: f.Path})
			}
			continue
		}

		root, layerBytes, numPieces, filePieces, filePending, herr := hashOneFileV2(f, pieceLength, blocksPerPiece, opts.Hybrid)
		if herr != nil {
			return nil, nil, herr
		}
		setFileTreeEntry(fileTree, path, f.Length, Hash256(root))
		if numPieces > 1 {
			pieceLayers[string(root[:])] = layerBytes
		}

		if opts.Hybrid {
			v1Pieces = append(v1Pieces, filePieces...)
			if filePending != nil {
				if multiFile && i < len(opts.Files)-1 {
					pending = filePending
				} else {
					// The last file, or a single-file torrent (which never
					// pads): finalize with whatever bytes are already
					// there, no padding to add.
					v1Pieces = append(v1Pieces, filePending.hasher.Sum(nil)...)
				}
			}
			if single {
				v1SingleLength = f.Length
			} else {
				v1Files = append(v1Files, fileDictWire{Length: f.Length, Path: f.Path})
			}
		}
	}

	var infoWire infoDictWire
	infoWire.Name = opts.Name
	infoWire.PieceLength = pieceLength
	infoWire.MetaVersion = 2
	if opts.Private {
		infoWire.Private = 1
	}
	fileTreeBytes, err := bencode.Marshal(fileTree)
	if err != nil {
		return nil, nil, fmt.Errorf("metainfo: encoding file tree: %w", err)
	}
	infoWire.FileTree = fileTreeBytes

	if opts.Hybrid {
		infoWire.Pieces = v1Pieces
		if single {
			infoWire.Length = v1SingleLength
		} else {
			infoWire.Files = v1Files
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
		PieceLayers:  pieceLayers,
	}
	raw, err = bencode.Marshal(tf)
	if err != nil {
		return nil, nil, fmt.Errorf("metainfo: encoding torrent: %w", err)
	}

	mi, err = Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("metainfo: built v2/hybrid torrent failed to parse back: %w", err)
	}
	return raw, mi, nil
}
