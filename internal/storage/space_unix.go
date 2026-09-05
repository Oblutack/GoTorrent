//go:build !windows

package storage

import "syscall"

// availableSpace reports the free bytes usable by this process on the
// filesystem holding path.
func availableSpace(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

// isNotExist reports whether err means the file is not there.
func isNotExist(err error) bool {
	return errorIsNotExist(err)
}
