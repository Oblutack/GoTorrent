package main

import (
	"flag" // For parsing command-line arguments
	"fmt"
	"log" // For fatal errors

	"github.com/Oblutack/GoTorrent/internal/metainfo" // Import our metainfo package
)

func main() {
	// Define a string flag for the torrent file path
	// -torrent <path>
	torrentFilePath := flag.String("torrent", "", "Path to the .torrent file")
	flag.Parse() // Parse the command-line flags

	if *torrentFilePath == "" {
		log.Println("Usage: gottrent -torrent <path_to_torrent_file>")
		flag.PrintDefaults() // Print default usage information
		return
	}

	log.Printf("Loading torrent file: %s\n", *torrentFilePath)

	metaInfo, err := metainfo.LoadFromFile(*torrentFilePath)
	if err != nil {
		log.Fatalf("Error loading torrent file: %v\n", err)
	}

	// Print some basic information from the parsed MetaInfo
	fmt.Println("-----------------------------------------------------")
	fmt.Println("Torrent MetaInfo:")
	fmt.Println("-----------------------------------------------------")
	fmt.Printf("Announce URL: %s\n", metaInfo.Announce)

	if len(metaInfo.AnnounceList) > 0 {
		fmt.Println("Announce List:")
		for i, tier := range metaInfo.AnnounceList {
			fmt.Printf("  Tier %d:\n", i+1)
			for _, trackerURL := range tier {
				fmt.Printf("    - %s\n", trackerURL)
			}
		}
	}

	if metaInfo.Comment != "" {
		fmt.Printf("Comment: %s\n", metaInfo.Comment)
	}
	if metaInfo.CreatedBy != "" {
		fmt.Printf("Created By: %s\n", metaInfo.CreatedBy)
	}
	if metaInfo.CreationDate > 0 {
		// TODO: Format timestamp to human-readable date
		fmt.Printf("Creation Date (Unix): %d\n", metaInfo.CreationDate)
	}

	// InfoHash is currently all zeros, but let's print it anyway
	fmt.Printf("InfoHash (hex): %x\n", metaInfo.InfoHash) // %x for hex representation of byte array

	fmt.Printf("Torrent Name: %s\n", metaInfo.Info.Name)
	fmt.Printf("Piece Length: %d bytes\n", metaInfo.Info.PieceLength)
	fmt.Printf("Total Length: %d bytes\n", metaInfo.TotalLength)
	fmt.Printf("Number of Pieces: %d\n", len(metaInfo.PieceHashes))
	if metaInfo.Info.Private == 1 {
		fmt.Println("Private Torrent: Yes")
	}

	if len(metaInfo.Info.Files) > 0 {
		fmt.Println("Files:")
		for i, file := range metaInfo.Info.Files {
			fmt.Printf("  %d. Path: %s, Length: %d bytes\n", i+1, file.Path, file.Length) // TODO: Join file.Path
		}
	} else {
		fmt.Printf("Single File Length: %d bytes\n", metaInfo.Info.Length)
	}
	fmt.Println("-----------------------------------------------------")

	// TODO: Further steps - connect to tracker, peers, etc.
}