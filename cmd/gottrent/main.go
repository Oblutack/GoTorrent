package main

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"strings"

	// "os" // For os.PathSeparator if used in file path display

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
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

	ourBitfield := peer.NewBitfield(numPiecesInTorrent)
	// TODO: Load ourBitfield from a saved state if resuming a download.

	var targetPieceIndex uint32
	var currentPieceActualLength int64
	var pieceBuffer []byte
	var pieceRequestedBlocks map[uint32]bool
	var pieceReceivedBlocks map[uint32]bool // Changed to map[uint32]bool to track received blocks by offset
	var pieceIsComplete bool
	var foundPieceToDownload bool
	var nextBlockToRequestOffset uint32 // Tracks the next block offset to request for the current targetPieceIndex

	const defaultBlockLength uint32 = 16384 // 16KB

	var canRequestPieces bool // True if peer has unchoked us

	for i := 0; i < 1000; i++ { // Increased loop substantially to allow for piece download
		log.Printf("--- Waiting for message %d from peer (or to send request) ---", i+1)
		// Set a shorter deadline for this iteration if we are actively downloading a piece
		// to allow our request logic to run more frequently.
		// If not downloading, a longer timeout is fine.
		// This is a simplification; a real client would use non-blocking I/O or select.
		// For now, ReadMessage has its own internal timeout.

		msg, err := peerClient.ReadMessage() // ReadMessage handles its own timeout
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				log.Println("Timeout reading message from peer.")
				// If we were expecting a piece and timed out, it's a problem.
				// For now, we'll just let the request logic below try again if appropriate.
				// If foundPieceToDownload is true, maybe we should retry requesting blocks or consider peer dead.
			} else if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				log.Println("Peer closed the connection.")
				break
			} else {
				log.Printf("Error reading message from peer: %v. Exiting message loop.", err)
				break
			}
			// If it was a timeout, we might still want to try sending a request below.
		}

		if msg != nil { // Process message if not keep-alive and no error
			payloadPreviewLen := min(16, len(msg.Payload))
			log.Printf("Received message from peer: ID: %s, Payload Length: %d, Payload (hex preview): %x\n",
				msg.ID, len(msg.Payload), msg.Payload[:payloadPreviewLen])

			switch msg.ID {
		case peer.MsgBitfield:
			if peerClient.PeerBitfield != nil {
				expectedBitfieldLen := (peerClient.NumPiecesInTorrent + 7) / 8
				if len(msg.Payload) == expectedBitfieldLen {
					bfCopy := make(peer.Bitfield, len(msg.Payload))
					copy(bfCopy, msg.Payload)
					peerClient.PeerBitfield = bfCopy
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
				log.Printf(">>> RECEIVED PIECE! Index: %d, Begin: %d, Block Length: %d\n",
					piecePayload.Index, piecePayload.Begin, len(piecePayload.Block))

				if foundPieceToDownload && piecePayload.Index == targetPieceIndex {
					if pieceBuffer != nil && (int(piecePayload.Begin)+len(piecePayload.Block) <= len(pieceBuffer)) {
						if _, alreadyReceived := pieceReceivedBlocks[piecePayload.Begin]; !alreadyReceived {
							copy(pieceBuffer[piecePayload.Begin:], piecePayload.Block)
							pieceReceivedBlocks[piecePayload.Begin] = true
							log.Printf("Stored block for piece %d at offset %d.\n", targetPieceIndex, piecePayload.Begin)

							allBlocksReceived := true
							if currentPieceActualLength > 0 {
								for checkOffset := uint32(0); int64(checkOffset) < currentPieceActualLength; checkOffset += defaultBlockLength {
									if !pieceReceivedBlocks[checkOffset] {
										allBlocksReceived = false
										break
									}
								}
							} else {
								allBlocksReceived = true // 0-length piece is considered complete
							}

							if allBlocksReceived && !pieceIsComplete { // Added !pieceIsComplete to avoid re-processing
								log.Printf("***** PIECE %d ALL BLOCKS RECEIVED! (%d bytes in buffer) *****\n", targetPieceIndex, len(pieceBuffer))
								
								if int(targetPieceIndex) < len(metaInfo.PieceHashes) {
									expectedHash := metaInfo.PieceHashes[targetPieceIndex]
									actualHash := sha1.Sum(pieceBuffer)

									if bytes.Equal(actualHash[:], expectedHash[:]) {
										log.Printf("========== Piece %d HASH VERIFIED! ==========", targetPieceIndex)
										ourBitfield.SetPiece(targetPieceIndex)
										log.Printf("Marked piece %d as acquired in our bitfield.", targetPieceIndex)
										trackerReq.Downloaded += currentPieceActualLength
										trackerReq.Left -= currentPieceActualLength
										if trackerReq.Left < 0 {
											trackerReq.Left = 0
										}
										log.Printf("Updated downloaded/left: %d/%d", trackerReq.Downloaded, trackerReq.Left)
										// TODO: Save to disk
										log.Printf("TODO: Save piece %d to disk.", targetPieceIndex)
										// TODO: Send HAVE messages
										pieceIsComplete = true // Mark as truly complete and verified
									} else {
										log.Printf("!!!!!!!! Piece %d HASH MISMATCH! Expected %x, got %x. Discarding piece. !!!!!!!!",
											targetPieceIndex, expectedHash, actualHash)
										// Reset for re-download of this piece
										pieceReceivedBlocks = make(map[uint32]bool)
										pieceRequestedBlocks = make(map[uint32]bool) 
										nextBlockToRequestOffset = 0
										// pieceIsComplete remains false
										// foundPieceToDownload remains true so we retry this piece
									}
								} else {
									log.Printf("Error: targetPieceIndex %d is out of bounds for PieceHashes (len %d). Cannot verify.",
										targetPieceIndex, len(metaInfo.PieceHashes))
									pieceIsComplete = true // Mark as complete to avoid retrying invalid index
								}
								
								// If piece processing is finished (verified or failed definitively for this attempt)
								// prepare to look for a new piece.
								if pieceIsComplete || (int(targetPieceIndex) >= len(metaInfo.PieceHashes)) /* Invalid index case */ {
									foundPieceToDownload = false 
									// pieceIsComplete is already true if hash matched, or set true for invalid index.
									// If hash mismatched, pieceIsComplete is false, and foundPieceToDownload is true,
									// so it will try to re-download blocks for the same targetPieceIndex.
									// We need to ensure pieceIsComplete is reset if we are to download a NEW piece.
									if pieceIsComplete { // Only reset pieceIsComplete if we are moving to a new piece
									    pieceIsComplete = false 
                                    }
									log.Println("Will look for a new piece to download (or retry current if hash failed and not marked complete).")
								}
							}
						} else {
							log.Printf("Already received block for piece %d at offset %d. Discarding duplicate.", targetPieceIndex, piecePayload.Begin)
						}
					} else {
						log.Printf("Warning: Received block for piece %d at offset %d with length %d overflows piece buffer (size %d) or buffer not init. Discarding.",
							piecePayload.Index, piecePayload.Begin, len(piecePayload.Block), len(pieceBuffer))
					}
				} else {
					log.Printf("Received piece block for unexpected piece index %d (was targeting %d, or not targeting any piece)\n",
						piecePayload.Index, targetPieceIndex)
				}
			} else {
				log.Printf("Error parsing Piece message payload: %v", err)
			}
		default:
			log.Printf("Received unhandled message ID: %s\n", msg.ID)
		}
		} // End if msg != nil

		// Logic for selecting a piece and sending Request messages
		if canRequestPieces && !pieceIsComplete {
			if !foundPieceToDownload {
				// Find the first piece the peer has that we don't and we haven't completed
				for pieceIdx := 0; pieceIdx < numPiecesInTorrent; pieceIdx++ {
					idx := uint32(pieceIdx)
					if peerClient.PeerBitfield != nil && peerClient.PeerBitfield.HasPiece(idx) && !ourBitfield.HasPiece(idx) {
						targetPieceIndex = idx
						foundPieceToDownload = true

						if int(targetPieceIndex) == numPiecesInTorrent-1 {
							currentPieceActualLength = metaInfo.TotalLength - (int64(numPiecesInTorrent-1) * metaInfo.Info.PieceLength)
						} else {
							currentPieceActualLength = metaInfo.Info.PieceLength
						}
						if currentPieceActualLength < 0 { currentPieceActualLength = 0 }
                        
                        if currentPieceActualLength == 0 { // Skip if piece has 0 length
                            log.Printf("Targeted piece %d has 0 length, skipping.", targetPieceIndex)
                            foundPieceToDownload = false // Reset to find another piece
                            continue // Try next piece in the loop
                        }

						pieceBuffer = make([]byte, currentPieceActualLength)
						pieceRequestedBlocks = make(map[uint32]bool)
						pieceReceivedBlocks = make(map[uint32]bool)
						nextBlockToRequestOffset = 0

						log.Printf("TARGETING PIECE %d for download (length: %d bytes).\n",
							targetPieceIndex, currentPieceActualLength)
						break
					}
				}
				if !foundPieceToDownload && (i%10 == 0) { // Log periodically if no piece found
					log.Println("Still no suitable pieces to request from this peer.")
				}
			}

			if foundPieceToDownload && !pieceIsComplete { // Ensure we have a target and it's not yet complete
				// Request next unrequested block for the targetPieceIndex
				if int64(nextBlockToRequestOffset) < currentPieceActualLength {
					if _, alreadyRequested := pieceRequestedBlocks[nextBlockToRequestOffset]; !alreadyRequested {
						lengthToRequest := defaultBlockLength
						if int64(nextBlockToRequestOffset+lengthToRequest) > currentPieceActualLength {
							lengthToRequest = uint32(currentPieceActualLength - int64(nextBlockToRequestOffset))
						}

						if lengthToRequest > 0 {
							log.Printf("Requesting piece %d, offset %d, length %d\n", targetPieceIndex, nextBlockToRequestOffset, lengthToRequest)
							reqErr := peerClient.SendRequest(targetPieceIndex, nextBlockToRequestOffset, lengthToRequest)
							if reqErr != nil {
								log.Printf("Failed to send Request for piece %d, block offset %d: %v\n", targetPieceIndex, nextBlockToRequestOffset, reqErr)
                                // If send fails, maybe we should break from message loop or try next peer
							} else {
								pieceRequestedBlocks[nextBlockToRequestOffset] = true
								log.Printf("Request sent for piece %d, block offset %d.\n", targetPieceIndex, nextBlockToRequestOffset)
								// Advance to next block offset for the *next time* we consider sending a request
                                // This implements a very simple "request one block at a time"
                                nextBlockToRequestOffset += lengthToRequest 
							}
						} else {
                            // This case means the remaining part of the piece is 0 length, so we are done with requests for this piece.
                            log.Printf("No more data to request for piece %d at offset %d (lengthToRequest is 0).", targetPieceIndex, nextBlockToRequestOffset)
                            // To ensure we check for completion:
                            nextBlockToRequestOffset = uint32(currentPieceActualLength) // Mark as all requested
                        }
					}
                    // If block already requested, we just wait for it or for timeout.
                    // The loop will continue, and ReadMessage will be called again.
				}
                // If nextBlockToRequestOffset >= currentPieceActualLength, all blocks for this piece have been requested.
                // We now wait for Piece messages to arrive and for pieceIsComplete to be set.
			}
		}
	} // End of message reading loop

	fmt.Println("-----------------------------------------------------")
	log.Println("Finished P2P communication loop with this peer.")
}