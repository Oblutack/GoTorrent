package session

import (
	"bytes"
	"crypto/sha1"
	"errors"
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

	"sync"
	"time"
)

const defaultBlockLength uint32 = 16384
const maxPeers = 50
const blockRequestTimeout = 30 * time.Second

type PieceWork struct {
	Index          uint32
	Length         int64
	Hash           [20]byte
	Buffer         []byte
	Blocks         []BlockState
	TotalBlocks    int
	ReceivedBlocks int
}

type BlockState struct {
	Offset      uint32
	Length      uint32
	State       int
	RequestedAt time.Time
}

type TorrentSession struct {
	MetaInfo           *metainfo.MetaInfo
	OurPeerID          [20]byte
	ListenPort         uint16
	DownloadDir        string
	OurBitfield        peer.Bitfield
	ConnectedPeers     map[[20]byte]*peer.Client
	TrackerRequest     tracker.TrackerRequest
	numPiecesInTorrent int

	PieceWorkQueue chan *PieceWork
	Results        chan *peer.PieceBlock

	mu           sync.Mutex
	ActivePieces map[uint32]*PieceWork
}

func New(metaInfo *metainfo.MetaInfo, listenPort uint16, downloadDir string) (*TorrentSession, error) {
	peerID, err := tracker.GeneratePeerID()
	if err != nil {
		return nil, err
	}
	log.Printf("Generated Peer ID (first 8 chars): %s (hex: %x)\n", string(peerID[:8]), peerID)

	numPieces := len(metaInfo.PieceHashes)
	trackerReq := tracker.TrackerRequest{
		InfoHash: metaInfo.InfoHash, PeerID: peerID, Port: listenPort,
		Uploaded: 0, Downloaded: 0, Left: metaInfo.TotalLength,
		Compact: 1, Event: "started", NumWant: 50,
	}

	s := &TorrentSession{
		MetaInfo:           metaInfo,
		ActivePieces:       make(map[uint32]*PieceWork),
		OurPeerID:          peerID,
		ListenPort:         listenPort,
		DownloadDir:        downloadDir,
		OurBitfield:        peer.NewBitfield(numPieces),
		ConnectedPeers:     make(map[[20]byte]*peer.Client),
		TrackerRequest:     trackerReq,
		numPiecesInTorrent: numPieces,
		PieceWorkQueue:     make(chan *PieceWork, numPieces),
		Results:            make(chan *peer.PieceBlock, 100),
	}
	return s, nil
}

func (s *TorrentSession) Run() error {
	log.Println("Starting torrent session...")

	if err := s.preallocateFiles(); err != nil {
		return fmt.Errorf("session setup failed during file pre-allocation: %w", err)
	}

	s.populateWorkQueue()

	trackerResponse, err := s.announceToTrackers()
	if err != nil {
		return fmt.Errorf("session setup failed during tracker announce: %w", err)
	}

	log.Println("-----------------------------------------------------")
	log.Println("Tracker Response:")
	log.Printf("  Interval: %d seconds", trackerResponse.Interval)
	log.Printf("  Seeders: %d, Leechers: %d", trackerResponse.Complete, trackerResponse.Incomplete)
	log.Printf("  Received %d peers.", len(trackerResponse.Peers))
	log.Println("-----------------------------------------------------")

	for _, peerInfo := range trackerResponse.Peers {
		if len(s.ConnectedPeers) >= maxPeers {
			break
		}

		go s.connectToPeer(peerInfo)
	}

	return s.downloadLoop()
}

func (s *TorrentSession) populateWorkQueue() {
	for i := 0; i < s.numPiecesInTorrent; i++ {
		idx := uint32(i)
		if !s.OurBitfield.HasPiece(idx) {
			pieceLength := s.MetaInfo.Info.PieceLength
			if i == s.numPiecesInTorrent-1 {
				pieceLength = s.MetaInfo.TotalLength - (int64(s.numPiecesInTorrent-1) * s.MetaInfo.Info.PieceLength)
			}
			if pieceLength < 0 {
				pieceLength = 0
			}

			pw := &PieceWork{
				Index:  idx,
				Length: pieceLength,
				Hash:   s.MetaInfo.PieceHashes[i],
				Buffer: make([]byte, pieceLength),
			}

			numBlocks := int((pieceLength + int64(defaultBlockLength) - 1) / int64(defaultBlockLength))
			pw.TotalBlocks = numBlocks
			pw.Blocks = make([]BlockState, numBlocks)
			for j := 0; j < numBlocks; j++ {
				offset := uint32(j) * defaultBlockLength
				length := defaultBlockLength
				if int64(offset+length) > pieceLength {
					length = uint32(pieceLength - int64(offset))
				}
				pw.Blocks[j] = BlockState{
					Offset: offset,
					Length: length,
					State:  0,
				}
			}
			s.PieceWorkQueue <- pw
		}
	}
}

func (s *TorrentSession) connectToPeer(peerInfo tracker.PeerInfo) {
	log.Printf("Attempting to connect and handshake with peer: %s\n", net.JoinHostPort(peerInfo.IP.String(), strconv.Itoa(int(peerInfo.Port))))
	client, err := peer.NewClient(peerInfo, s.MetaInfo.InfoHash, s.OurPeerID, s.numPiecesInTorrent)
	if err != nil {
		log.Printf("Warning: Failed to connect or handshake with peer %s: %v\n", peerInfo.IP.String(), err)
		return
	}

	s.mu.Lock()
	s.ConnectedPeers[client.RemoteID] = client
	s.mu.Unlock()

	go client.Run()

	for pieceBlock := range client.Results {
		s.Results <- pieceBlock
	}

	log.Printf("Peer %s disconnected.", client.Conn.RemoteAddr())

	s.mu.Lock()
	delete(s.ConnectedPeers, client.RemoteID)
	s.mu.Unlock()
}

func (s *TorrentSession) downloadLoop() error {
	ticker := time.NewTicker(5 * time.Second) 
	defer ticker.Stop()

	for s.TrackerRequest.Left > 0 {
		select {
		case pieceWork := <-s.PieceWorkQueue:
			s.mu.Lock()
			s.ActivePieces[pieceWork.Index] = pieceWork
			s.mu.Unlock()
			log.Printf("Piece %d moved to active work.", pieceWork.Index)

		case resultBlock := <-s.Results:
			s.mu.Lock()
			pw, ok := s.ActivePieces[resultBlock.Index]
			if ok {
				for i := range pw.Blocks {
					block := &pw.Blocks[i]
					if block.Offset == resultBlock.Begin && block.State == 1 { 
						block.State = 2 
						copy(pw.Buffer[resultBlock.Begin:], resultBlock.Block)
						pw.ReceivedBlocks++
						log.Printf("Stored block for piece %d. Progress: %d/%d blocks.", pw.Index, pw.ReceivedBlocks, pw.TotalBlocks)
						break
					}
				}

				if pw.ReceivedBlocks == pw.TotalBlocks {
					expectedHash := s.MetaInfo.PieceHashes[pw.Index]
					actualHash := sha1.Sum(pw.Buffer)
					if bytes.Equal(actualHash[:], expectedHash[:]) {
						log.Printf("========== Piece %d HASH VERIFIED! ==========\n", pw.Index)
						if err := s.writePieceToDisk(pw.Index, pw.Buffer); err != nil {
							log.Printf("CRITICAL: Failed to write piece %d: %v", pw.Index, err)
							s.PieceWorkQueue <- pw 
						} else {
							s.OurBitfield.SetPiece(pw.Index)
							s.TrackerRequest.Downloaded += pw.Length
							s.TrackerRequest.Left -= pw.Length
							log.Printf("Updated downloaded/left: %d/%d", s.TrackerRequest.Downloaded, s.TrackerRequest.Left)
							log.Printf("Sending HAVE message for piece %d to all connected peers.", pw.Index)
							for _, peerClient := range s.ConnectedPeers {
								err := peerClient.SendHave(pw.Index)
								if err != nil {
									log.Printf("Warning: Failed to send HAVE message for piece %d to peer %s: %v",
										pw.Index, peerClient.Conn.RemoteAddr(), err)
								}

							}
						}
					} else {
						log.Printf("!!!!!!!! Piece %d HASH MISMATCH! Re-queueing. !!!!!!!!\n", pw.Index)
						for i := range pw.Blocks {
							pw.Blocks[i].State = 0
						}
						pw.ReceivedBlocks = 0
						s.PieceWorkQueue <- pw
					}
					delete(s.ActivePieces, pw.Index) 
				}
			} else {
				log.Printf("Received a block for non-active piece index %d. Discarding.", resultBlock.Index)
			}
			s.mu.Unlock()

		case <-ticker.C:
			s.mu.Lock()

			for _, pw := range s.ActivePieces {
				for i := range pw.Blocks {
					block := &pw.Blocks[i]
					if block.State == 1 && time.Since(block.RequestedAt) > blockRequestTimeout {
						log.Printf("TIMEOUT for block offset %d of piece %d. Re-queueing.", block.Offset, pw.Index)
						block.State = 0 
					}
				}
			}

			for _, pw := range s.ActivePieces {
				for _, peerClient := range s.ConnectedPeers {
					if !peerClient.Choked && peerClient.Bitfield.HasPiece(pw.Index) {
						for i := range pw.Blocks {
							block := &pw.Blocks[i]
							if block.State == 0 { 
								if len(peerClient.WorkQueue) < cap(peerClient.WorkQueue) {
									block.State = 1 
									block.RequestedAt = time.Now()
									log.Printf("Assigning block offset %d of piece %d to peer %s", block.Offset, pw.Index, peerClient.Conn.RemoteAddr())
									peerClient.WorkQueue <- &peer.BlockRequest{Index: pw.Index, Begin: block.Offset, Length: block.Length}
								}
							}
						}
					}
				}
			}
			s.mu.Unlock()
		}
	}

	log.Println("Download complete!")
	return nil
}

func (s *TorrentSession) preallocateFiles() error {
	log.Printf("Preparing download directory: %s\n", s.DownloadDir)
	if err := os.MkdirAll(s.DownloadDir, 0755); err != nil {
		return fmt.Errorf("failed to create download directory %s: %w", s.DownloadDir, err)
	}

	if len(s.MetaInfo.Info.Files) > 0 {
		torrentBaseDir := filepath.Join(s.DownloadDir, s.MetaInfo.Info.Name)
		log.Printf("Multi-file torrent. Base directory: %s\n", torrentBaseDir)
		if err := os.MkdirAll(torrentBaseDir, 0755); err != nil {
			return fmt.Errorf("failed to create base torrent directory %s: %w", torrentBaseDir, err)
		}
		for _, fileInfo := range s.MetaInfo.Info.Files {
			pathParts := append([]string{torrentBaseDir}, fileInfo.Path...)
			fullFilePath := filepath.Join(pathParts...)
			if err := os.MkdirAll(filepath.Dir(fullFilePath), 0755); err != nil {
				return fmt.Errorf("failed to create subdirectory for %s: %w", fullFilePath, err)
			}
			log.Printf("Pre-allocating file: %s (size: %d bytes)\n", fullFilePath, fileInfo.Length)
			file, err := os.OpenFile(fullFilePath, os.O_CREATE|os.O_RDWR, 0644)
			if err != nil {
				return fmt.Errorf("failed to create/open file %s: %w", fullFilePath, err)
			}
			if err := file.Truncate(fileInfo.Length); err != nil {
				file.Close()
				return fmt.Errorf("failed to truncate file %s: %w", fullFilePath, err)
			}
			if err := file.Close(); err != nil {
				return fmt.Errorf("failed to close file %s: %w", fullFilePath, err)
			}
		}
	} else {
		fullFilePath := filepath.Join(s.DownloadDir, s.MetaInfo.Info.Name)
		log.Printf("Single-file torrent. File: %s (size: %d bytes)\n", fullFilePath, s.MetaInfo.Info.Length)
		file, err := os.OpenFile(fullFilePath, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return fmt.Errorf("failed to create/open file %s: %w", fullFilePath, err)
		}
		if err := file.Truncate(s.MetaInfo.Info.Length); err != nil {
			file.Close()
			return fmt.Errorf("failed to truncate file %s: %w", fullFilePath, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("failed to close file %s: %w", fullFilePath, err)
		}
	}
	log.Println("File pre-allocation complete.")
	return nil
}

func (s *TorrentSession) announceToTrackers() (*tracker.TrackerResponse, error) {
	log.Println("Attempting to announce to tracker(s)...")

	var httpAnnounceURLs []string
	if s.MetaInfo.Announce != "" && (strings.HasPrefix(s.MetaInfo.Announce, "http://") || strings.HasPrefix(s.MetaInfo.Announce, "https://")) {
		httpAnnounceURLs = append(httpAnnounceURLs, s.MetaInfo.Announce)
	}
	for _, tier := range s.MetaInfo.AnnounceList {
		for _, trackerURL := range tier {
			if strings.HasPrefix(trackerURL, "http://") || strings.HasPrefix(trackerURL, "https://") {
				isDuplicate := false
				for _, u := range httpAnnounceURLs {
					if u == trackerURL {
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
		return nil, errors.New("no HTTP/HTTPS tracker announce URLs found")
	}

	var trackerResponse *tracker.TrackerResponse
	var lastAnnounceErr error
	for _, announceURL := range httpAnnounceURLs {
		log.Printf("Announcing to: %s\n", announceURL)
		currentResponse, err := tracker.Announce(announceURL, s.TrackerRequest)
		if err != nil {
			log.Printf("Warning: Failed to announce to %s: %v\n", announceURL, err)
			lastAnnounceErr = err
			continue
		}
		if currentResponse.FailureReason != "" {
			log.Printf("Tracker at %s returned failure: %s\n", announceURL, currentResponse.FailureReason)
			lastAnnounceErr = fmt.Errorf("tracker failure at %s: %s", announceURL, currentResponse.FailureReason)
			continue
		}
		log.Printf("Successfully received response from: %s\n", announceURL)
		trackerResponse = currentResponse
		break
	}
	if trackerResponse == nil {
		return nil, fmt.Errorf("failed to announce to any available HTTP/HTTPS tracker, last error: %w", lastAnnounceErr)
	}
	return trackerResponse, nil
}

func (s *TorrentSession) writePieceToDisk(pieceIndex uint32, pieceBuffer []byte) error {
	log.Printf("Attempting to write piece %d to disk...\n", pieceIndex)
	pieceOffsetInTorrent := int64(pieceIndex) * s.MetaInfo.Info.PieceLength
	bytesToWrite := pieceBuffer

	if len(s.MetaInfo.Info.Files) > 0 {
		for _, fileInfo := range s.MetaInfo.Info.Files {
			if len(bytesToWrite) == 0 {
				break
			}
			if pieceOffsetInTorrent >= fileInfo.Length {
				pieceOffsetInTorrent -= fileInfo.Length
				continue
			}

			torrentBaseDir := filepath.Join(s.DownloadDir, s.MetaInfo.Info.Name)
			pathParts := append([]string{torrentBaseDir}, fileInfo.Path...)
			fullFilePath := filepath.Join(pathParts...)
			file, err := os.OpenFile(fullFilePath, os.O_WRONLY, 0644)
			if err != nil {
				return fmt.Errorf("opening file %s: %w", fullFilePath, err)
			}

			_, err = file.Seek(pieceOffsetInTorrent, io.SeekStart)
			if err != nil {
				file.Close()
				return fmt.Errorf("seeking in file %s: %w", fullFilePath, err)
			}

			bytesInFile := fileInfo.Length - pieceOffsetInTorrent
			bytesToWriteNow := int64(len(bytesToWrite))
			if bytesToWriteNow > bytesInFile {
				bytesToWriteNow = bytesInFile
			}

			n, err := file.Write(bytesToWrite[:bytesToWriteNow])
			file.Close()
			if err != nil {
				return fmt.Errorf("writing to file %s: %w", fullFilePath, err)
			}

			log.Printf("Wrote %d bytes of piece %d to %s\n", n, pieceIndex, fullFilePath)
			bytesToWrite = bytesToWrite[n:]
			pieceOffsetInTorrent = 0
		}
	} else {
		fullFilePath := filepath.Join(s.DownloadDir, s.MetaInfo.Info.Name)
		file, err := os.OpenFile(fullFilePath, os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("opening file %s: %w", fullFilePath, err)
		}

		_, err = file.Seek(pieceOffsetInTorrent, io.SeekStart)
		if err != nil {
			file.Close()
			return fmt.Errorf("seeking in file %s: %w", fullFilePath, err)
		}

		n, err := file.Write(bytesToWrite)
		file.Close()
		if err != nil {
			return fmt.Errorf("writing to file %s: %w", fullFilePath, err)
		}
		log.Printf("Wrote %d bytes of piece %d to %s\n", n, pieceIndex, fullFilePath)
	}
	return nil
}
