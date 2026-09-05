// Package storage maps torrent-relative paths onto the filesystem. It is the
// single place in the client where a name that arrived off the network turns
// into a path that gets opened, so the containment check lives here and
// nowhere else.
package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Layout knows where a torrent's files live on disk.
//
// A single-file torrent writes straight into the download directory as
// <downloadDir>/<name>. A multi-file torrent nests everything under
// <downloadDir>/<name>/, and every resolved path must stay inside that
// directory.
type Layout struct {
	base   string // directory every resolved path must stay within
	single string // the file itself, for single-file torrents
}

// NewLayout builds a layout for one torrent. torrentName must already have
// passed metainfo.ValidatePathSegment.
func NewLayout(downloadDir, torrentName string, multiFile bool) (*Layout, error) {
	if torrentName == "" {
		return nil, errors.New("storage: torrent name is empty")
	}

	root, err := filepath.Abs(downloadDir)
	if err != nil {
		return nil, fmt.Errorf("storage: resolving download directory %q: %w", downloadDir, err)
	}

	if multiFile {
		return &Layout{base: filepath.Join(root, torrentName)}, nil
	}

	l := &Layout{base: root, single: filepath.Join(root, torrentName)}
	// The name is a single validated segment, but re-check the result: this is
	// the invariant the whole package exists to guarantee.
	if err := l.contains(l.single); err != nil {
		return nil, err
	}
	return l, nil
}

// Base is the directory that holds this torrent's data. For a multi-file
// torrent it is the torrent's own directory; for a single-file torrent it is
// the download directory.
func (l *Layout) Base() string { return l.base }

// Resolve turns a torrent-relative path into an absolute filesystem path,
// refusing anything that would land outside Base. Pass nil for the single file
// of a single-file torrent.
func (l *Layout) Resolve(parts []string) (string, error) {
	if len(parts) == 0 {
		if l.single == "" {
			return "", errors.New("storage: multi-file torrent requires a path")
		}
		return l.single, nil
	}

	// filepath.Join cleans the result, so "a/../../etc" collapses before the
	// containment check sees it.
	full := filepath.Join(append([]string{l.base}, parts...)...)
	if err := l.contains(full); err != nil {
		return "", fmt.Errorf("storage: %q: %w", strings.Join(parts, "/"), err)
	}
	return full, nil
}

// contains reports an error unless path is strictly inside base.
func (l *Layout) contains(path string) error {
	rel, err := filepath.Rel(l.base, path)
	if err != nil {
		// Rel fails when the two paths are on different volumes, which is
		// itself an escape.
		return fmt.Errorf("path escapes the download directory: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errors.New("path escapes the download directory")
	}
	return nil
}
