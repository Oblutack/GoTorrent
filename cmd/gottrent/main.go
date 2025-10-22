package main

import (
	"flag"
	"log"
	
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/session"
)

func main() {
	torrentFilePath := flag.String("torrent", "", "Path to the .torrent file")
	listenPort := flag.Uint("port", 6881, "Port number for incoming peer connections")
	downloadDir := flag.String("dir", ".", "Directory to save downloaded files")
	flag.Parse()

	if *torrentFilePath == "" {
		log.Println("Usage: gottrent -torrent <path_to_torrent_file> [-port <listen_port>] [-dir <download_directory>]")
		flag.PrintDefaults()
		return
	}

	
	log.Printf("Loading torrent file: %s\n", *torrentFilePath)
	metaInfo, err := metainfo.LoadFromFile(*torrentFilePath)
	if err != nil {
		log.Fatalf("Error loading torrent file: %v\n", err)
	}

	
	torrentSession, err := session.New(metaInfo, uint16(*listenPort), *downloadDir)
	if err != nil {
		log.Fatalf("Error creating torrent session: %v\n", err)
	}

	
	
	if err := torrentSession.Run(); err != nil {
		log.Fatalf("Torrent session run failed: %v", err)
	}

	log.Println("GoTorrent finished.")
}