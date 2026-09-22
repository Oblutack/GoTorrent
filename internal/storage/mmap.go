package storage

import (
	"container/list"
	"os"
	"sync"
)

// platformHandle is whatever extra platform-specific state a mapping
// needs to keep around between platformMap and platformUnmap/
// platformFlushView — an opaque any rather than a concrete type because
// the two platforms need genuinely different things here: Windows needs
// the CreateFileMappingW kernel object's own handle (see mmap_windows.go),
// Unix needs nothing at all beyond the mapped slice itself (see
// mmap_unix.go).
type platformHandle any

// mmapRegion is one file's whole-file memory mapping — the mmap-backed
// counterpart to handleCache's plain *os.File. By the time anything maps
// a file, Storage.allocateFile has already truncated it to exactly the
// region's real length (the same "allocate before any I/O touches it"
// ordering both storage backends rely on), so a mapping never needs to
// grow or shrink for as long as it lives.
type mmapRegion struct {
	file   *os.File
	data   []byte
	handle platformHandle
}

// openMmapRegion opens path read-write (it must already exist and be the
// right size — this never creates or resizes a file, same as
// handleCache.acquire's own read path) and maps the whole of it.
func openMmapRegion(path string, size int64) (*mmapRegion, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	data, handle, err := platformMap(f, size)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &mmapRegion{file: f, data: data, handle: handle}, nil
}

// sync flushes this region's dirty pages to disk. Order matters on
// Windows (view → system cache, then system cache → disk — see
// platformFlushView's own doc comment for why); platformFlushView is a
// no-op on the platforms where fsync alone already covers it.
func (r *mmapRegion) sync() error {
	if err := platformFlushView(r.data, r.handle); err != nil {
		return err
	}
	return r.file.Sync()
}

func (r *mmapRegion) close() error {
	var firstErr error
	if err := platformUnmap(r.data, r.handle); err != nil {
		firstErr = err
	}
	if err := r.file.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// mmapCache is handleCache's mmap-flavored twin — the identical bounded
// LRU-with-refcounting shape (see handles.go's own doc comment for the
// reasoning: a many-file torrent must not exhaust OS resources, whether
// those are open file descriptors or active memory mappings), kept as
// its own type rather than sharing code with handleCache because the two
// manage genuinely different resources with different construction and
// cleanup — not worth a generic abstraction for exactly two, structurally
// simple call sites.
type mmapCache struct {
	mu     sync.Mutex
	max    int
	byPath map[string]*list.Element
	lru    *list.List
}

type mmapCacheEntry struct {
	path    string
	region  *mmapRegion
	refs    int
	evicted bool
}

func newMmapCache(max int) *mmapCache {
	if max <= 0 {
		max = DefaultMaxOpenFiles
	}
	return &mmapCache{max: max, byPath: make(map[string]*list.Element), lru: list.New()}
}

// acquire returns the mapped bytes for path (mapping it fresh on a miss)
// plus a release function the caller must call when done — the exact
// same contract handleCache.acquire already has. size is only consulted
// on a miss; an already-mapped region is reused as-is regardless of what
// size is passed on a later call, which is safe here because a file
// region's length never changes after Storage first computes it from the
// torrent's own metainfo.
func (c *mmapCache) acquire(path string, size int64) ([]byte, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.byPath[path]; ok {
		entry := elem.Value.(*mmapCacheEntry)
		c.lru.MoveToFront(elem)
		entry.refs++
		return entry.region.data, func() { c.release(entry) }, nil
	}

	region, err := openMmapRegion(path, size)
	if err != nil {
		return nil, nil, err
	}
	entry := &mmapCacheEntry{path: path, region: region, refs: 1}
	c.byPath[path] = c.lru.PushFront(entry)
	c.trim()
	return region.data, func() { c.release(entry) }, nil
}

func (c *mmapCache) release(entry *mmapCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry.refs--
	if entry.evicted && entry.refs <= 0 {
		entry.region.close()
	}
}

// trim mirrors handleCache.trim exactly: evict least-recently-used idle
// entries until under budget, overshooting rather than blocking if
// everything cached is currently in use.
func (c *mmapCache) trim() {
	for c.lru.Len() > c.max {
		evicted := false
		for elem := c.lru.Back(); elem != nil; elem = elem.Prev() {
			if elem.Value.(*mmapCacheEntry).refs == 0 {
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

func (c *mmapCache) evict(elem *list.Element) {
	entry := elem.Value.(*mmapCacheEntry)
	c.lru.Remove(elem)
	delete(c.byPath, entry.path)
	entry.evicted = true
	if entry.refs <= 0 {
		entry.region.close()
	}
}

// closeAll unmaps every cached region. Entries still in use are closed by
// their last release, same as handleCache.closeAll.
func (c *mmapCache) closeAll() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var firstErr error
	for elem := c.lru.Front(); elem != nil; {
		next := elem.Next()
		entry := elem.Value.(*mmapCacheEntry)
		c.lru.Remove(elem)
		delete(c.byPath, entry.path)
		entry.evicted = true
		if entry.refs <= 0 {
			if err := entry.region.close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		elem = next
	}
	return firstErr
}

// syncAll flushes every currently-mapped region to disk — Storage.Sync's
// mmap counterpart to closing over every writable *os.File it has cached.
// Snapshots the region list under the lock, then flushes without holding
// it, so a slow flush on one file never blocks a concurrent acquire/
// release of a different one.
func (c *mmapCache) syncAll() error {
	c.mu.Lock()
	regions := make([]*mmapRegion, 0, c.lru.Len())
	for elem := c.lru.Front(); elem != nil; elem = elem.Next() {
		regions = append(regions, elem.Value.(*mmapCacheEntry).region)
	}
	c.mu.Unlock()

	var firstErr error
	for _, r := range regions {
		if err := r.sync(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// openCount reports how many regions are currently mapped. Used by tests.
func (c *mmapCache) openCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}
