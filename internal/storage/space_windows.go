//go:build windows

package storage

import (
	"syscall"
	"unsafe"
)

// availableSpace reports the free bytes usable by this process on the volume
// holding path. The path may not exist yet, in which case Windows walks up to
// the nearest existing parent on its own.
func availableSpace(path string) (int64, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")

	utf16Path, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	var freeToCaller, totalBytes, totalFree uint64
	r, _, callErr := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(utf16Path)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return 0, callErr
	}
	return int64(freeToCaller), nil
}

// isNotExist reports whether err means the file is not there.
func isNotExist(err error) bool {
	return errorIsNotExist(err)
}
