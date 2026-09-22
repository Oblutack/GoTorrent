//go:build !windows

package storage

import (
	"fmt"
	"os"
	"syscall"
)

// platformMap memory-maps the whole of f (already truncated to exactly
// size bytes by the caller) read-write and shared, so a write through the
// mapping is a write to the real file, not a private copy-on-write page.
// f must stay open for the mapping's whole lifetime — the mapping itself
// keeps no reference to the file descriptor after this call returns.
func platformMap(f *os.File, size int64) ([]byte, platformHandle, error) {
	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, nil, fmt.Errorf("storage: mmap %s: %w", f.Name(), err)
	}
	return data, nil, nil
}

// platformUnmap reverses platformMap. handle is unused on this platform —
// syscall.Munmap only needs the mapped slice itself.
func platformUnmap(data []byte, handle platformHandle) error {
	if err := syscall.Munmap(data); err != nil {
		return fmt.Errorf("storage: munmap: %w", err)
	}
	return nil
}

// platformFlushView is a no-op on every platform this file covers: POSIX
// guarantees fsync(2) on any open file descriptor for a file flushes that
// file's dirty pages to stable storage regardless of whether they were
// dirtied through write(2) or through a shared mmap — there is nothing a
// separate msync(2) call would add for this package's own purposes (a
// single process, never reading its own mmap through a second, distinct
// mapping), and msync's own availability is not uniformly guaranteed
// across every GOOS the portable syscall package covers. mmapRegion.Sync
// already calls the underlying *os.File's own Sync method, which is
// exactly that fsync call.
func platformFlushView([]byte, platformHandle) error { return nil }
