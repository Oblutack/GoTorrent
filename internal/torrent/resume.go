package torrent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/storage"
)

// resumeVersion is bumped whenever the on-disk format changes incompatibly.
// A version an older gottrent wrote that this build does not recognise is
// treated as absent, not corrupt: falling back to a full verify is always
// safe.
const resumeVersion = 1

var resumeMagic = [4]byte{'G', 'T', 'R', 'D'}

// ErrResumeStale is returned by loadResume when the recorded file metadata no
// longer matches what is on disk — the files were touched by something other
// than this client, so the saved bitfield cannot be trusted without a verify.
var ErrResumeStale = errors.New("torrent: resume data is stale")

// resumeFile is one file's identity at the time resume data was written.
type resumeFile struct {
	Path    string `bencode:"path"`
	Length  int64  `bencode:"length"`
	ModTime int64  `bencode:"mod_time"`
}

// resumeData is the versioned, checkpointed state of one torrent's download.
//
// Earlier state was a bare bitfield, trusted blindly and written only on a
// clean Ctrl-C — a crash lost the entire download. This is checkpointed
// periodically (see Torrent.maybeCheckpoint) and records enough about the
// files themselves to detect when trusting the bitfield would be wrong.
type resumeData struct {
	Magic    [4]byte       `bencode:"-"` // handled by MarshalBencode/Unmarshal below
	Version  int           `bencode:"version"`
	InfoHash metainfo.Hash `bencode:"-"`

	TotalLength int64        `bencode:"total_length"`
	PieceBits   []byte       `bencode:"piece_bits"`
	NumPieces   int          `bencode:"num_pieces"`
	Files       []resumeFile `bencode:"files"`

	Downloaded int64  `bencode:"downloaded"`
	Uploaded   int64  `bencode:"uploaded"`
	SavedAtRFC string `bencode:"saved_at"`
}

// resumeWire is the exact bencoded shape, kept separate from resumeData so
// InfoHash (a fixed-size array) round-trips as a plain 20-byte string instead
// of needing bencode to know about metainfo.Hash.
type resumeWire struct {
	Magic       string       `bencode:"magic"`
	Version     int          `bencode:"version"`
	InfoHash    []byte       `bencode:"info_hash"`
	TotalLength int64        `bencode:"total_length"`
	PieceBits   []byte       `bencode:"piece_bits"`
	NumPieces   int          `bencode:"num_pieces"`
	Files       []resumeFile `bencode:"files"`
	Downloaded  int64        `bencode:"downloaded"`
	Uploaded    int64        `bencode:"uploaded"`
	SavedAt     string       `bencode:"saved_at"`
}

// ResumeDir returns the directory resume files are kept in: a per-user config
// directory, not the download directory. Keeping it separate means a user
// deleting or moving downloaded files does not also erase the record of what
// had been verified, and vice versa — the staleness check in loadResume
// decides what to trust, not co-location.
//
// os.UserConfigDir resolves to %AppData% on Windows, ~/Library/Application
// Support on macOS, and $XDG_CONFIG_HOME (or ~/.config) on Linux, so this is
// portable without any platform-specific code.
func ResumeDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("torrent: could not locate a config directory: %w", err)
	}
	return filepath.Join(base, "GoTorrent", "resume"), nil
}

func resumePath(dir string, hash metainfo.Hash) string {
	return filepath.Join(dir, hash.String()+".resume")
}

// buildResume snapshots the current state for checkpointing.
func buildResume(hash metainfo.Hash, mi *metainfo.MetaInfo, st *storage.Storage, have []byte, downloaded, uploaded int64) *resumeData {
	rd := &resumeData{
		Magic:       resumeMagic,
		Version:     resumeVersion,
		InfoHash:    hash,
		TotalLength: mi.TotalLength,
		PieceBits:   append([]byte(nil), have...),
		NumPieces:   mi.NumPieces(),
		Downloaded:  downloaded,
		Uploaded:    uploaded,
		SavedAtRFC:  time.Now().UTC().Format(time.RFC3339),
	}
	for _, f := range st.Files() {
		info, err := os.Stat(f.Path)
		modTime := int64(0)
		length := f.Length
		if err == nil {
			modTime = info.ModTime().UnixNano()
			length = info.Size()
		}
		rd.Files = append(rd.Files, resumeFile{Path: f.Path, Length: length, ModTime: modTime})
	}
	return rd
}

// save writes resume data atomically: a torrent killed mid-write must never
// leave a half-written file that a later load mistakes for valid.
func (rd *resumeData) save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("torrent: creating resume directory: %w", err)
	}

	wire := resumeWire{
		Magic:       string(rd.Magic[:]),
		Version:     rd.Version,
		InfoHash:    rd.InfoHash[:],
		TotalLength: rd.TotalLength,
		PieceBits:   rd.PieceBits,
		NumPieces:   rd.NumPieces,
		Files:       rd.Files,
		Downloaded:  rd.Downloaded,
		Uploaded:    rd.Uploaded,
		SavedAt:     rd.SavedAtRFC,
	}
	data, err := bencode.Marshal(wire)
	if err != nil {
		return fmt.Errorf("torrent: encoding resume data: %w", err)
	}

	final := resumePath(dir, rd.InfoHash)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("torrent: writing resume data: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("torrent: committing resume data: %w", err)
	}
	return nil
}

// loadResume reads and validates resume data for hash, checking it against
// the metainfo and the files actually on disk. Any problem — missing file,
// wrong version, size or mtime drift — returns ErrResumeStale rather than a
// hard error: the caller's answer is always the same, fall back to a full
// verify.
func loadResume(dir string, hash metainfo.Hash, mi *metainfo.MetaInfo, st *storage.Storage) (*resumeData, error) {
	data, err := os.ReadFile(resumePath(dir, hash))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrResumeStale
		}
		return nil, fmt.Errorf("torrent: reading resume data: %w", err)
	}

	var wire resumeWire
	if err := bencode.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("%w: could not decode: %v", ErrResumeStale, err)
	}
	if wire.Magic != string(resumeMagic[:]) || wire.Version != resumeVersion {
		return nil, ErrResumeStale
	}
	fileHash, err := metainfo.HashFrom(wire.InfoHash)
	if err != nil || fileHash != hash {
		return nil, ErrResumeStale
	}
	if wire.TotalLength != mi.TotalLength || wire.NumPieces != mi.NumPieces() {
		return nil, ErrResumeStale
	}

	current := st.Files()
	if len(wire.Files) != len(current) {
		return nil, ErrResumeStale
	}
	for i, recorded := range wire.Files {
		if recorded.Path != current[i].Path {
			return nil, ErrResumeStale
		}
		info, err := os.Stat(recorded.Path)
		if err != nil {
			return nil, ErrResumeStale
		}
		if info.Size() != recorded.Length || info.ModTime().UnixNano() != recorded.ModTime {
			return nil, ErrResumeStale
		}
	}

	rd := &resumeData{
		Magic:       resumeMagic,
		Version:     wire.Version,
		InfoHash:    fileHash,
		TotalLength: wire.TotalLength,
		PieceBits:   wire.PieceBits,
		NumPieces:   wire.NumPieces,
		Files:       wire.Files,
		Downloaded:  wire.Downloaded,
		Uploaded:    wire.Uploaded,
		SavedAtRFC:  wire.SavedAt,
	}
	return rd, nil
}

// removeResume deletes resume data for a torrent, used when it is removed
// entirely rather than just paused.
func removeResume(dir string, hash metainfo.Hash) error {
	err := os.Remove(resumePath(dir, hash))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
