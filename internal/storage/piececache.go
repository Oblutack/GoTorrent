package storage

import (
	"crypto/sha1"
	"fmt"
	"sync"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// PieceCache buffers whole pieces in memory as their blocks arrive, then
// verifies straight from that buffer and writes it to the underlying
// Storage in one call — write coalescing (many 16 KiB block writes
// collapse into a single write per piece) plus verification-read
// elision: the usual hash-a-piece-right-after-it-completes pass no
// longer has to read anything back from disk, since the bytes are
// already sitting in memory. A piece that fails its hash check under
// this scheme never touches disk at all, a real improvement over always
// writing each block as it arrives and only finding out afterward that
// some of them were wrong.
//
// A piece commits to one mode — buffered, or straight through to
// Storage.WriteAt — the moment its first block arrives, and stays that
// way for the rest of its lifetime. Switching partway through would mean
// a later WriteBlock call filling a fresh in-memory buffer that is
// silently missing whatever earlier blocks already went straight to
// disk, corrupting the eventual flush — see WriteBlock's own doc
// comment for how that's avoided.
//
// Bounded by maxBytes: once buffering a new piece would exceed it,
// WriteBlock falls back to straight-through for that piece instead of
// blocking or evicting anything already in flight. This cache is a
// best-effort accelerator a caller can always safely ignore the return
// value of — never a correctness requirement — which is what makes the
// bound simple: no eviction policy to get right, just "no room, use the
// path that always works instead."
//
// Not durable across a crash: buffered bytes exist only in this
// process's memory until a piece completes and is flushed. A graceful
// shutdown is safe (internal/torrent's own actor tracks every in-flight
// verify — and so every in-flight flush — under its wg.Wait()); an
// abrupt crash can lose up to maxBytes worth of otherwise-fully-received
// piece data, which then simply gets re-requested on the next run — the
// same class of exposure this codebase already accepts for the OS's own
// page cache between resume.go's periodic checkpoints, just larger in
// the worst case.
type PieceCache struct {
	storage *Storage

	mu       sync.Mutex
	maxBytes int64
	used     int64
	pieces   map[int]*cachedPiece
}

type cachedPiece struct {
	offset int64 // torrent-flat offset where this piece begins
	data   []byte
}

// NewPieceCache returns a PieceCache that buffers up to maxBytes total
// across every piece currently in flight, flushing through storage.
// maxBytes <= 0 is a valid, if useless, degenerate case: every piece
// falls straight through immediately, identical to not having a cache
// at all.
func NewPieceCache(storage *Storage, maxBytes int64) *PieceCache {
	return &PieceCache{storage: storage, maxBytes: maxBytes, pieces: make(map[int]*cachedPiece)}
}

// WriteBlock buffers one block of piece index if there is room to start
// buffering that piece, reporting whether it did. false means the
// caller must write the block to the underlying Storage itself — the
// ordinary, always-correct path this cache is an optional accelerator
// on top of. pieceOffset is where the piece begins in the torrent's
// flat byte space; pieceLength is its real length (the short final
// piece included) — both known to the caller already, since it needs
// them to compute the block's own absolute offset regardless of whether
// this cache is in play.
func (c *PieceCache) WriteBlock(index int, pieceOffset, pieceLength int64, begin int, data []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	p, buffered := c.pieces[index]
	if !buffered {
		if c.used+pieceLength > c.maxBytes {
			return false
		}
		p = &cachedPiece{offset: pieceOffset, data: make([]byte, pieceLength)}
		c.pieces[index] = p
		c.used += pieceLength
	}
	copy(p.data[begin:], data)
	return true
}

// TryVerify hashes piece index straight from its buffered bytes and, if
// they match expected, writes them to Storage in one call — the whole
// point of this type. found is false when index was never buffered
// (every one of its blocks already went through WriteBlock's fallback
// path), the caller's signal to fall back to Storage.VerifyOne's
// ordinary disk-read verification instead. Releases the piece's memory
// either way: a piece is verified at most once per completion.
func (c *PieceCache) TryVerify(index int, expected metainfo.Hash) (ok, found bool, err error) {
	c.mu.Lock()
	p, buffered := c.pieces[index]
	if buffered {
		delete(c.pieces, index)
		c.used -= int64(len(p.data))
	}
	c.mu.Unlock()
	if !buffered {
		return false, false, nil
	}

	if metainfo.Hash(sha1.Sum(p.data)) != expected {
		return false, true, nil
	}
	if _, err := c.storage.WriteAt(p.data, p.offset); err != nil {
		return false, true, fmt.Errorf("storage: flushing cached piece %d: %w", index, err)
	}
	return true, true, nil
}

// Used is the total bytes currently buffered across every in-flight
// piece — exposed for tests and diagnostics, not part of this type's
// own control flow.
func (c *PieceCache) Used() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}
