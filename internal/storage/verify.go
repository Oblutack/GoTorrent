package storage

import (
	"context"
	"crypto/sha1"
	"errors"
	"io"
	"runtime"
	"sync"

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

// verifyPiece reads one piece and compares its SHA-1 with the metainfo. A
// piece that is missing or truncated on disk reports false with no error: that
// is what an incomplete download looks like.
func (s *Storage) verifyPiece(mi *metainfo.MetaInfo, index int, buf []byte) (bool, error) {
	length := mi.PieceLen(index)
	if length <= 0 {
		return false, nil
	}
	if int64(len(buf)) < length {
		buf = make([]byte, length)
	}
	p := buf[:length]

	offset := int64(index) * mi.Info.PieceLength
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

	return metainfo.Hash(sha1.Sum(p)) == mi.PieceHashes[index], nil
}
