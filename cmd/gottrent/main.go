package main

import (
	"flag"
	"log"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/session"
)

func main() {
	torrentFilePath := flag.String("torrent", "", "Path to the .torrent file")
	listenPort := flag.Uint("port", 6881, "Port number for incoming peer connections")
	downloadDir := flag.String("dir", ".", "Directory to save downloaded files")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	flag.Parse()

	logger.Init(*verbose)

	if *torrentFilePath == "" {
		log.Println("Usage: gottrent -torrent <path_to_torrent_file> [-port <listen_port>] [-dir <download_directory>]")
		flag.PrintDefaults()
		return
	}

	logger.Logf("Loading torrent file: %s\n", *torrentFilePath)
	metaInfo, err := metainfo.LoadFromFile(*torrentFilePath)
	if err != nil {
		logger.Error.Fatalf("Error loading torrent file: %v\n", err)
	}

	torrentSession, err := session.New(metaInfo, uint16(*listenPort), *downloadDir)
	if err != nil {
		logger.Error.Fatalf("Error creating torrent session: %v\n", err)
	}

	if err := torrentSession.Run(); err != nil {
		logger.Error.Fatalf("Torrent session run failed: %v", err)
	}

	log.Println("GoTorrent finished.")
}
