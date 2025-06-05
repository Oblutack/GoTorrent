package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"strings"

	// "os" // Za os.PathSeparator ako ga koristimo u ispisu fajlova

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer" // Uključuje i tipove poruka
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// Helper function to find minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

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

	// Print MetaInfo
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
	numPiecesInTorrent := len(metaInfo.PieceHashes)
	fmt.Printf("Number of Pieces: %d\n", numPiecesInTorrent)
	if metaInfo.Info.Private == 1 {
		fmt.Println("Private Torrent: Yes")
	}

	if len(metaInfo.Info.Files) > 0 {
		fmt.Println("Files:")
		for i, file := range metaInfo.Info.Files {
			// Za lepši ispis putanje: import "os"; import "path/filepath"; fmt.Println(filepath.Join(file.Path...))
			fmt.Printf("  %d. Path: %v, Length: %d bytes\n", i+1, file.Path, file.Length)
		}
	} else if metaInfo.Info.Length > 0 {
		fmt.Printf("Single File Length: %d bytes\n", metaInfo.Info.Length)
	}
	fmt.Println("-----------------------------------------------------")

	// Tracker Communication
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
		NumWant:    50,
	}

	var httpAnnounceURLs []string
	if metaInfo.Announce != "" && (strings.HasPrefix(metaInfo.Announce, "http://") || strings.HasPrefix(metaInfo.Announce, "https://")) {
		httpAnnounceURLs = append(httpAnnounceURLs, metaInfo.Announce)
	}
	for _, tier := range metaInfo.AnnounceList {
		for _, trackerURL := range tier {
			if strings.HasPrefix(trackerURL, "http://") || strings.HasPrefix(trackerURL, "https://") {
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
		currentResponse, errAnnounce := tracker.Announce(announceURL, trackerReq)
		if errAnnounce != nil {
			log.Printf("Warning: Failed to announce to %s: %v\n", announceURL, errAnnounce)
			lastAnnounceErr = errAnnounce
			continue
		}
		if currentResponse.FailureReason != "" {
			log.Printf("Tracker at %s returned failure: %s\n", announceURL, currentResponse.FailureReason)
			lastAnnounceErr = fmt.Errorf("tracker failure at %s: %s", announceURL, currentResponse.FailureReason)
			trackerResponse = nil
			continue
		}
		trackerResponse = currentResponse
		successfulAnnounceURL = announceURL
		break
	}

	if trackerResponse == nil {
		log.Fatalf("Failed to announce to any available HTTP/HTTPS tracker. Last error: %v\n", lastAnnounceErr)
	}
	log.Printf("Successfully received response from: %s\n", successfulAnnounceURL)

	// Print Tracker Response
	fmt.Println("-----------------------------------------------------")
	fmt.Println("Tracker Response:")
	fmt.Println("-----------------------------------------------------")
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
		peerInfo := trackerResponse.Peers[i]
		fmt.Printf("  - Peer %d: IP: %s, Port: %d\n", i+1, peerInfo.IP.String(), peerInfo.Port)
	}
	if len(trackerResponse.Peers) > maxPeersToShow {
		fmt.Printf("  ... and %d more peers.\n", len(trackerResponse.Peers)-maxPeersToShow)
	}
	fmt.Println("-----------------------------------------------------")

	// Peer Communication
	if trackerResponse == nil || len(trackerResponse.Peers) == 0 {
		log.Println("No peers received from tracker. Exiting.")
		return
	}

	firstPeerInfo := trackerResponse.Peers[0]
	log.Printf("Attempting to connect and handshake with peer: %s:%d\n",
		firstPeerInfo.IP.String(), firstPeerInfo.Port)

	peerClient, err := peer.NewClient(firstPeerInfo, metaInfo.InfoHash, peerID, numPiecesInTorrent)
	if err != nil {
		log.Printf("Failed to connect or handshake with peer %s:%d: %v\n",
			firstPeerInfo.IP.String(), firstPeerInfo.Port, err)
		return
	}
	defer peerClient.Close()

	log.Printf("Successfully connected and handshaked with peer! Remote Peer ID: %x\n", peerClient.RemoteID)
	fmt.Println("-----------------------------------------------------")

	log.Println("Sending Interested message to peer...")
	err = peerClient.SendInterested()
	if err != nil {
		log.Fatalf("Failed to send Interested message: %v\n", err)
	}
	log.Println("Interested message sent.")

	log.Println("Entering message loop with peer...")

	ourBitfield := peer.NewBitfield(numPiecesInTorrent) // Our record of what we have
	// TODO: Later, load this from a saved state if resuming.

	// Map to track pieces requested from this peer to avoid duplicate requests in this simple loop
	// Key: pieceIndex, Value: true if block 0 of this piece has been requested
	// This is a very basic mechanism for now.
	requestedPiecesFromThisPeer := make(map[uint32]bool)

	var canRequestPieces bool // True if peer has unchoked us

	for i := 0; i < 30; i++ { // Increased loop count to catch more messages, adjust as needed
		log.Printf("--- Waiting for message %d from peer ---", i+1)
		msg, err := peerClient.ReadMessage()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				log.Println("Timeout reading message from peer.")
				break
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				log.Println("Peer closed the connection.")
				break
			}
			log.Printf("Error reading message from peer: %v. Exiting message loop.", err)
			break
		}

		if msg == nil { // Keep-alive
			log.Println("Received keep-alive from peer.")
			continue
		}

		payloadPreviewLen := min(16, len(msg.Payload))
		log.Printf("Received message from peer: ID: %s, Payload Length: %d, Payload (hex preview): %x\n",
			msg.ID, len(msg.Payload), msg.Payload[:payloadPreviewLen])

		switch msg.ID {
		case peer.MsgBitfield:
			if peerClient.PeerBitfield != nil {
				expectedBitfieldLen := (peerClient.NumPiecesInTorrent + 7) / 8
				if len(msg.Payload) == expectedBitfieldLen {
					copy(peerClient.PeerBitfield, msg.Payload) // Copy payload to our client's view of peer's bitfield
					log.Printf("Stored Bitfield from peer. Peer has piece 0: %t", peerClient.PeerBitfield.HasPiece(0))
				} else {
					log.Printf("Warning: Received Bitfield has incorrect length. Got %d, expected %d for %d pieces. Ignoring. Payload: %x",
						len(msg.Payload), expectedBitfieldLen, peerClient.NumPiecesInTorrent, msg.Payload)
				}
			} else {
				log.Println("Warning: Received Bitfield, but peerClient.PeerBitfield was nil (should have been initialized in NewClient).")
			}
		case peer.MsgChoke:
			log.Println("Peer choked us.")
			canRequestPieces = false
		case peer.MsgUnchoke:
			log.Println("Peer unchoked us! We can now request pieces.")
			canRequestPieces = true
		case peer.MsgHave:
			var havePayload peer.MsgHavePayload
			if err := havePayload.Parse(msg.Payload); err == nil {
				log.Printf("Received Have message for piece index: %d", havePayload.PieceIndex)
				if peerClient.PeerBitfield != nil {
					if int(havePayload.PieceIndex) < peerClient.NumPiecesInTorrent { // Bounds check
						peerClient.PeerBitfield.SetPiece(havePayload.PieceIndex)
						log.Printf("Updated peer bitfield for piece %d. Peer now has piece 0: %t",
							havePayload.PieceIndex, peerClient.PeerBitfield.HasPiece(0))
					} else {
						log.Printf("Warning: Received Have for out-of-bounds piece index %d (total pieces %d)",
							havePayload.PieceIndex, peerClient.NumPiecesInTorrent)
					}
				} else {
					log.Println("Warning: Received Have message, but peer bitfield is not yet initialized.")
				}
			} else {
				log.Printf("Error parsing Have message payload: %v", err)
			}
		case peer.MsgPiece:
			var piecePayload peer.MsgPiecePayload
			if err := piecePayload.Parse(msg.Payload); err == nil {
				log.Printf(">>> RECEIVED PIECE! Index: %d, Begin: %d, Block Length: %d",
					piecePayload.Index, piecePayload.Begin, len(piecePayload.Block))
				// TODO: Store this block
				// TODO: Mark this block as received in our download state
				// TODO: If piece is complete, verify hash
				// TODO: If piece is verified, update ourBitfield.SetPiece(piecePayload.Index)
				// TODO: Request next block or piece
				
				// For now, to stop requesting this piece from this peer again in this simple loop:
				requestedPiecesFromThisPeer[piecePayload.Index] = true 
			} else {
				log.Printf("Error parsing Piece message payload: %v", err)
			}
		default:
			log.Printf("Received unhandled message ID: %s\n", msg.ID)
		}

		// Logic for sending a Request message
		if canRequestPieces && peerClient.PeerBitfield != nil {
			// Find first piece that peer has, we don't have, and we haven't requested yet from this peer
			for pieceIdx := 0; pieceIdx < peerClient.NumPiecesInTorrent; pieceIdx++ {
				currentPieceIndex := uint32(pieceIdx)

				if !ourBitfield.HasPiece(currentPieceIndex) &&
					peerClient.PeerBitfield.HasPiece(currentPieceIndex) &&
					!requestedPiecesFromThisPeer[currentPieceIndex] {

					log.Printf("Found piece to request: Index %d (Peer has: Yes, We have: No, Not yet requested from this peer: Yes)", currentPieceIndex)

					blockOffset := uint32(0)    // For simplicity, always request the first block of a piece for now
					blockLength := uint32(16384) // Default 16KB block

					// Determine actual length of the piece we are requesting
					var actualPieceLength int64
					if int(currentPieceIndex) == numPiecesInTorrent-1 { // Is it the last piece?
						actualPieceLength = metaInfo.TotalLength - (int64(numPiecesInTorrent-1) * metaInfo.Info.PieceLength)
						if actualPieceLength <= 0 && metaInfo.TotalLength > 0 { // Should not happen with correct numPieces calc
							actualPieceLength = metaInfo.Info.PieceLength // Fallback, though indicates issue
						}
					} else {
						actualPieceLength = metaInfo.Info.PieceLength
					}
                    // Ensure actualPieceLength is not negative in edge cases of very small torrents
                    if actualPieceLength < 0 { actualPieceLength = 0 }


					// Adjust blockLength if it's larger than the piece itself or remaining part
					if int64(blockOffset)+int64(blockLength) > actualPieceLength {
						if actualPieceLength > int64(blockOffset) {
							blockLength = uint32(actualPieceLength - int64(blockOffset))
						} else {
							blockLength = 0 // Offset is at or beyond the end of this piece
						}
					}
					
					if blockLength > 0 {
						log.Printf("CONDITION MET: Sending Request for piece %d, offset %d, length %d (actual piece len for this piece: %d)",
							currentPieceIndex, blockOffset, blockLength, actualPieceLength)
						reqErr := peerClient.SendRequest(currentPieceIndex, blockOffset, blockLength)
						if reqErr != nil {
							log.Printf("Failed to send Request message for piece %d: %v", currentPieceIndex, reqErr)
						} else {
							log.Printf("Request message sent for piece %d.", currentPieceIndex)
							requestedPiecesFromThisPeer[currentPieceIndex] = true // Mark as requested from this peer
						}
					} else {
						log.Printf("Calculated blockLength for piece %d is 0 or less, not sending Request.", currentPieceIndex)
						requestedPiecesFromThisPeer[currentPieceIndex] = true // Mark as "handled" to avoid re-evaluating
					}
					break // Send only one request per message loop iteration for now
				}
			}
		}
	} // End of message reading loop

	fmt.Println("-----------------------------------------------------")
	log.Println("Finished attempting to read messages. Further P2P communication to be implemented.")
}