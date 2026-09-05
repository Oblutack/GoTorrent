package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// Allocation is how files are created on disk.
type Allocation int

const (
	// Sparse creates files at their final size without writing data. Fast, and
	// the OS only commits blocks as they are written — but the free-space
	// check at allocation time is the only warning you get before running out
	// mid-download.
	Sparse Allocation = iota

	// Full writes zeros across every file up front. Slow, but it reserves the
	// space for real and keeps the file contiguous on spinning disks.
	Full
)

func (a Allocation) String() string {
	if a == Full {
		return "full"
	}
	return "sparse"
}

// ErrShortWrite is returned when the underlying files could not accept a whole
// write, which means the torrent geometry and what is on disk disagree.
var ErrShortWrite = errors.New("storage: short write")

// Storage presents a torrent's files as one contiguous byte space.
//
// Everything above it addresses data by torrent-global offset and never needs
// to know how many files there are or where the boundaries fall. It implements
// io.ReaderAt and io.WriterAt, which are safe for concurrent use — unlike
// Seek+Write, which races when two goroutines share a handle.
type Storage struct {
	layout *Layout
	files  []FileRegion
	total  int64

	cache      *handleCache
	allocation Allocation
	dirMode    os.FileMode
	fileMode   os.FileMode

	mu        sync.Mutex
	allocated bool
	closed    bool
}

// FileRegion is one file's placement inside the torrent's byte space.
type FileRegion struct {
	Path   string // absolute path on disk
	Offset int64  // where this file starts within the torrent
	Length int64
}

// End is the exclusive end offset of the region within the torrent.
func (f FileRegion) End() int64 { return f.Offset + f.Length }

// Option configures a Storage.
type Option func(*Storage)

// WithAllocation selects sparse or full allocation.
func WithAllocation(a Allocation) Option {
	return func(s *Storage) { s.allocation = a }
}

// WithMaxOpenFiles bounds the open file handle cache.
func WithMaxOpenFiles(n int) Option {
	return func(s *Storage) { s.cache = newHandleCache(n) }
}

// WithFileMode sets the permissions used for created files and directories.
func WithFileMode(file, dir os.FileMode) Option {
	return func(s *Storage) {
		s.fileMode = file
		s.dirMode = dir
	}
}

// New builds the storage for one torrent under downloadDir.
func New(downloadDir string, mi *metainfo.MetaInfo, opts ...Option) (*Storage, error) {
	if mi == nil {
		return nil, metainfo.ErrNoMetadata
	}

	layout, err := NewLayout(downloadDir, mi.Info.Name, mi.Info.IsMultiFile())
	if err != nil {
		return nil, err
	}

	s := &Storage{
		layout:     layout,
		total:      mi.TotalLength,
		cache:      newHandleCache(DefaultMaxOpenFiles),
		allocation: Sparse,
		dirMode:    0o755,
		fileMode:   0o644,
	}
	for _, opt := range opts {
		opt(s)
	}

	if mi.Info.IsMultiFile() {
		var offset int64
		for _, f := range mi.Info.Files {
			path, err := layout.Resolve(f.Path)
			if err != nil {
				return nil, err
			}
			s.files = append(s.files, FileRegion{Path: path, Offset: offset, Length: f.Length})
			offset += f.Length
		}
	} else {
		path, err := layout.Resolve(nil)
		if err != nil {
			return nil, err
		}
		s.files = append(s.files, FileRegion{Path: path, Offset: 0, Length: mi.Info.Length})
	}

	return s, nil
}

// Files returns the torrent's file layout.
func (s *Storage) Files() []FileRegion { return s.files }

// TotalLength is the torrent's size in bytes.
func (s *Storage) TotalLength() int64 { return s.total }

// Root is the directory holding this torrent's data.
func (s *Storage) Root() string { return s.layout.Base() }

// Allocate creates the directory tree and the files. It is safe to call on an
// existing download: files that are already the right size are left alone.
func (s *Storage) Allocate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.allocated {
		return nil
	}

	if err := os.MkdirAll(s.layout.Base(), s.dirMode); err != nil {
		return fmt.Errorf("storage: creating %s: %w", s.layout.Base(), err)
	}

	if err := s.checkFreeSpace(); err != nil {
		return err
	}

	for _, region := range s.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.allocateFile(ctx, region); err != nil {
			return err
		}
	}

	s.allocated = true
	return nil
}

func (s *Storage) allocateFile(ctx context.Context, region FileRegion) error {
	if err := os.MkdirAll(filepath.Dir(region.Path), s.dirMode); err != nil {
		return fmt.Errorf("storage: creating directory for %s: %w", region.Path, err)
	}

	f, err := os.OpenFile(region.Path, os.O_CREATE|os.O_RDWR, s.fileMode)
	if err != nil {
		return fmt.Errorf("storage: creating %s: %w", region.Path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("storage: stat %s: %w", region.Path, err)
	}
	if info.Size() != region.Length {
		if err := f.Truncate(region.Length); err != nil {
			return fmt.Errorf("storage: sizing %s to %d bytes: %w", region.Path, region.Length, err)
		}
	}

	if s.allocation == Full {
		if err := writeZeros(ctx, f, region.Length); err != nil {
			return fmt.Errorf("storage: pre-allocating %s: %w", region.Path, err)
		}
	}
	return nil
}

// writeZeros fills a file with zeros so the space is genuinely reserved rather
// than merely promised by a sparse file.
func writeZeros(ctx context.Context, f *os.File, length int64) error {
	const chunk = 1 << 20
	zeros := make([]byte, chunk)
	for off := int64(0); off < length; off += chunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := int64(chunk)
		if remaining := length - off; remaining < n {
			n = remaining
		}
		if _, err := f.WriteAt(zeros[:n], off); err != nil {
			return err
		}
	}
	return f.Sync()
}

// checkFreeSpace refuses to start a download that obviously cannot fit. It is
// advisory: the check can be wrong on network filesystems, and other processes
// are free to consume the space afterwards.
func (s *Storage) checkFreeSpace() error {
	available, err := availableSpace(s.layout.Base())
	if err != nil {
		// Not being able to ask is not a reason to refuse the download.
		return nil
	}
	if available >= 0 && available < s.total {
		return fmt.Errorf("storage: %s has %d bytes free, torrent needs %d",
			s.layout.Base(), available, s.total)
	}
	return nil
}

// regionAt returns the index of the file containing offset, or -1.
func (s *Storage) regionAt(offset int64) int {
	i := sort.Search(len(s.files), func(i int) bool {
		return s.files[i].End() > offset
	})
	if i < len(s.files) && s.files[i].Offset <= offset {
		return i
	}
	return -1
}

// ReadAt reads len(p) bytes from the torrent's byte space starting at off,
// crossing file boundaries as needed. It satisfies io.ReaderAt.
func (s *Storage) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("storage: negative offset %d", off)
	}
	if off >= s.total {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}

	read := 0
	for read < len(p) {
		current := off + int64(read)
		if current >= s.total {
			return read, io.EOF
		}
		idx := s.regionAt(current)
		if idx < 0 {
			return read, fmt.Errorf("storage: offset %d is outside the torrent", current)
		}
		region := s.files[idx]

		want := len(p) - read
		if room := region.End() - current; int64(want) > room {
			want = int(room)
		}
		// A zero-length file occupies no space; skip past it.
		if want == 0 {
			continue
		}

		f, release, err := s.cache.acquire(region.Path, false)
		if err != nil {
			return read, fmt.Errorf("storage: opening %s: %w", region.Path, err)
		}
		n, err := f.ReadAt(p[read:read+want], current-region.Offset)
		release()
		read += n
		if err != nil && !errors.Is(err, io.EOF) {
			return read, fmt.Errorf("storage: reading %s: %w", region.Path, err)
		}
		if n < want {
			return read, io.ErrUnexpectedEOF
		}
	}
	return read, nil
}

// WriteAt writes p into the torrent's byte space at off, crossing file
// boundaries as needed. It satisfies io.WriterAt and is safe to call
// concurrently for non-overlapping ranges.
func (s *Storage) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("storage: negative offset %d", off)
	}
	if off+int64(len(p)) > s.total {
		return 0, fmt.Errorf("storage: write of %d bytes at %d overruns the torrent (%d bytes)",
			len(p), off, s.total)
	}

	written := 0
	for written < len(p) {
		current := off + int64(written)
		idx := s.regionAt(current)
		if idx < 0 {
			return written, fmt.Errorf("storage: offset %d is outside the torrent", current)
		}
		region := s.files[idx]

		want := len(p) - written
		if room := region.End() - current; int64(want) > room {
			want = int(room)
		}
		if want == 0 {
			continue
		}

		f, release, err := s.cache.acquire(region.Path, true)
		if err != nil {
			return written, fmt.Errorf("storage: opening %s for writing: %w", region.Path, err)
		}
		n, err := f.WriteAt(p[written:written+want], current-region.Offset)
		release()
		written += n
		if err != nil {
			return written, fmt.Errorf("storage: writing %s: %w", region.Path, err)
		}
		if n < want {
			return written, ErrShortWrite
		}
	}
	return written, nil
}

// Sync flushes every cached handle to disk.
func (s *Storage) Sync() error {
	s.cache.mu.Lock()
	files := make([]*os.File, 0, s.cache.lru.Len())
	for elem := s.cache.lru.Front(); elem != nil; elem = elem.Next() {
		entry := elem.Value.(*cacheEntry)
		if entry.writable {
			files = append(files, entry.file)
		}
	}
	s.cache.mu.Unlock()

	var firstErr error
	for _, f := range files {
		if err := f.Sync(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Close releases every open handle.
func (s *Storage) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	return s.cache.closeAll()
}
