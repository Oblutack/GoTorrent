package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/storage"
)

// runVerify implements `gottrent verify`: a parallel re-hash of a torrent's
// data already on disk against its .torrent file, with a live progress
// line — for checking a download's integrity without starting the whole
// client, or after moving/restoring files by hand.
func runVerify(args []string) {
	fs := flag.NewFlagSet("gottrent verify", flag.ExitOnError)
	dir := fs.String("dir", ".", "Directory holding the torrent's data")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: gottrent verify [-dir <download_directory>] <path_to_torrent_file>")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}

	mi, err := metainfo.Load(fs.Arg(0))
	if err != nil {
		logger.Error.Fatalf("Error loading %s: %v\n", fs.Arg(0), err)
	}

	st, err := storage.New(*dir, mi)
	if err != nil {
		logger.Error.Fatalf("Error opening %s: %v\n", *dir, err)
	}
	defer st.Close()

	start := time.Now()
	result, err := st.Verify(context.Background(), mi, storage.VerifyOptions{
		OnProgress: func(done, total int) {
			fmt.Printf("\rVerifying: %d/%d pieces (%.1f%%)", done, total, float64(done)/float64(total)*100)
		},
	})
	fmt.Println()
	if err != nil {
		logger.Error.Fatalf("Error verifying: %v\n", err)
	}

	fmt.Printf("%d/%d pieces OK (%.1f%%) in %s\n",
		result.Complete, result.Total, float64(result.Complete)/float64(result.Total)*100, time.Since(start).Round(time.Millisecond))
	if result.Complete != result.Total {
		os.Exit(1)
	}
}
