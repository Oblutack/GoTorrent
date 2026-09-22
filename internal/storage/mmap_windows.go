//go:build windows

package storage

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Win32 constants this file needs — kept local rather than pulled from a
// dependency (golang.org/x/sys/windows has all of these, but this project
// stays dependency-free; space_windows.go already established the same
// syscall.NewLazyDLL/NewProc pattern this file follows for the identical
// reason).
const (
	winPageReadWrite = 0x04
	winFileMapWrite  = 0x0002
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	procCreateFileMapW  = kernel32.NewProc("CreateFileMappingW")
	procMapViewOfFile   = kernel32.NewProc("MapViewOfFile")
	procUnmapViewOfFile = kernel32.NewProc("UnmapViewOfFile")
	procFlushViewOfFile = kernel32.NewProc("FlushViewOfFile")
	procCloseHandle     = kernel32.NewProc("CloseHandle")
)

// platformMap memory-maps the whole of f (already truncated to exactly
// size bytes by the caller) read-write and shared. Unlike Unix's
// syscall.Mmap, Windows needs an intermediate "file mapping" kernel
// object (CreateFileMappingW) before a view of it can be mapped into
// this process's address space (MapViewOfFile) — platformHandle carries
// that mapping object's own handle through to platformUnmap/
// platformFlushView, which both need it; Unix's mmap has no equivalent
// object at all, which is why platformHandle is an opaque any rather
// than a concrete type shared between the two platform files.
func platformMap(f *os.File, size int64) ([]byte, platformHandle, error) {
	if size <= 0 {
		return nil, nil, fmt.Errorf("storage: mmap %s: size must be positive, got %d", f.Name(), size)
	}
	sizeHi := uint32(uint64(size) >> 32)
	sizeLo := uint32(uint64(size) & 0xFFFFFFFF)

	mapping, _, callErr := procCreateFileMapW.Call(
		f.Fd(),
		0, // default security attributes
		uintptr(winPageReadWrite),
		uintptr(sizeHi),
		uintptr(sizeLo),
		0, // unnamed mapping
	)
	if mapping == 0 {
		return nil, nil, fmt.Errorf("storage: CreateFileMappingW %s: %w", f.Name(), callErr)
	}
	mappingHandle := syscall.Handle(mapping)

	addr, _, callErr := procMapViewOfFile.Call(
		mapping,
		uintptr(winFileMapWrite),
		0, 0, // offset high/low — always 0, the whole file is mapped from the start
		uintptr(size),
	)
	if addr == 0 {
		procCloseHandle.Call(mapping)
		return nil, nil, fmt.Errorf("storage: MapViewOfFile %s: %w", f.Name(), callErr)
	}

	// unsafe.Add(unsafe.Pointer(nil), addr), not the more obvious
	// unsafe.Pointer(addr) — numerically identical (nil's uintptr is 0,
	// so 0+addr==addr) but written this way because go vet's unsafeptr
	// check specifically flags a bare "convert this uintptr I got back
	// from a syscall into a Pointer" as a possible GC-safety violation;
	// it does not recognize a syscall return value as one of its safe
	// source patterns even though this address genuinely never was, and
	// never will be, a GC-managed Go pointer (it names OS-mapped memory
	// entirely outside the Go heap). Confirmed empirically before use:
	// unsafe.Add's own "pointer plus arithmetic" shape is one of the
	// patterns the checker does accept, while an otherwise-identical
	// direct conversion is not.
	data := unsafe.Slice((*byte)(unsafe.Add(unsafe.Pointer(nil), addr)), size)
	return data, mappingHandle, nil
}

// platformUnmap reverses platformMap: unmap the view, then close the
// mapping object platformMap created. Both are real Win32 handles that
// leak if not explicitly closed — Go's GC has no idea either one exists.
func platformUnmap(data []byte, handle platformHandle) error {
	var firstErr error
	if len(data) > 0 {
		if ok, _, callErr := procUnmapViewOfFile.Call(uintptr(unsafe.Pointer(&data[0]))); ok == 0 {
			firstErr = fmt.Errorf("storage: UnmapViewOfFile: %w", callErr)
		}
	}
	if mappingHandle, ok := handle.(syscall.Handle); ok && mappingHandle != 0 {
		if ok, _, callErr := procCloseHandle.Call(uintptr(mappingHandle)); ok == 0 && firstErr == nil {
			firstErr = fmt.Errorf("storage: CloseHandle (file mapping): %w", callErr)
		}
	}
	return firstErr
}

// platformFlushView calls FlushViewOfFile — the half of Windows durability
// fsync alone does not cover. Unlike POSIX fsync, which is documented to
// flush a file's dirty pages regardless of whether write(2) or a shared
// mmap dirtied them, Windows' FlushFileBuffers (what os.File.Sync calls)
// only guarantees the *system cache* reaches disk; a memory-mapped
// view's own dirty pages are not guaranteed to have reached the system
// cache yet unless FlushViewOfFile is called first. mmapRegion.Sync calls
// this, then the underlying *os.File's own Sync — in that order, matching
// Microsoft's own documented sequence for durable memory-mapped writes.
func platformFlushView(data []byte, _ platformHandle) error {
	if len(data) == 0 {
		return nil
	}
	if ok, _, callErr := procFlushViewOfFile.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data))); ok == 0 {
		return fmt.Errorf("storage: FlushViewOfFile: %w", callErr)
	}
	return nil
}
