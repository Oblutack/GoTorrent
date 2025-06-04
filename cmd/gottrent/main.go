package main

import (
	"flag"
	"fmt"
	"log"
	"strings" // Potreban za strings.HasPrefix

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

func main() {
	torrentFilePath := flag.String("torrent", "", "Path to the .torrent file")
	listenPort := flag.Uint("port", 6881, "Port number for incoming peer connections")
	flag.Parse()

	if *torrentFilePath == "" {
		log.Println("Usage: gottrent -torrent <path_to_torrent_file> [-port <listen_port>]")
		flag.PrintDefaults()
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

	fmt.Printf("InfoHash (hex): %x\n", metaInfo.InfoHash)
	fmt.Printf("Torrent Name: %s\n", metaInfo.Info.Name)
	fmt.Printf("Piece Length: %d bytes\n", metaInfo.Info.PieceLength)
	fmt.Printf("Total Length: %d bytes\n", metaInfo.TotalLength)
	fmt.Printf("Number of Pieces: %d\n", len(metaInfo.PieceHashes))
	if metaInfo.Info.Private == 1 {
		fmt.Println("Private Torrent: Yes")
	}

	if len(metaInfo.Info.Files) > 0 {
		fmt.Println("Files:")
		// TODO: Proper path joining for display
		// currentPath := "" // if multi-file and Name is a directory
		for i, file := range metaInfo.Info.Files {
			// This will just print the array of path segments.
			// For a nice display, you'd join them with os.PathSeparator.
			// Example: strings.Join(file.Path, string(os.PathSeparator))
			fmt.Printf("  %d. Path: %v, Length: %d bytes\n", i+1, file.Path, file.Length)
		}
	} else if metaInfo.Info.Length > 0 { // Ensure it's a single file with positive length
		fmt.Printf("Single File Length: %d bytes\n", metaInfo.Info.Length)
	}
	fmt.Println("-----------------------------------------------------")

	// ==============================================================
	// KORAK 2: KOMUNIKACIJA SA TRACKEROM
	// ==============================================================
	log.Println("Attempting to announce to tracker(s)...")

	peerID, err := tracker.GeneratePeerID()
	if err != nil {
		log.Fatalf("Error generating Peer ID: %v\n", err)
	}
	log.Printf("Generated Peer ID (first 8 chars): %s (hex: %x)\n", string(peerID[:8]), peerID)


	trackerReq := tracker.TrackerRequest{
		InfoHash:   metaInfo.InfoHash,
		PeerID:     peerID,
		Port:       uint16(*listenPort),
		Uploaded:   0,
		Downloaded: 0,
		Left:       metaInfo.TotalLength,
		Compact:    1,
		Event:      "started",
		NumWant:    50, // Request around 50 peers
	}

	// Collect all potential HTTP/HTTPS announce URLs
	var httpAnnounceURLs []string
	if metaInfo.Announce != "" && (strings.HasPrefix(metaInfo.Announce, "http://") || strings.HasPrefix(metaInfo.Announce, "https://")) {
		httpAnnounceURLs = append(httpAnnounceURLs, metaInfo.Announce)
	}
	for _, tier := range metaInfo.AnnounceList {
		for _, trackerURL := range tier {
			if strings.HasPrefix(trackerURL, "http://") || strings.HasPrefix(trackerURL, "https://") {
				// Avoid duplicates if Announce is also in AnnounceList
				isDuplicate := false
				for _, existingURL := range httpAnnounceURLs {
					if existingURL == trackerURL {
						isDuplicate = true
						break
					}
				}
				if !isDuplicate {
					httpAnnounceURLs = append(httpAnnounceURLs, trackerURL)
				}
			} else {
				log.Printf("Skipping non-HTTP(S) tracker: %s\n", trackerURL)
			}
		}
	}

	if len(httpAnnounceURLs) == 0 {
		log.Fatalf("No HTTP/HTTPS tracker announce URLs found in torrent file. UDP trackers are not yet supported.")
	}

	var trackerResponse *tracker.TrackerResponse
	var successfulAnnounceURL string
	var lastAnnounceErr error

	for _, announceURL := range httpAnnounceURLs {
		log.Printf("Announcing to: %s\n", announceURL)
		currentResponse, err := tracker.Announce(announceURL, trackerReq)
		if err != nil {
			log.Printf("Warning: Failed to announce to %s: %v\n", announceURL, err)
			lastAnnounceErr = err // Save the last error
			continue              // Try the next tracker
		}
		
		// If tracker returns a failure reason, it's still a "successful" HTTP communication
		// but a logical failure from the tracker's perspective.
		if currentResponse.FailureReason != "" {
			log.Printf("Tracker at %s returned failure: %s\n", announceURL, currentResponse.FailureReason)
            lastAnnounceErr = fmt.Errorf("tracker failure at %s: %s", announceURL, currentResponse.FailureReason)
			// We might still want to try other trackers if one explicitly fails.
            // For now, let's treat this as a reason to try the next one.
            // If all trackers return failure reasons, we'll report the last one.
            trackerResponse = nil // Ensure we don't use a failed response
			continue
		}
		
		trackerResponse = currentResponse
		successfulAnnounceURL = announceURL
		break // Successfully announced (or got a non-HTTP error response like 'failure reason')
	}

	if trackerResponse == nil {
		log.Fatalf("Failed to announce to any available HTTP/HTTPS tracker. Last error: %v\n", lastAnnounceErr)
	}
	log.Printf("Successfully received response from: %s\n", successfulAnnounceURL)


	// Print tracker response
	fmt.Println("-----------------------------------------------------")
	fmt.Println("Tracker Response:")
	fmt.Println("-----------------------------------------------------")
	// FailureReason should have been handled above, but double check
	if trackerResponse.FailureReason != "" {
		fmt.Printf("Tracker Failure: %s\n", trackerResponse.FailureReason)
		return
	}
	if trackerResponse.WarningMessage != "" {
		fmt.Printf("Tracker Warning: %s\n", trackerResponse.WarningMessage)
	}
	fmt.Printf("Interval: %d seconds\n", trackerResponse.Interval)
	if trackerResponse.MinInterval > 0 {
		fmt.Printf("Min Interval: %d seconds\n", trackerResponse.MinInterval)
	}
	if trackerResponse.TrackerID != "" {
		fmt.Printf("Tracker ID: %s\n", trackerResponse.TrackerID)
	}
	fmt.Printf("Seeders (Complete): %d\n", trackerResponse.Complete)
	fmt.Printf("Leechers (Incomplete): %d\n", trackerResponse.Incomplete)

	fmt.Printf("Received %d peers:\n", len(trackerResponse.Peers))
	maxPeersToShow := 10
	if len(trackerResponse.Peers) < maxPeersToShow {
		maxPeersToShow = len(trackerResponse.Peers)
	}
	for i := 0; i < maxPeersToShow; i++ {
		peer := trackerResponse.Peers[i]
		fmt.Printf("  - Peer %d: IP: %s, Port: %d\n", i+1, peer.IP.String(), peer.Port)
	}
	if len(trackerResponse.Peers) > maxPeersToShow {
		fmt.Printf("  ... and %d more peers.\n", len(trackerResponse.Peers)-maxPeersToShow)
	}
	fmt.Println("-----------------------------------------------------")

	// TODO: Next step - connect to peers
}