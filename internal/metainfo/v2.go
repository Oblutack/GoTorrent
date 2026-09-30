package metainfo

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/merkle"
)

// V2FileInfo is one file from a BEP 52 "file tree", flattened into path
// order (a sorted-key walk of the tree, matching bencode's own canonical
// key ordering — the same order the file's pieces occupy in the flat
// piece address space).
type V2FileInfo struct {
	Path   []string
	Length int64
	// PiecesRoot is the file's own merkle root — zero for an empty
	// (Length == 0) file, which BEP 52 says has no "pieces root" field at
	// all.
	PiecesRoot Hash256
}

// fileTreeLeafWire is the {"": {length, pieces root}} shape a file-tree
// node takes once it describes a file rather than a directory.
type fileTreeLeafWire struct {
	Length     int64  `bencode:"length"`
	PiecesRoot []byte `bencode:"pieces root,omitempty"`
}

// v2PieceRun is one file's contiguous run of global piece indices — the
// resolver parseFileTree/finishV2 builds so PieceFile can answer "which
// file, what offset" for a flat wire-protocol piece index in O(log files)
// rather than re-walking the file tree on every call.
type v2PieceRun struct {
	fileIndex  int
	startPiece int
	numPieces  int
}

// parseFileTree decodes BEP 52's "file tree" field into V2FileInfo order,
// rejecting the one case the spec calls out explicitly: the tree's own
// root must not itself be a file.
func parseFileTree(raw bencode.RawMessage) ([]V2FileInfo, error) {
	var root map[string]bencode.RawMessage
	if err := bencode.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("metainfo: bad 'file tree': %w", err)
	}
	if _, isLeaf := root[""]; isLeaf && len(root) == 1 {
		return nil, errors.New("metainfo: 'file tree' root must not itself be a file")
	}

	var files []V2FileInfo
	if err := walkFileTree(root, nil, &files); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("metainfo: 'file tree' describes no files")
	}
	return files, nil
}

func walkFileTree(node map[string]bencode.RawMessage, prefix []string, out *[]V2FileInfo) error {
	if raw, ok := node[""]; ok && len(node) == 1 {
		var leaf fileTreeLeafWire
		if err := bencode.Unmarshal(raw, &leaf); err != nil {
			return fmt.Errorf("metainfo: bad file tree leaf at %v: %w", prefix, err)
		}
		if leaf.Length < 0 {
			return fmt.Errorf("metainfo: file %v has a negative length", prefix)
		}
		var root Hash256
		if leaf.Length > 0 {
			var err error
			root, err = Hash256From(leaf.PiecesRoot)
			if err != nil {
				return fmt.Errorf("metainfo: file %v: %w", prefix, err)
			}
		} else if len(leaf.PiecesRoot) != 0 {
			return fmt.Errorf("metainfo: file %v is empty but has a 'pieces root'", prefix)
		}
		path := append([]string(nil), prefix...)
		if err := ValidatePath(path); err != nil {
			return fmt.Errorf("metainfo: unsafe path in file tree: %w", err)
		}
		*out = append(*out, V2FileInfo{Path: path, Length: leaf.Length, PiecesRoot: root})
		return nil
	}

	// A directory: recurse into every child in sorted key order, matching
	// bencode's own canonical "sorted as raw strings" rule — Go's string
	// comparison is already exactly that (byte-wise lexicographic), and
	// map iteration order must be made deterministic here since Go's own
	// map iteration is randomized.
	keys := make([]string, 0, len(node))
	for k := range node {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		var child map[string]bencode.RawMessage
		if err := bencode.Unmarshal(node[k], &child); err != nil {
			return fmt.Errorf("metainfo: bad file tree entry %q: %w", k, err)
		}
		// A fresh slice per child, never reusing prefix's backing array -
		// sibling recursive calls must not alias each other's path.
		childPrefix := make([]string, len(prefix)+1)
		copy(childPrefix, prefix)
		childPrefix[len(prefix)] = k
		if err := walkFileTree(child, childPrefix, out); err != nil {
			return err
		}
	}
	return nil
}

// parsePieceLayers decodes BEP 52's top-level "piece layers" dict.
func parsePieceLayers(wire map[string][]byte) (map[Hash256][]byte, error) {
	if len(wire) == 0 {
		return nil, nil
	}
	layers := make(map[Hash256][]byte, len(wire))
	for k, v := range wire {
		h, err := Hash256From([]byte(k))
		if err != nil {
			return nil, fmt.Errorf("metainfo: 'piece layers' key: %w", err)
		}
		layers[h] = v
	}
	return layers, nil
}

// buildV2PieceRuns assigns each file a contiguous run of global piece
// indices, in file-tree order, aligned to piece boundaries per BEP 52 -
// "each non-empty file is aligned to a piece boundary." An empty file
// occupies zero pieces and is skipped entirely.
func buildV2PieceRuns(files []V2FileInfo, pieceLength int64) []v2PieceRun {
	var runs []v2PieceRun
	next := 0
	for i, f := range files {
		if f.Length == 0 {
			continue
		}
		n := int((f.Length + pieceLength - 1) / pieceLength)
		runs = append(runs, v2PieceRun{fileIndex: i, startPiece: next, numPieces: n})
		next += n
	}
	return runs
}

// v2TotalPieces returns the total flat piece count the resolver covers.
func v2TotalPieces(runs []v2PieceRun) int {
	if len(runs) == 0 {
		return 0
	}
	last := runs[len(runs)-1]
	return last.startPiece + last.numPieces
}

// setV2 parses BEP 52's fields (called once MetaVersion == 2 is already
// confirmed) and validates whatever can be checked without any real file
// data on disk: piece length is a power of two, the file tree parses and
// contains at least one file, and every 'piece layers' entry that IS
// present reconstructs its claimed 'pieces root' exactly.
//
// A large file's 'piece layers' entry being *absent* is deliberately not
// a parse error here, even though BEP 52 calls a torrent invalid without
// one — ParseInfo (the BEP 9 magnet path) never has one to give, by
// construction (see ParseInfo's own doc comment), and this MetaInfo must
// still parse successfully so the hash-request/hashes wire exchange
// (internal/torrent) has something to reconstruct it into. Verifying that
// every piece is actually covered by real, checked hashes before it's
// downloaded is internal/storage's job, at download time, not this one's.
func (mi *MetaInfo) setV2(wire *infoDictWire, pieceLayersWire map[string][]byte) error {
	if mi.Info.PieceLength&(mi.Info.PieceLength-1) != 0 {
		return fmt.Errorf("metainfo: v2 'piece length' %d is not a power of two", mi.Info.PieceLength)
	}
	if len(wire.FileTree) == 0 {
		return errors.New("metainfo: meta version 2 requires 'file tree'")
	}

	files, err := parseFileTree(wire.FileTree)
	if err != nil {
		return err
	}
	mi.V2Files = files
	mi.InfoHashV2 = Hash256(sha256.Sum256(mi.InfoBytes))

	layers, err := parsePieceLayers(pieceLayersWire)
	if err != nil {
		return err
	}
	mi.PieceLayers = layers

	mi.v2Pieces = buildV2PieceRuns(files, mi.Info.PieceLength)
	if v2TotalPieces(mi.v2Pieces) == 0 {
		return errors.New("metainfo: v2 torrent has no pieces (every file is empty)")
	}

	blocksPerPiece := int(mi.Info.PieceLength / merkle.BlockSize)
	for _, run := range mi.v2Pieces {
		f := files[run.fileIndex]
		layerBytes, ok := layers[f.PiecesRoot]
		if !ok {
			continue // not yet known - see the doc comment above
		}
		if len(layerBytes)%Hash256Size != 0 {
			return fmt.Errorf("metainfo: 'piece layers' entry for file %v is %d bytes, not a multiple of %d",
				f.Path, len(layerBytes), Hash256Size)
		}
		got := len(layerBytes) / Hash256Size
		if got != run.numPieces {
			return fmt.Errorf("metainfo: 'piece layers' entry for file %v has %d hashes, file needs %d",
				f.Path, got, run.numPieces)
		}
		pieceRoots := make([]merkle.Hash, run.numPieces)
		for j := range pieceRoots {
			copy(pieceRoots[j][:], layerBytes[j*Hash256Size:(j+1)*Hash256Size])
		}
		if got := merkle.FileRoot(pieceRoots, blocksPerPiece); got != merkle.Hash(f.PiecesRoot) {
			return fmt.Errorf("metainfo: 'piece layers' entry for file %v does not reconstruct its 'pieces root'", f.Path)
		}
	}
	return nil
}

// validateHybridConsistency checks structural agreement between a hybrid
// torrent's v1 and v2 descriptions — piece count, and real content length
// with v1's own BEP 47 padding files excluded. It does not (and cannot,
// with no file data on disk yet) prove every byte agrees; that is what
// internal/storage's per-piece hybrid double-verification proves once
// real data exists.
func (mi *MetaInfo) validateHybridConsistency() error {
	v1Pieces := len(mi.PieceHashes)
	v2Pieces := v2TotalPieces(mi.v2Pieces)
	if v1Pieces != v2Pieces {
		return fmt.Errorf("metainfo: hybrid torrent's v1 piece count (%d) disagrees with its v2 file tree (%d pieces)",
			v1Pieces, v2Pieces)
	}

	var v1Real int64
	if mi.Info.IsMultiFile() {
		for _, f := range mi.Info.Files {
			if !f.IsPadding() {
				v1Real += f.Length
			}
		}
	} else {
		v1Real = mi.Info.Length
	}
	var v2Total int64
	for _, f := range mi.V2Files {
		v2Total += f.Length
	}
	if v1Real != v2Total {
		return fmt.Errorf("metainfo: hybrid torrent's v1 real content length (%d, excluding padding files) disagrees with its v2 file tree (%d)",
			v1Real, v2Total)
	}
	return nil
}

// PieceFile resolves a flat global piece index to which V2FileInfo entry
// it falls in and the byte offset of that piece's first byte within that
// file. Only meaningful when MetaVersion == 2; the caller is expected to
// have already checked that (the same precondition NumPieces/PieceLen
// already rely on for their own v2 branch).
func (mi *MetaInfo) PieceFile(index int) (fileIndex int, offsetInFile int64, ok bool) {
	runs := mi.v2Pieces
	i := sort.Search(len(runs), func(i int) bool {
		return runs[i].startPiece+runs[i].numPieces > index
	})
	if i == len(runs) || index < runs[i].startPiece {
		return 0, 0, false
	}
	run := runs[i]
	return run.fileIndex, int64(index-run.startPiece) * mi.Info.PieceLength, true
}
