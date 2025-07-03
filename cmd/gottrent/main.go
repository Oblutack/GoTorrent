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
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)
 
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

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

	log.Printf("Preparing download directory: %s\n", *downloadDir)
	if err := os.MkdirAll(*downloadDir, 0755); err != nil {
		log.Fatalf("Failed to create download directory %s: %v", *downloadDir, err)
	}

	if len(metaInfo.Info.Files) > 0 {
		torrentBaseDir := filepath.Join(*downloadDir, metaInfo.Info.Name)
		log.Printf("Multi-file torrent. Base directory: %s\n", torrentBaseDir)
		if err := os.MkdirAll(torrentBaseDir, 0755); err != nil {
			log.Fatalf("Failed to create base torrent directory %s: %v", torrentBaseDir, err)
		}
		for _, fileInfo := range metaInfo.Info.Files {
			pathParts := make([]string, 0, len(fileInfo.Path)+1)
			pathParts = append(pathParts, torrentBaseDir)
			pathParts = append(pathParts, fileInfo.Path...)
			fullFilePath := filepath.Join(pathParts...)
			if err := os.MkdirAll(filepath.Dir(fullFilePath), 0755); err != nil {
				log.Fatalf("Failed to create subdirectory for %s: %v", fullFilePath, err)
			}
			log.Printf("Pre-allocating file: %s (size: %d bytes)\n", fullFilePath, fileInfo.Length)
			file, errFile := os.OpenFile(fullFilePath, os.O_CREATE|os.O_RDWR, 0644)
			if errFile != nil {
				log.Fatalf("Failed to create/open file %s: %v", fullFilePath, errFile)
			}
			if errTrunc := file.Truncate(fileInfo.Length); errTrunc != nil {
				file.Close()
				log.Fatalf("Failed to truncate file %s to size %d: %v", fullFilePath, fileInfo.Length, errTrunc)
			}
			if errClose := file.Close(); errClose != nil {
				log.Fatalf("Failed to close file %s after truncation: %v", fullFilePath, errClose)
			}
		}
	} else {
		fullFilePath := filepath.Join(*downloadDir, metaInfo.Info.Name)
		log.Printf("Single-file torrent. File: %s (size: %d bytes)\n", fullFilePath, metaInfo.Info.Length)
		file, errFile := os.OpenFile(fullFilePath, os.O_CREATE|os.O_RDWR, 0644)
		if errFile != nil {
			log.Fatalf("Failed to create/open file %s: %v", fullFilePath, errFile)
		}
		if errTrunc := file.Truncate(metaInfo.Info.Length); errTrunc != nil {
			file.Close()
			log.Fatalf("Failed to truncate file %s to size %d: %v", fullFilePath, metaInfo.Info.Length, errTrunc)
		}
		if errClose := file.Close(); errClose != nil {
			log.Fatalf("Failed to close file %s after truncation: %v", fullFilePath, errClose)
		}
	}
	log.Println("File pre-allocation complete.")
	fmt.Println("-----------------------------------------------------")

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

	if trackerResponse == nil || len(trackerResponse.Peers) == 0 {
		log.Println("No peers received from tracker. Exiting.")
		return
	}

	var successfulPeerClient *peer.Client 
	var peerConnectErr error

	for _, peerInfo := range trackerResponse.Peers {
		log.Printf("Attempting to connect and handshake with peer: %s\n", net.JoinHostPort(peerInfo.IP.String(), strconv.Itoa(int(peerInfo.Port))))

		client, err := peer.NewClient(peerInfo, metaInfo.InfoHash, peerID, numPiecesInTorrent)
		if err != nil {
			log.Printf("Warning: Failed to connect or handshake with peer %s: %v\n", peerInfo.IP.String(), err)
			peerConnectErr = err 
			continue 
		}

		successfulPeerClient = client
		log.Printf("Successfully established connection with peer: %s\n", successfulPeerClient.Conn.RemoteAddr())
		break 
	}

	if successfulPeerClient == nil {
		log.Fatalf("Failed to connect to any of the %d peers received from the tracker. Last error: %v",
			len(trackerResponse.Peers), peerConnectErr)
	}
	defer successfulPeerClient.Close()

	log.Printf("Successfully connected and handshaked with peer! Remote Peer ID: %x\n", successfulPeerClient.RemoteID)
	fmt.Println("-----------------------------------------------------")

	log.Println("Sending Interested message to peer...")
	err = successfulPeerClient.SendInterested()
	if err != nil {
		log.Fatalf("Failed to send Interested message: %v\n", err)
	}
	log.Println("Interested message sent.")
	log.Println("Entering message loop with peer...")

	ourBitfield := peer.NewBitfield(numPiecesInTorrent)
	var targetPieceIndex uint32
	var currentPieceActualLength int64
	var pieceBuffer []byte
	var pieceRequestedBlocks map[uint32]bool
	var pieceReceivedBlocks map[uint32]bool
	var pieceIsComplete bool
	var foundPieceToDownload bool
	var nextBlockToRequestOffset uint32
	const defaultBlockLength uint32 = 16384
	var canRequestPieces bool

	for i := 0; i < 1000; i++ {
		log.Printf("--- Waiting for message %d from peer (or to send request) ---", i+1)
		msg, err := successfulPeerClient.ReadMessage()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				log.Println("Timeout reading message from peer.")
			} else if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				log.Println("Peer closed the connection.")
				break
			} else {
				log.Printf("Error reading message from peer: %v. Exiting P2P loop.", err)
				break
			}
		}

		if msg != nil {
			payloadPreviewLen := min(16, len(msg.Payload))
			log.Printf("Received message from peer: ID: %s, Payload Length: %d, Payload (hex preview): %x\n",
				msg.ID, len(msg.Payload), msg.Payload[:payloadPreviewLen])

			switch msg.ID {
			case peer.MsgBitfield:
				if successfulPeerClient.PeerBitfield != nil {
					expectedBitfieldLen := (successfulPeerClient.NumPiecesInTorrent + 7) / 8
					if len(msg.Payload) == expectedBitfieldLen {
						bfCopy := make(peer.Bitfield, len(msg.Payload))
						copy(bfCopy, msg.Payload)
						successfulPeerClient.PeerBitfield = bfCopy
						log.Printf("Stored Bitfield from peer. Peer has piece 0: %t", successfulPeerClient.PeerBitfield.HasPiece(0))
					} else {
						log.Printf("Warning: Received Bitfield has incorrect length. Got %d, expected %d.", len(msg.Payload), expectedBitfieldLen)
					}
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
					if successfulPeerClient.PeerBitfield != nil && int(havePayload.PieceIndex) < successfulPeerClient.NumPiecesInTorrent {
						successfulPeerClient.PeerBitfield.SetPiece(havePayload.PieceIndex)
						log.Printf("Updated peer bitfield for piece %d.", havePayload.PieceIndex)
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
							if !pieceReceivedBlocks[piecePayload.Begin] {
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
									allBlocksReceived = true
								}

								if allBlocksReceived && !pieceIsComplete {
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

											log.Printf("Attempting to write piece %d to disk...\n", targetPieceIndex)
											pieceOffsetInTorrent := int64(targetPieceIndex) * metaInfo.Info.PieceLength
											bytesToWriteFromPieceBuffer := pieceBuffer
											if len(metaInfo.Info.Files) > 0 { 
												for _, fileInfo := range metaInfo.Info.Files {
													if len(bytesToWriteFromPieceBuffer) == 0 {
														break
													}
													if pieceOffsetInTorrent >= fileInfo.Length {
														pieceOffsetInTorrent -= fileInfo.Length
														continue
													}
													torrentBaseDir := filepath.Join(*downloadDir, metaInfo.Info.Name)
													pathParts := append([]string{torrentBaseDir}, fileInfo.Path...)
													fullFilePath := filepath.Join(pathParts...)
													file, errFile := os.OpenFile(fullFilePath, os.O_WRONLY, 0644)
													if errFile != nil {
														log.Printf("Error opening file %s for writing piece %d: %v.", fullFilePath, targetPieceIndex, errFile)
														break
													}
													_, errSeek := file.Seek(pieceOffsetInTorrent, io.SeekStart)
													if errSeek != nil {
														log.Printf("Error seeking in file %s for piece %d: %v.", fullFilePath, targetPieceIndex, errSeek)
														file.Close()
														break
													}
													bytesInThisFileForThisPiece := fileInfo.Length - pieceOffsetInTorrent
													bytesToWriteNow := int64(len(bytesToWriteFromPieceBuffer))
													if bytesToWriteNow > bytesInThisFileForThisPiece {
														bytesToWriteNow = bytesInThisFileForThisPiece
													}
													n, errWrite := file.Write(bytesToWriteFromPieceBuffer[:bytesToWriteNow])
													file.Close()
													if errWrite != nil {
														log.Printf("Error writing to file %s for piece %d: %v.", fullFilePath, targetPieceIndex, errWrite)
														break
													}
													log.Printf("Successfully wrote %d bytes of piece %d to %s at offset %d\n", n, targetPieceIndex, fullFilePath, pieceOffsetInTorrent)
													bytesToWriteFromPieceBuffer = bytesToWriteFromPieceBuffer[n:]
													pieceOffsetInTorrent = 0
												}
											} else { 
												fullFilePath := filepath.Join(*downloadDir, metaInfo.Info.Name)
												file, errFile := os.OpenFile(fullFilePath, os.O_WRONLY, 0644)
												if errFile != nil {
													log.Printf("Error opening file %s for writing piece %d: %v.", fullFilePath, targetPieceIndex, errFile)
												} else {
													_, errSeek := file.Seek(pieceOffsetInTorrent, io.SeekStart)
													if errSeek != nil {
														log.Printf("Error seeking in file %s for piece %d: %v.", fullFilePath, targetPieceIndex, errSeek)
													} else {
														n, errWrite := file.Write(bytesToWriteFromPieceBuffer)
														if errWrite != nil {
															log.Printf("Error writing to file %s for piece %d: %v.", fullFilePath, targetPieceIndex, errWrite)
														} else {
															log.Printf("Successfully wrote %d bytes of piece %d to %s at offset %d\n", n, targetPieceIndex, fullFilePath, pieceOffsetInTorrent)
														}
													}
													file.Close()
												}
											}
											pieceIsComplete = true
										} else { 
											log.Printf("!!!!!!!! Piece %d HASH MISMATCH! Expected %x, got %x. Discarding piece. !!!!!!!!", targetPieceIndex, expectedHash, actualHash)
											pieceReceivedBlocks = make(map[uint32]bool)
											pieceRequestedBlocks = make(map[uint32]bool)
											nextBlockToRequestOffset = 0
										}
									} else {
										log.Printf("Error: targetPieceIndex %d OOB for PieceHashes. Cannot verify.", targetPieceIndex)
										pieceIsComplete = true
									}
									if pieceIsComplete {
										foundPieceToDownload = false
										pieceIsComplete = false
										log.Println("Will look for a new piece to download.")
									}
								}
							} else {
								log.Printf("Already received block for piece %d at offset %d. Discarding.", targetPieceIndex, piecePayload.Begin)
							}
						} else {
							log.Printf("Warning: Received block for piece %d does not fit buffer or buffer not init.", piecePayload.Index)
						}
					} else {
						log.Printf("Received piece block for unexpected piece index %d (targeting %d).", piecePayload.Index, targetPieceIndex)
					}
				} else {
					log.Printf("Error parsing Piece message payload: %v", err)
				}
			default:
				log.Printf("Received unhandled message ID: %s\n", msg.ID)
			}
		}

		if canRequestPieces && !pieceIsComplete {
			if !foundPieceToDownload {
				for pieceIdx := 0; pieceIdx < numPiecesInTorrent; pieceIdx++ {
					idx := uint32(pieceIdx)
					if successfulPeerClient.PeerBitfield != nil && successfulPeerClient.PeerBitfield.HasPiece(idx) && !ourBitfield.HasPiece(idx) {
						targetPieceIndex = idx
						foundPieceToDownload = true
						if int(targetPieceIndex) == numPiecesInTorrent-1 {
							currentPieceActualLength = metaInfo.TotalLength - (int64(numPiecesInTorrent-1) * metaInfo.Info.PieceLength)
						} else {
							currentPieceActualLength = metaInfo.Info.PieceLength
						}
						if currentPieceActualLength < 0 {
							currentPieceActualLength = 0
						}
						if currentPieceActualLength == 0 {
							foundPieceToDownload = false
							log.Printf("Targeted piece %d has 0 length, skipping.", targetPieceIndex)
							continue
						}
						pieceBuffer = make([]byte, currentPieceActualLength)
						pieceRequestedBlocks = make(map[uint32]bool)
						pieceReceivedBlocks = make(map[uint32]bool)
						nextBlockToRequestOffset = 0
						pieceIsComplete = false
						log.Printf("TARGETING PIECE %d for download (length: %d bytes).\n", targetPieceIndex, currentPieceActualLength)
						break
					}
				}
				if !foundPieceToDownload && (i%20 == 0 && i > 0) {
					log.Println("Still no suitable pieces to request from this peer.")
				}
			}

			if foundPieceToDownload && !pieceIsComplete {
				if int64(nextBlockToRequestOffset) < currentPieceActualLength {
					if _, alreadyRequested := pieceRequestedBlocks[nextBlockToRequestOffset]; !alreadyRequested {
						lengthToRequest := defaultBlockLength
						if int64(nextBlockToRequestOffset+lengthToRequest) > currentPieceActualLength {
							lengthToRequest = uint32(currentPieceActualLength - int64(nextBlockToRequestOffset))
						}
						if lengthToRequest > 0 {
							log.Printf("Requesting piece %d, offset %d, length %d\n", targetPieceIndex, nextBlockToRequestOffset, lengthToRequest)
							reqErr := successfulPeerClient.SendRequest(targetPieceIndex, nextBlockToRequestOffset, lengthToRequest)
							if reqErr != nil {
								log.Printf("Failed to send Request for piece %d, block offset %d: %v\n", targetPieceIndex, nextBlockToRequestOffset, reqErr)
							} else {
								pieceRequestedBlocks[nextBlockToRequestOffset] = true
								log.Printf("Request sent for piece %d, block offset %d.\n", targetPieceIndex, nextBlockToRequestOffset)
								nextBlockToRequestOffset += lengthToRequest
							}
						} else {
							log.Printf("No more data to request for piece %d at offset %d. Marking all blocks as requested.", targetPieceIndex, nextBlockToRequestOffset)
							nextBlockToRequestOffset = uint32(currentPieceActualLength)
						}
					}
				}
			}
		}
	} // End main P2P message loop

	fmt.Println("-----------------------------------------------------")
	log.Println("Finished P2P communication loop with this peer.")
}