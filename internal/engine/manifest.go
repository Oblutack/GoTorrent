package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// manifestVersion is bumped whenever the on-disk format changes
// incompatibly. Like resume data, a version this build does not recognise is
// treated as absent rather than corrupt.
//
// v2: torrent_path (a filesystem path only) became source (a filesystem path
// or a magnet: URI), when Add gained magnet support.
// v3: added category and tags (3.5) — unlike queue position or per-torrent
// rate limits, these are meant to survive a restart. No migration from v2:
// same precedent as the v1->v2 bump. A v2 manifest is not read by a v3
// build (see readManifest's version check) — the fleet it recorded has to
// be re-Added by hand, though nothing about the actual downloaded data or
// the .torrent files/magnets themselves is touched or lost.
const manifestVersion = 3

var manifestMagic = [4]byte{'G', 'T', 'F', 'L'}

// manifestEntry is one torrent recorded in the manifest, decoded from its
// wire form.
type manifestEntry struct {
	InfoHash    metainfo.Hash
	Source      string
	DownloadDir string
	Category    string
	Tags        []string
}

// manifestEntryWire and manifestWire are the exact bencoded shapes, kept
// separate from manifestEntry so metainfo.Hash round-trips as hex text
// (readable in the file on disk) rather than needing bencode to know about
// the type.
type manifestEntryWire struct {
	InfoHash    string   `bencode:"info_hash"`
	Source      string   `bencode:"source"`
	DownloadDir string   `bencode:"download_dir"`
	Category    string   `bencode:"category,omitempty"`
	Tags        []string `bencode:"tags,omitempty"`
}

type manifestWire struct {
	Magic   string              `bencode:"magic"`
	Version int                 `bencode:"version"`
	Entries []manifestEntryWire `bencode:"entries"`
}

func (e *Engine) manifestPath() string {
	return filepath.Join(e.stateDir, "fleet.manifest")
}

// saveManifestLocked writes the current set of managed torrents to disk
// atomically (temp file + rename), mirroring internal/torrent's resume data.
// Callers must hold e.mu.
func (e *Engine) saveManifestLocked() error {
	if err := os.MkdirAll(e.stateDir, 0o755); err != nil {
		return fmt.Errorf("engine: creating state directory: %w", err)
	}

	wire := manifestWire{Magic: string(manifestMagic[:]), Version: manifestVersion}
	for hash, mt := range e.torrents {
		wire.Entries = append(wire.Entries, manifestEntryWire{
			InfoHash:    hash.String(),
			Source:      mt.source,
			DownloadDir: mt.downloadDir,
			Category:    mt.category,
			Tags:        mt.tags,
		})
	}
	sort.Slice(wire.Entries, func(i, j int) bool { return wire.Entries[i].InfoHash < wire.Entries[j].InfoHash })

	data, err := bencode.Marshal(wire)
	if err != nil {
		return fmt.Errorf("engine: encoding manifest: %w", err)
	}

	final := e.manifestPath()
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("engine: writing manifest: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("engine: committing manifest: %w", err)
	}
	return nil
}

// readManifest loads the manifest from disk. A missing file is reported via
// the plain os.ErrNotExist-wrapping error os.ReadFile returns, so callers
// distinguish "no manifest yet" from a real read failure with errors.Is.
func (e *Engine) readManifest() ([]manifestEntry, error) {
	data, err := os.ReadFile(e.manifestPath())
	if err != nil {
		return nil, err
	}

	var wire manifestWire
	if err := bencode.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("engine: decoding manifest: %w", err)
	}
	if wire.Magic != string(manifestMagic[:]) {
		return nil, fmt.Errorf("engine: manifest has an unrecognised format")
	}
	if wire.Version != manifestVersion {
		// A stale-but-recognisable version, not corruption: honor the
		// package doc's promise that this is "treated as absent rather
		// than corrupt" (Load swallows os.ErrNotExist) instead of the
		// fatal error a plain mismatch used to bubble all the way up to
		// main.go — a version bump must not brick an existing fleet's
		// manifest. Every torrent's own .torrent file/magnet is untouched,
		// so re-adding is enough to pick the fleet back up; just not
		// automatically.
		logger.Warning.Printf("engine: manifest is format version %d, this build writes version %d - starting with an empty fleet instead of one it can't fully understand\n", wire.Version, manifestVersion)
		return nil, os.ErrNotExist
	}

	entries := make([]manifestEntry, 0, len(wire.Entries))
	for _, we := range wire.Entries {
		hash, err := metainfo.ParseHash(we.InfoHash)
		if err != nil {
			return nil, fmt.Errorf("engine: manifest entry %q: %w", we.Source, err)
		}
		entries = append(entries, manifestEntry{
			InfoHash:    hash,
			Source:      we.Source,
			DownloadDir: we.DownloadDir,
			Category:    we.Category,
			Tags:        we.Tags,
		})
	}
	return entries, nil
}
