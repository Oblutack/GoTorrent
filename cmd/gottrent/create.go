package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// trackerTiers collects repeated -tracker flags, each becoming its own
// announce-list tier (BEP 12) — simple, and still valid: a tool without a
// full tiered-tracker UI putting each tracker in its own tier is normal.
type trackerTiers []string

func (p *trackerTiers) String() string     { return strings.Join(*p, ",") }
func (p *trackerTiers) Set(v string) error { *p = append(*p, v); return nil }

// runCreate implements `gottrent create`.
func runCreate(args []string) {
	fs := flag.NewFlagSet("gottrent create", flag.ExitOnError)
	out := fs.String("out", "", "Path to write the .torrent file (default: <name>.torrent in the current directory)")
	pieceLength := fs.Int64("piece-length", 0, "Piece size in bytes (0 = chosen automatically from the total size)")
	private := fs.Bool("private", false, "Set the private flag (BEP 27): no DHT/PEX/LSD, tracker-only peer discovery")
	comment := fs.String("comment", "", "Optional comment embedded in the torrent")
	createdBy := fs.String("created-by", "gottrent", "Value of the torrent's \"created by\" field")
	var trackers trackerTiers
	fs.Var(&trackers, "tracker", "Announce URL (repeat for multiple trackers, each its own tier)")
	var webSeeds trackerTiers
	fs.Var(&webSeeds, "web-seed", "BEP 19 web seed URL (repeat for multiple)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: gottrent create [flags] <file-or-directory>")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	source := fs.Arg(0)

	info, err := os.Stat(source)
	if err != nil {
		logger.Error.Fatalf("Error reading %s: %v\n", source, err)
	}

	name := filepath.Base(filepath.Clean(source))
	files, err := collectCreateFiles(source, info)
	if err != nil {
		logger.Error.Fatalf("Error: %v\n", err)
	}

	opts := metainfo.CreateOptions{
		Name:        name,
		PieceLength: *pieceLength,
		Private:     *private,
		Comment:     *comment,
		CreatedBy:   *createdBy,
		UrlList:     webSeeds,
		Files:       files,
	}
	if len(trackers) > 0 {
		opts.Announce = trackers[0]
		for _, u := range trackers {
			opts.AnnounceList = append(opts.AnnounceList, []string{u})
		}
	}

	raw, mi, err := metainfo.Build(opts)
	if err != nil {
		logger.Error.Fatalf("Error building torrent: %v\n", err)
	}

	outPath := *out
	if outPath == "" {
		outPath = name + ".torrent"
	}
	if err := os.WriteFile(outPath, raw, 0o644); err != nil {
		logger.Error.Fatalf("Error writing %s: %v\n", outPath, err)
	}

	fmt.Printf("Wrote %s\n", outPath)
	fmt.Printf("  Name:        %s\n", mi.Info.Name)
	fmt.Printf("  Info hash:   %s\n", mi.InfoHash)
	fmt.Printf("  Total size:  %d bytes\n", mi.TotalLength)
	fmt.Printf("  Piece size:  %d bytes (%d pieces)\n", mi.Info.PieceLength, mi.NumPieces())
	fmt.Printf("  Files:       %d\n", len(files))
	fmt.Printf("  Private:     %t\n", mi.Info.Private)
}

// collectCreateFiles builds the file list Build needs, from either a
// single file (a single-file torrent, Path left nil) or a directory
// (walked recursively, each file's Path relative to the directory itself —
// filepath.WalkDir visits entries in lexical order per directory, so this
// is deterministic across runs and platforms without an extra sort).
func collectCreateFiles(source string, info os.FileInfo) ([]metainfo.CreateFile, error) {
	if !info.IsDir() {
		return []metainfo.CreateFile{{SourcePath: source, Length: info.Size()}}, nil
	}

	var files []metainfo.CreateFile
	err := filepath.WalkDir(source, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if verr := metainfo.ValidatePath(parts); verr != nil {
			return fmt.Errorf("%s: %w", rel, verr)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, metainfo.CreateFile{Path: parts, SourcePath: path, Length: fi.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s contains no files", source)
	}
	return files, nil
}
