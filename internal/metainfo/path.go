package metainfo

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// maxPathSegmentLength is the per-component limit on every mainstream
	// filesystem.
	maxPathSegmentLength = 255

	// maxPathDepth bounds how deeply a torrent may nest its files. Real
	// torrents are a handful of levels deep; anything past this is a way to
	// blow past the OS path limit.
	maxPathDepth = 32
)

// reservedWindowsNames are device names Windows resolves ahead of any file of
// the same name, with or without an extension. Opening one of these talks to a
// device rather than to the disk.
var reservedWindowsNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM0": true, "COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT0": true, "LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// ValidatePathSegment rejects a single component of a torrent-supplied path.
//
// A .torrent file is untrusted input and every segment here ends up in a real
// filesystem path, so anything that could escape the download directory or
// address a device is refused outright rather than rewritten. Sanitizing in
// place would be worse: two different entries in the same torrent could be
// rewritten to the same name and silently overwrite each other.
func ValidatePathSegment(seg string) error {
	switch seg {
	case "":
		return errors.New("path segment is empty")
	case ".", "..":
		return fmt.Errorf("path segment %q is a directory traversal", seg)
	}

	if len(seg) > maxPathSegmentLength {
		return fmt.Errorf("path segment is %d bytes, limit is %d", len(seg), maxPathSegmentLength)
	}

	for _, r := range seg {
		switch {
		case r == 0:
			return errors.New("path segment contains a NUL byte")
		case r == '/' || r == '\\':
			return fmt.Errorf("path segment %q contains a path separator", seg)
		case r == ':':
			return fmt.Errorf("path segment %q contains ':' (drive letter or alternate data stream)", seg)
		case r < 0x20 || r == 0x7f:
			return fmt.Errorf("path segment %q contains a control character", seg)
		}
	}

	// Windows strips trailing dots and spaces, so "evil. " and "evil" would
	// resolve to the same file.
	if last := seg[len(seg)-1]; last == '.' || last == ' ' {
		return fmt.Errorf("path segment %q ends in a dot or a space", seg)
	}

	base := seg
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if reservedWindowsNames[strings.ToUpper(base)] {
		return fmt.Errorf("path segment %q is a reserved device name", seg)
	}

	return nil
}

// ValidatePath validates every component of a multi-file torrent path.
func ValidatePath(parts []string) error {
	if len(parts) == 0 {
		return errors.New("path has no segments")
	}
	if len(parts) > maxPathDepth {
		return fmt.Errorf("path is %d segments deep, limit is %d", len(parts), maxPathDepth)
	}
	for i, part := range parts {
		if err := ValidatePathSegment(part); err != nil {
			return fmt.Errorf("segment %d: %w", i, err)
		}
	}
	return nil
}
