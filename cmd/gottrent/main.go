// Command gottrent is a CLI fleet manager: point it at one or more .torrent
// files and it downloads (or seeds, if the data is already complete) all of
// them until Ctrl-C. Torrents added in a previous run are picked back up
// automatically from the engine's manifest.
package main

import (
	"context"
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
	"github.com/Oblutack/GoTorrent/internal/picker"
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
	listenPort := flag.Uint("port", 6881, "Port to listen on for inbound peer connections and advertise to trackers")
	randomPort := flag.Bool("random-port", false, "Listen on a random OS-assigned port instead of -port")
	bindAddress := flag.String("bind-address", "", "Local address to listen on for inbound peer connections (default: all interfaces)")
	noPortMap := flag.Bool("no-portmap", false, "Disable automatic UPnP/NAT-PMP port mapping")
	downLimitKB := flag.Uint("down-limit", 0, "Download rate cap in KiB/s across the whole fleet (0 = unlimited)")
	upLimitKB := flag.Uint("up-limit", 0, "Upload rate cap in KiB/s across the whole fleet (0 = unlimited)")
	ratioLimit := flag.Float64("ratio-limit", 0, "Pause a torrent once its upload/download ratio reaches this (0 = unlimited)")
	seedTimeLimit := flag.Duration("seed-time-limit", 0, "Pause a torrent once it has spent this long seeding, e.g. 2h30m (0 = unlimited)")
	sequential := flag.Bool("sequential", false, "Download pieces in order instead of rarest-first (useful for streaming)")
	firstLastPiece := flag.Bool("first-last-piece-first", false, "Fetch each file's first and last piece early, so a partially-downloaded file can be previewed")
	maxActiveDownloads := flag.Int("max-active-downloads", 0, "Maximum torrents actively downloading at once across the fleet (0 = unlimited)")
	maxActiveSeeds := flag.Int("max-active-seeds", 0, "Maximum torrents actively seeding at once across the fleet (0 = unlimited)")
	maxActiveTotal := flag.Int("max-active", 0, "Maximum torrents active (downloading or seeding) at once across the fleet (0 = unlimited)")
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

	defaults := engine.Defaults{
		DownloadDir:         *downloadDir,
		ListenPort:          uint16(*listenPort),
		BindAddress:         *bindAddress,
		SeedRatioLimit:      *ratioLimit,
		SeedTimeLimit:       *seedTimeLimit,
		FirstLastPieceFirst: *firstLastPiece,
		MaxActiveDownloads:  *maxActiveDownloads,
		MaxActiveSeeds:      *maxActiveSeeds,
		MaxActiveTotal:      *maxActiveTotal,
	}
	if *sequential {
		defaults.PickerStrategy = picker.Sequential
	}
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

	// actualPort is what every subsequent StartDHT/StartPortMapping/StartLSD
	// call below (and torrentConfig, on every Add) advertises. It starts as
	// the requested -port and, with -random-port, becomes whatever the OS
	// actually assigned once Listen has bound it.
	actualPort := uint16(*listenPort)
	if *randomPort {
		p, err := e.ListenRandomPort(context.Background())
		if err != nil {
			logger.Warning.Printf("Not accepting inbound connections: %v\n", err)
		} else {
			actualPort = p
			logger.Logf("Listening on random port %d\n", actualPort)
		}
	} else if err := e.Listen(context.Background()); err != nil {
		logger.Warning.Printf("Not accepting inbound connections: %v\n", err)
	}

	if !*noPortMap {
		// Before StartDHT/Load: a successful mapping updates the port every
		// subsequently-built torrent advertises to trackers and DHT peers, so
		// it needs to land before anything reads that value.
		if err := e.StartPortMapping(context.Background(), actualPort); err != nil {
			logger.Logf("Not mapping a port automatically (%v) - inbound connections need the port forwarded by hand unless this machine is already reachable\n", err)
		}
	}
	if err := e.StartDHT(context.Background(), actualPort); err != nil {
		logger.Warning.Printf("Not starting DHT: %v\n", err)
	}
	// LSD always advertises the internal port, never StartPortMapping's
	// rewritten external one - an LSD peer is on the same LAN and connects
	// directly, not through any NAT mapping.
	if err := e.StartLSD(context.Background(), actualPort); err != nil {
		logger.Warning.Printf("Not starting local service discovery: %v\n", err)
	}
	// Reactive queue enforcement is always on (every Add'd torrent's own
	// OnStateChange callback triggers it); this just adds the periodic
	// safety-net pass, and can start any time before or after Load/Add.
	e.StartQueue(context.Background())
	// All three must be up before Load/Add so every torrent - reloaded from a
	// previous run included - gets a working DHT peer source from the start.
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
			// BEP 27: a private torrent never touches DHT/PEX/LSD (enforced
			// where it matters, in internal/torrent and internal/engine);
			// this marker is just so the person running the client can see
			// which of their torrents that applies to.
			private := "  "
			if s.Private {
				private = "P "
			}

			fmt.Printf("%-24s %s%-16s %6.2f%% %6.2f/%6.2f MB %s peers:%-3d ratio:%5.2f\033[K\n",
				name,
				private,
				s.Stats.State,
				percent,
				float64(s.Stats.Downloaded)/(1024*1024),
				float64(s.Stats.TotalLength)/(1024*1024),
				formatSpeed(speed),
				s.Stats.PeerCount,
				s.Stats.SeedRatio,
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
