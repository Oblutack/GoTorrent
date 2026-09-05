package storage

import (
	"container/list"
	"fmt"
	"os"
	"sync"
)

// DefaultMaxOpenFiles is how many file handles a Storage keeps cached.
const DefaultMaxOpenFiles = 64

// handleCache keeps recently used files open. Without it every block write
// opens and closes a file, which on a multi-file torrent is two syscalls per
// 16 KiB and the dominant cost of the write path.
//
// Handles are shared between goroutines. That is safe because Storage only
// ever uses ReadAt and WriteAt, which do not touch the file's seek offset.
type handleCache struct {
	mu     sync.Mutex
	max    int
	byPath map[string]*list.Element
	lru    *list.List // front is most recently used
}

type cacheEntry struct {
	path     string
	file     *os.File
	writable bool
	refs     int
	evicted  bool
}

func newHandleCache(max int) *handleCache {
	if max <= 0 {
		max = DefaultMaxOpenFiles
	}
	return &handleCache{
		max:    max,
		byPath: make(map[string]*list.Element),
		lru:    list.New(),
	}
}

// acquire returns an open handle for path plus a release function that must be
// called when the caller is done with it. A handle opened read-only is
// reopened if a writable one is requested later.
//
// Files are opened while the cache lock is held. That serialises opens, which
// is fine: a torrent has a handful of files and an open only happens on a miss.
func (c *handleCache) acquire(path string, write bool) (*os.File, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.byPath[path]; ok {
		entry := elem.Value.(*cacheEntry)
		if !write || entry.writable {
			c.lru.MoveToFront(elem)
			entry.refs++
			return entry.file, func() { c.release(entry) }, nil
		}
		// Need write access on a read-only handle: drop it and reopen.
		c.evict(elem)
	}

	flags := os.O_RDONLY
	if write {
		flags = os.O_RDWR
	}
	f, err := os.OpenFile(path, flags, 0)
	if err != nil {
		return nil, nil, err
	}

	entry := &cacheEntry{path: path, file: f, writable: write, refs: 1}
	c.byPath[path] = c.lru.PushFront(entry)
	c.trim()
	return f, func() { c.release(entry) }, nil
}

func (c *handleCache) release(entry *cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry.refs--
	if entry.evicted && entry.refs <= 0 {
		entry.file.Close()
	}
}

// trim evicts least-recently-used entries that nobody is holding. If every
// cached handle is in use the cache overshoots rather than blocking; that is a
// transient condition and blocking here would deadlock the writer.
func (c *handleCache) trim() {
	for c.lru.Len() > c.max {
		evicted := false
		for elem := c.lru.Back(); elem != nil; elem = elem.Prev() {
			if elem.Value.(*cacheEntry).refs == 0 {
				c.evict(elem)
				evicted = true
				break
			}
		}
		if !evicted {
			return
		}
	}
}

// evict removes an entry from the cache, closing it immediately if it is idle
// and deferring the close to the last release otherwise.
func (c *handleCache) evict(elem *list.Element) {
	entry := elem.Value.(*cacheEntry)
	c.lru.Remove(elem)
	delete(c.byPath, entry.path)
	entry.evicted = true
	if entry.refs <= 0 {
		entry.file.Close()
	}
}

// closeAll drops every handle. Entries still in use are closed by their last
// release.
func (c *handleCache) closeAll() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var firstErr error
	for elem := c.lru.Front(); elem != nil; {
		next := elem.Next()
		entry := elem.Value.(*cacheEntry)
		c.lru.Remove(elem)
		delete(c.byPath, entry.path)
		entry.evicted = true
		if entry.refs <= 0 {
			if err := entry.file.Close(); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("storage: closing %s: %w", entry.path, err)
			}
		}
		elem = next
	}
	return firstErr
}

// openCount reports how many handles are currently cached. Used by tests.
func (c *handleCache) openCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}
