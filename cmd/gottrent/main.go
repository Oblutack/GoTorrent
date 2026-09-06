// Command gottrent is a CLI fleet manager: point it at one or more .torrent
// files and it downloads (or seeds, if the data is already complete) all of
// them until Ctrl-C. Torrents added in a previous run are picked back up
// automatically from the engine's manifest.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/ratelimit"
)

// torrentSources collects a flag that may be repeated, one -torrent per
// .torrent file or magnet: URI.
type torrentSources []string

func (p *torrentSources) String() string     { return strings.Join(*p, ",") }
func (p *torrentSources) Set(v string) error { *p = append(*p, v); return nil }

func main() {
	var sources torrentSources
	flag.Var(&sources, "torrent", "A .torrent file path or a magnet: URI (repeat for multiple torrents)")
	downloadDir := flag.String("dir", ".", "Default directory to save downloaded files")
	stateDir := flag.String("state-dir", "", "Directory for the fleet manifest (default: a directory under the OS config dir)")
	listenPort := flag.Uint("port", 6881, "Port advertised to trackers (no inbound listener yet)")
	downLimitKB := flag.Uint("down-limit", 0, "Download rate cap in KiB/s across the whole fleet (0 = unlimited)")
	upLimitKB := flag.Uint("up-limit", 0, "Upload rate cap in KiB/s across the whole fleet (0 = unlimited)")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	flag.Parse()

	logger.Init(*verbose)

	dir := *stateDir
	if dir == "" {
		d, err := engine.DefaultStateDir()
		if err != nil {
			logger.Error.Fatalf("Error resolving state directory: %v\n", err)
		}
		dir = d
	}

	defaults := engine.Defaults{DownloadDir: *downloadDir, ListenPort: uint16(*listenPort)}
	if *downLimitKB > 0 {
		defaults.DownLimit = ratelimit.New(int64(*downLimitKB) * 1024)
	}
	if *upLimitKB > 0 {
		defaults.UpLimit = ratelimit.New(int64(*upLimitKB) * 1024)
	}

	e, err := engine.New(dir, defaults)
	if err != nil {
		logger.Error.Fatalf("Error creating engine: %v\n", err)
	}
	if err := e.Load(); err != nil {
		logger.Error.Fatalf("Error loading fleet manifest: %v\n", err)
	}

	for _, src := range sources {
		if _, err := e.Add(src, ""); err != nil {
			logger.Warning.Printf("Could not add %s: %v\n", src, err)
		}
	}

	if len(e.List()) == 0 {
		fmt.Println("Usage: gottrent -torrent <path_to_torrent_file | magnet_uri> [-torrent <another> ...] [-dir <download_directory>] [-port <listen_port>]")
		flag.PrintDefaults()
		return
	}

	// Ctrl-C (and SIGTERM) triggers a graceful shutdown of the whole fleet:
	// every torrent saves a final checkpoint, tells its tracker it is
	// stopping, and disconnects its peers before Shutdown returns. No
	// os.Exit here — main just falls off the end once everything has
	// actually stopped.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	shutdownDone := make(chan struct{})
	go func() {
		<-sigCh
		fmt.Println("\nShutdown signal received, saving state and disconnecting...")
		e.Shutdown()
		close(shutdownDone)
	}()

	displayFleet(e, shutdownDone)

	logger.Logf("GoTorrent finished.\n")
}

// displayFleet prints one self-overwriting status line per managed torrent
// until shutdownDone closes.
func displayFleet(e *engine.Engine, shutdownDone <-chan struct{}) {
	fmt.Print("\033[?25l")       // hide cursor
	defer fmt.Print("\033[?25h") // restore it on the way out

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	lastBytes := make(map[metainfo.Hash]int64)
	lastTime := time.Now()
	linesDrawn := 0

	render := func() {
		list := e.List()
		now := time.Now()
		elapsed := now.Sub(lastTime).Seconds()

		if linesDrawn > 0 {
			fmt.Printf("\033[%dA", linesDrawn)
		}
		for _, s := range list {
			var speed float64
			if elapsed > 0.1 {
				speed = float64(s.Stats.Downloaded-lastBytes[s.InfoHash]) / elapsed
			}
			lastBytes[s.InfoHash] = s.Stats.Downloaded

			percent := 0.0
			if s.Stats.TotalLength > 0 {
				percent = float64(s.Stats.Downloaded) / float64(s.Stats.TotalLength) * 100
			}

			name := s.Name
			if len(name) > 24 {
				name = name[:21] + "..."
			}

			fmt.Printf("%-24s %-16s %6.2f%% %6.2f/%6.2f MB %s peers:%-3d\033[K\n",
				name,
				s.Stats.State,
				percent,
				float64(s.Stats.Downloaded)/(1024*1024),
				float64(s.Stats.TotalLength)/(1024*1024),
				formatSpeed(speed),
				s.Stats.PeerCount,
			)
		}
		lastTime = now
		linesDrawn = len(list)
	}

	for {
		select {
		case <-shutdownDone:
			return
		case <-ticker.C:
			render()
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
