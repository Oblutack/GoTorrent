package storage

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"

	"github.com/Oblutack/GoTorrent/internal/merkle"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// VerifyResult summarises a verification pass.
type VerifyResult struct {
	Complete int // pieces whose SHA-1 matched
	Total    int
}

// VerifyOptions configures a verification pass.
type VerifyOptions struct {
	// Workers is how many pieces are hashed in parallel. Zero uses one worker
	// per CPU, capped at 8: past that the disk is the bottleneck, not the CPU.
	Workers int

	// OnPiece is called once per piece as it is checked, from multiple
	// goroutines, so it must be safe for concurrent use. It may be nil.
	OnPiece func(index int, ok bool)

	// OnProgress is called with the number of pieces checked so far. It may be
	// nil, and is also called concurrently.
	OnProgress func(done, total int)
}

// Verify hashes every piece on disk against the metainfo.
//
// This is the CheckingFiles state: it runs when resume data is missing or
// stale, and on an explicit force-recheck. Pieces that are missing or short
// simply come back false rather than failing the whole pass, because a
// partially downloaded torrent is the normal case here.
func (s *Storage) Verify(ctx context.Context, mi *metainfo.MetaInfo, opts VerifyOptions) (VerifyResult, error) {
	if mi == nil {
		return VerifyResult{}, metainfo.ErrNoMetadata
	}
	total := mi.NumPieces()
	result := VerifyResult{Total: total}
	if total == 0 {
		return result, nil
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = min(runtime.NumCPU(), 8)
	}
	if workers > total {
		workers = total
	}

	var (
		mu       sync.Mutex
		complete int
		done     int
		failure  error
	)

	indexes := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)

	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			buf := make([]byte, mi.Info.PieceLength)

			for index := range indexes {
				ok, err := s.verifyPiece(mi, index, buf)

				mu.Lock()
				if err != nil && failure == nil {
					failure = err
				}
				if ok {
					complete++
				}
				done++
				progress := done
				mu.Unlock()

				if opts.OnPiece != nil {
					opts.OnPiece(index, ok)
				}
				if opts.OnProgress != nil {
					opts.OnProgress(progress, total)
				}
			}
		}()
	}

	var feedErr error
feed:
	for i := 0; i < total; i++ {
		select {
		case indexes <- i:
		case <-ctx.Done():
			feedErr = ctx.Err()
			break feed
		}
	}
	close(indexes)
	wg.Wait()

	result.Complete = complete
	if feedErr != nil {
		return result, feedErr
	}
	return result, failure
}

// VerifyOne hashes a single piece against the metainfo and reports whether
// it matches. It is what the torrent actor calls right after a piece
// receives its last block, rather than re-running the whole-torrent Verify.
func (s *Storage) VerifyOne(ctx context.Context, mi *metainfo.MetaInfo, index int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if mi == nil {
		return false, metainfo.ErrNoMetadata
	}
	buf := make([]byte, mi.PieceLen(index))
	return s.verifyPiece(mi, index, buf)
}

// verifyPiece reads one piece and checks it against the metainfo. A piece
// that is missing or truncated on disk reports false with no error: that
// is what an incomplete download looks like.
//
// v1/hybrid pieces are checked against their flat-stream SHA-1
// (mi.PieceHashes). A pure-v2 piece is checked via internal/merkle
// instead (verifyPieceV2), since it has no entry there at all. A hybrid
// piece is checked BOTH ways, per BEP 52's own explicit requirement
// ("during the download they must also verify that pieces match both
// piece hash formats") — the two descriptions disagreeing is a real
// content-integrity problem, treated as a hard failure (this feature's
// documented "abort" choice among the two the spec allows), never
// silently marked have.
func (s *Storage) verifyPiece(mi *metainfo.MetaInfo, index int, buf []byte) (bool, error) {
	length := mi.PieceLen(index)
	if length <= 0 {
		return false, nil
	}
	if int64(len(buf)) < length {
		buf = make([]byte, length)
	}
	p := buf[:length]

	offset, err := s.pieceOffset(mi, index)
	if err != nil {
		return false, nil
	}
	if _, err := s.ReadAt(p, offset); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		// A missing file is expected before allocation; anything else is real.
		if isNotExist(err) {
			return false, nil
		}
		return false, err
	}

	if mi.IsPureV2() {
		return s.verifyPieceV2(mi, index, p)
	}
	v1OK := metainfo.Hash(sha1.Sum(p)) == mi.PieceHashes[index]
	if mi.MetaVersion != 2 {
		return v1OK, nil
	}
	v2OK, err := s.verifyPieceV2(mi, index, p)
	if err != nil {
		return false, err
	}
	if v1OK != v2OK {
		return false, fmt.Errorf("storage: piece %d disagrees between its v1 (%v) and v2 (%v) hash — hybrid torrent content mismatch", index, v1OK, v2OK)
	}
	return v1OK, nil
}

// pieceOffset resolves piece index to its byte offset in this Storage's
// own flat on-disk address space. For v1/hybrid this is the ordinary
// index*PieceLength flat formula (correct for hybrid too, since its BEP
// 47 padding files already make that address space gap-free — see
// Storage.New's own v2 branch doc comment for why a pure-v2 torrent's
// disk layout and wire-protocol piece addressing are, deliberately, two
// different address spaces). For pure v2, it goes through
// MetaInfo.PieceFile plus this Storage's own FileRegion offset for that
// file, since there is no padding to make a flat index*PieceLength
// formula correct.
func (s *Storage) pieceOffset(mi *metainfo.MetaInfo, index int) (int64, error) {
	if !mi.IsPureV2() {
		return int64(index) * mi.Info.PieceLength, nil
	}
	fileIndex, offsetInFile, ok := mi.PieceFile(index)
	if !ok {
		return 0, fmt.Errorf("storage: piece %d out of range", index)
	}
	if fileIndex >= len(s.files) {
		return 0, fmt.Errorf("storage: piece %d resolves to file %d, storage has %d files", index, fileIndex, len(s.files))
	}
	return s.files[fileIndex].Offset + offsetInFile, nil
}

// verifyPieceV2 hashes p (already read from disk) into its own piece root
// and checks it against the covering file's known merkle data — either
// directly against PiecesRoot, for a file no bigger than one piece, or
// against the matching entry of that file's own 'piece layers' value, for
// a larger one. A file whose 'piece layers' entry isn't known yet (the
// magnet path, before the hash-request/hashes wire exchange fills it in)
// reports false with no error — not yet verifiable, the same shape a
// missing/incomplete file on disk already reports.
//
// p's own real v2 content length is computed independently here from the
// covering file's remaining bytes — deliberately NOT just len(p) as the
// caller sized it. For a hybrid torrent, the caller's buffer is sized by
// the v1/flat PieceLen, which for a piece a BEP 47 padding file closes
// out is the *padded* full piece length; v2's own per-file piece length
// for that same piece is shorter (just the file's own real tail bytes) —
// a real bug this distinction fixes, caught by this package's own hybrid
// padding test hashing the padding bytes as if they were part of the
// next file's own v2 leaves, which they never are.
func (s *Storage) verifyPieceV2(mi *metainfo.MetaInfo, index int, p []byte) (bool, error) {
	fileIndex, offsetInFile, ok := mi.PieceFile(index)
	if !ok {
		return false, nil
	}
	f := mi.V2Files[fileIndex]
	blocksPerPiece := int(mi.Info.PieceLength / merkle.BlockSize)

	v2Length := f.Length - offsetInFile
	if v2Length > mi.Info.PieceLength {
		v2Length = mi.Info.PieceLength
	}
	if v2Length > int64(len(p)) {
		v2Length = int64(len(p))
	}
	p = p[:v2Length]

	var leaves []merkle.Hash
	for off := 0; off < len(p); off += merkle.BlockSize {
		end := off + merkle.BlockSize
		if end > len(p) {
			end = len(p)
		}
		leaves = append(leaves, merkle.Leaf(p[off:end]))
	}
	soleFilePiece := f.Length <= mi.Info.PieceLength
	pieceRoot := merkle.PieceRoot(leaves, blocksPerPiece, soleFilePiece)

	if soleFilePiece {
		return pieceRoot == merkle.Hash(f.PiecesRoot), nil
	}

	layerBytes, ok := mi.PieceLayers[f.PiecesRoot]
	if !ok {
		return false, nil
	}
	pieceInFile := int(offsetInFile / mi.Info.PieceLength)
	start := pieceInFile * merkle.DigestSize
	if start+merkle.DigestSize > len(layerBytes) {
		return false, nil
	}
	var want merkle.Hash
	copy(want[:], layerBytes[start:start+merkle.DigestSize])
	return pieceRoot == want, nil
}
