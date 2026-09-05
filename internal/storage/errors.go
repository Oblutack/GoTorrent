package storage

import (
	"errors"
	"io/fs"
)

// errorIsNotExist reports whether err, anywhere in its chain, means the file
// does not exist. Storage wraps OS errors with context, so a bare
// os.IsNotExist would miss them.
func errorIsNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
