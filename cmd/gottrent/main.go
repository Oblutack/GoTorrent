// Command gottrent is a one-shot CLI: point it at a .torrent file and it
// downloads (or seeds, if the data is already complete) until Ctrl-C.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

func main() {
	torrentFilePath := flag.String("torrent", "", "Path to the .torrent file")
	listenPort := flag.Uint("port", 6881, "Port advertised to trackers (no inbound listener yet)")
	downloadDir := flag.String("dir", ".", "Directory to save downloaded files")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	flag.Parse()

	logger.Init(*verbose)

	if *torrentFilePath == "" {
		fmt.Println("Usage: gottrent -torrent <path_to_torrent_file> [-port <listen_port>] [-dir <download_directory>]")
		flag.PrintDefaults()
		return
	}

	logger.Logf("Loading torrent file: %s\n", *torrentFilePath)
	mi, err := metainfo.Load(*torrentFilePath)
	if err != nil {
		logger.Error.Fatalf("Error loading torrent file: %v\n", err)
	}

	tr, err := torrent.New(mi, torrent.Config{
		DownloadDir: *downloadDir,
		ListenPort:  uint16(*listenPort),
	})
	if err != nil {
		logger.Error.Fatalf("Error creating torrent: %v\n", err)
	}

	// Ctrl-C (and SIGTERM) triggers a graceful shutdown: cancelling this
	// context makes Run save a final checkpoint, tell the tracker we're
	// stopping, and disconnect every peer before returning. No os.Exit here —
	// main just falls off the end once everything has actually stopped.
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutdown signal received, saving state and disconnecting...")
		cancel()
	}()

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := tr.Run(ctx); err != nil {
			logger.Error.Printf("Torrent run failed: %v\n", err)
		}
	}()

	displayProgress(tr, runDone)

	logger.Logf("GoTorrent finished.\n")
}

// displayProgress prints a single self-overwriting status line, matching the
// old session's displayLoop, until the torrent finishes or Run returns.
func displayProgress(tr *torrent.Torrent, runDone <-chan struct{}) {
	fmt.Print("\033[?25l")       // hide cursor
	defer fmt.Print("\033[?25h") // restore it on the way out

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastBytes int64
	lastTime := time.Now()
	announcedDone := false

	for {
		select {
		case <-runDone:
			fmt.Println()
			return
		case now := <-ticker.C:
			stats := tr.Stats()

			elapsed := now.Sub(lastTime).Seconds()
			var speed float64
			if elapsed > 0.1 {
				speed = float64(stats.Downloaded-lastBytes) / elapsed
			}
			lastBytes = stats.Downloaded
			lastTime = now

			percent := 0.0
			if stats.TotalLength > 0 {
				percent = float64(stats.Downloaded) / float64(stats.TotalLength) * 100
			}

			fmt.Printf("\rState: %-16s | Progress: %6.2f%% | %.2f/%.2f MB | %s | Peers: %d \033[K",
				stats.State,
				percent,
				float64(stats.Downloaded)/(1024*1024),
				float64(stats.TotalLength)/(1024*1024),
				formatSpeed(speed),
				stats.PeerCount,
			)

			if stats.State == torrent.StateSeeding && !announcedDone {
				fmt.Println()
				fmt.Println("Download complete. Seeding — press Ctrl-C to stop.")
				announcedDone = true
			}
			if stats.State == torrent.StateError {
				fmt.Println()
				fmt.Println("Torrent stopped due to an unrecoverable error; see -verbose output.")
				return
			}
		}
	}
}

func formatSpeed(bytesPerSec float64) string {
	switch {
	case bytesPerSec > 1024*1024:
		return fmt.Sprintf("%6.2f MB/s", bytesPerSec/(1024*1024))
	case bytesPerSec > 1024:
		return fmt.Sprintf("%6.2f KB/s", bytesPerSec/1024)
	default:
		return fmt.Sprintf("%6.2f B/s", bytesPerSec)
	}
}
