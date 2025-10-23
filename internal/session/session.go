package session

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Oblutack/GoTorrent/internal/logger"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/tracker"

	"sort"
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

	muDownloaded     sync.Mutex
	bytesDownloaded  int64
	lastSampledTime  time.Time
	lastSampledBytes int64
	currentSpeedBps  float64

	trackerInterval int
}

type pieceRarity struct {
	Index  uint32
	Rarity int
}

func New(metaInfo *metainfo.MetaInfo, listenPort uint16, downloadDir string) (*TorrentSession, error) {
	peerID, err := tracker.GeneratePeerID()
	if err != nil {
		return nil, err
	}
	logger.Logf("Generated Peer ID (first 8 chars): %s (hex: %x)\n", string(peerID[:8]), peerID)

	numPieces := len(metaInfo.PieceHashes)
	trackerReq := tracker.TrackerRequest{
		InfoHash:   metaInfo.InfoHash,
		PeerID:     peerID,
		Port:       listenPort,
		Uploaded:   0,
		Downloaded: 0,
		Left:       metaInfo.TotalLength,
		Compact:    1,
		Event:      "started",
		NumWant:    50,
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
		lastSampledTime:    time.Now(),
	}
	return s, nil
}

func (s *TorrentSession) Run() error {
	logger.Logf("Starting torrent session...\n")

	if err := s.preallocateFiles(); err != nil {
		return fmt.Errorf("session setup failed during file pre-allocation: %w", err)
	}

	s.populateWorkQueue()

	trackerResponse, err := s.announceToTrackers()
	if err != nil {
		return fmt.Errorf("session setup failed during tracker announce: %w", err)
	}

	logger.Logf("-----------------------------------------------------\n")
	logger.Logf("Tracker Response:\n")
	logger.Logf("  Interval: %d seconds\n", trackerResponse.Interval)
	logger.Logf("  Seeders: %d, Leechers: %d\n", trackerResponse.Complete, trackerResponse.Incomplete)
	logger.Logf("  Received %d peers.\n", len(trackerResponse.Peers))
	logger.Logf("-----------------------------------------------------\n")

	for _, peerInfo := range trackerResponse.Peers {
		s.mu.Lock()
		numConnected := len(s.ConnectedPeers)
		s.mu.Unlock()
		if numConnected >= maxPeers {
			break
		}
		go s.connectToPeer(peerInfo)
	}

	go s.displayLoop()
	go s.trackerLoop() 

	err = s.downloadLoop()
	if err != nil {
		logger.Error.Printf("Download loop failed: %v\n", err)
		return err
	}


	time.Sleep(2 * time.Second)
	fmt.Println() 
	logger.Logf("GoTorrent finished download.\n")
	return nil
}

func (s *TorrentSession) displayLoop() {
	fmt.Print("\033[?25l")       
	defer fmt.Print("\033[?25h") 

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.muDownloaded.Lock()
			now := time.Now()
			elapsed := now.Sub(s.lastSampledTime).Seconds()
			if elapsed > 0.5 {
				bytesSinceLastSample := s.bytesDownloaded - s.lastSampledBytes
				s.currentSpeedBps = float64(bytesSinceLastSample) / elapsed
				s.lastSampledTime = now
				s.lastSampledBytes = s.bytesDownloaded
			}
			currentDownloadedBytes := s.bytesDownloaded
			speed := s.currentSpeedBps
			s.muDownloaded.Unlock()

			verifiedDownloadedBytes := s.TrackerRequest.Downloaded

			totalSize := s.MetaInfo.TotalLength
			percent := 0.0
			if totalSize > 0 {
				percent = (float64(verifiedDownloadedBytes) / float64(totalSize)) * 100
			}

			s.mu.Lock()
			numPeers := len(s.ConnectedPeers)
			s.mu.Unlock()

			speedStr := fmt.Sprintf("%.2f B/s", speed)
			if speed > 1024*1024 {
				speedStr = fmt.Sprintf("%.2f MB/s", speed/(1024*1024))
			} else if speed > 1024 {
				speedStr = fmt.Sprintf("%.2f KB/s", speed/1024)
			}

			displayDownloadedMB := float64(currentDownloadedBytes) / (1024 * 1024)
			totalSizeMB := float64(totalSize) / (1024 * 1024)

			fmt.Printf("\rProgress: %.2f%% | Downloaded: %.2f/%.2f MB | Speed: %s | Peers: %d \033[K",
				percent,
				displayDownloadedMB,
				totalSizeMB,
				speedStr,
				numPeers)

			if totalSize > 0 && verifiedDownloadedBytes >= totalSize {
				fmt.Println()
				logger.Logf("Display loop finished: Download complete.")
				return
			}
		}
	}
}

func (s *TorrentSession) trackerLoop() {
	s.mu.Lock()
	initialInterval := s.trackerInterval
	if initialInterval == 0 {
		initialInterval = 1800 
	}
	s.mu.Unlock()

	logger.Logf("Tracker loop started. Announce interval: %d seconds.\n", initialInterval)
	ticker := time.NewTicker(time.Duration(initialInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			left := s.TrackerRequest.Left
			s.mu.Unlock()

			if left == 0 {
				logger.Logf("Download complete. Stopping tracker loop.\n")
				return
			}

			s.TrackerRequest.Event = ""

			logger.Logf("Re-announcing to tracker...\n")
			trackerResponse, err := s.announceToTrackers()
			if err != nil {
				logger.Warning.Printf("Failed to re-announce to tracker: %v\n", err)
				continue
			}

			s.mu.Lock()
			newInterval := s.trackerInterval
			s.mu.Unlock()

			if newInterval > 0 {
				logger.Logf("Tracker returned new interval: %d seconds.\n", newInterval)
				ticker.Reset(time.Duration(newInterval) * time.Second)
			}

			s.mu.Lock()
			for _, peerInfo := range trackerResponse.Peers {
				if len(s.ConnectedPeers) >= maxPeers {
					break
				}
				// TODO: Check if already connected to this peer before launching goroutine
				go s.connectToPeer(peerInfo)
			}
			s.mu.Unlock()

			// TODO: Add a quit channel for graceful shutdown
		}
	}
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

func (s *TorrentSession) readBlockFromDisk(index, begin, length uint32) ([]byte, error) {
	pieceOffsetInTorrent := int64(index) * s.MetaInfo.Info.PieceLength

	blockOffsetInTorrent := pieceOffsetInTorrent + int64(begin)

	buffer := make([]byte, length)
	bytesRead := 0

	if len(s.MetaInfo.Info.Files) > 0 {
		currentOffset := int64(0)
		for _, fileInfo := range s.MetaInfo.Info.Files {
			fileStart := currentOffset
			fileEnd := currentOffset + fileInfo.Length

			if blockOffsetInTorrent >= fileStart && blockOffsetInTorrent < fileEnd {
				torrentBaseDir := filepath.Join(s.DownloadDir, s.MetaInfo.Info.Name)
				pathParts := append([]string{torrentBaseDir}, fileInfo.Path...)
				fullFilePath := filepath.Join(pathParts...)

				file, err := os.Open(fullFilePath)
				if err != nil {
					return nil, err
				}

				offsetInFile := blockOffsetInTorrent - fileStart
				_, err = file.Seek(offsetInFile, io.SeekStart)
				if err != nil {
					file.Close()
					return nil, err
				}

				n, err := io.ReadFull(file, buffer[bytesRead:])
				bytesRead += n
				file.Close()

				if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
					return nil, err
				}
				if uint32(bytesRead) == length {
					break
				}
				blockOffsetInTorrent += int64(n)
			}
			currentOffset += fileInfo.Length
		}

	} else {
		fullFilePath := filepath.Join(s.DownloadDir, s.MetaInfo.Info.Name)
		file, err := os.Open(fullFilePath)
		if err != nil {
			return nil, err
		}

		_, err = file.Seek(blockOffsetInTorrent, io.SeekStart)
		if err != nil {
			file.Close()
			return nil, err
		}

		_, err = io.ReadFull(file, buffer)
		file.Close()
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, err
		}
	}

	if uint32(len(buffer)) != length {

	}

	return buffer, nil
}

func (s *TorrentSession) connectToPeer(peerInfo tracker.PeerInfo) {
	logger.Logf("Attempting to connect and handshake with peer: %s\n", net.JoinHostPort(peerInfo.IP.String(), strconv.Itoa(int(peerInfo.Port))))
	client, err := peer.NewClient(peerInfo, s.MetaInfo.InfoHash, s.OurPeerID, s.numPiecesInTorrent, s.OurBitfield, s.readBlockFromDisk)
	if err != nil {
		logger.Logf("Warning: Failed to connect or handshake with peer %s: %v\n", peerInfo.IP.String(), err)
		return
	}

	s.mu.Lock()
	s.ConnectedPeers[client.RemoteID] = client
	s.mu.Unlock()

	go client.Run()

	for pieceBlock := range client.Results {
		s.Results <- pieceBlock
	}

	logger.Logf("Peer %s disconnected.", client.Conn.RemoteAddr())

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
			logger.Logf("Piece %d moved to active work.", pieceWork.Index)

		case resultBlock := <-s.Results:
			// --- POČETAK IZMENE ---
			// Ažuriraj bajtove odmah po prijemu bloka za računanje brzine u realnom vremenu
			s.muDownloaded.Lock()
			s.bytesDownloaded += int64(len(resultBlock.Block))
			s.muDownloaded.Unlock()
			// --- KRAJ IZMENE ---

			s.mu.Lock()
			pw, ok := s.ActivePieces[resultBlock.Index]
			if ok {
				blockFound := false
				for i := range pw.Blocks {
					block := &pw.Blocks[i]
					if block.Offset == resultBlock.Begin && block.State == 1 {
						block.State = 2
						copy(pw.Buffer[resultBlock.Begin:], resultBlock.Block)
						pw.ReceivedBlocks++
						logger.Logf("Stored block for piece %d. Progress: %d/%d blocks.", pw.Index, pw.ReceivedBlocks, pw.TotalBlocks)
						blockFound = true
						break
					}
				}
				if !blockFound {
					logger.Logf("Warning: Received a block that was not in 'Requested' state (or wrong offset). Piece %d, offset %d. Discarding.",
						resultBlock.Index, resultBlock.Begin)
				}

				if pw.TotalBlocks > 0 && pw.ReceivedBlocks == pw.TotalBlocks {
					expectedHash := s.MetaInfo.PieceHashes[pw.Index]
					actualHash := sha1.Sum(pw.Buffer)
					if bytes.Equal(actualHash[:], expectedHash[:]) {
						logger.Logf("========== Piece %d HASH VERIFIED! ==========\n", pw.Index)
						if err := s.writePieceToDisk(pw.Index, pw.Buffer); err != nil {
							logger.Logf("CRITICAL: Failed to write piece %d: %v. Re-queueing.", pw.Index, err)
							for i := range pw.Blocks {
								pw.Blocks[i].State = 0
							}
							pw.ReceivedBlocks = 0
							s.PieceWorkQueue <- pw
						} else {
							s.OurBitfield.SetPiece(pw.Index)
							s.TrackerRequest.Downloaded += pw.Length
							s.TrackerRequest.Left -= pw.Length
							logger.Logf("Updated downloaded/left: %d/%d", s.TrackerRequest.Downloaded, s.TrackerRequest.Left)
							logger.Logf("Sending HAVE message for piece %d to all connected peers.", pw.Index)
							for _, peerClient := range s.ConnectedPeers {
								if err := peerClient.SendHave(pw.Index); err != nil {
									logger.Logf("Warning: Failed to send HAVE for piece %d to peer %s: %v",
										pw.Index, peerClient.Conn.RemoteAddr(), err)
								}
							}
						}
					} else {
						logger.Logf("!!!!!!!! Piece %d HASH MISMATCH! Re-queueing. !!!!!!!!\n", pw.Index)
						for i := range pw.Blocks {
							pw.Blocks[i].State = 0
						}
						pw.ReceivedBlocks = 0
						s.PieceWorkQueue <- pw
					}
					delete(s.ActivePieces, pw.Index)
				}
			} else {
				logger.Logf("Received a block for a non-active piece index %d. Discarding.", resultBlock.Index)
			}
			s.mu.Unlock()

		case <-ticker.C:
			s.mu.Lock()
			for _, pw := range s.ActivePieces {
				for i := range pw.Blocks {
					block := &pw.Blocks[i]
					if block.State == 1 && time.Since(block.RequestedAt) > blockRequestTimeout {
						logger.Logf("TIMEOUT for block offset %d of piece %d. Re-queueing.", block.Offset, pw.Index)
						block.State = 0
					}
				}
			}
			rarityMap := make(map[uint32]int)
			for index := range s.ActivePieces {
				if s.OurBitfield.HasPiece(index) {
					continue
				}
				count := 0
				for _, peerClient := range s.ConnectedPeers {
					if peerClient.Bitfield.HasPiece(index) {
						count++
					}
				}
				rarityMap[index] = count
			}
			raritySlice := make([]pieceRarity, 0, len(rarityMap))
			for index, count := range rarityMap {
				raritySlice = append(raritySlice, pieceRarity{Index: index, Rarity: count})
			}
			sort.Slice(raritySlice, func(i, j int) bool {
				return raritySlice[i].Rarity < raritySlice[j].Rarity
			})
			for _, piece := range raritySlice {
				pw, ok := s.ActivePieces[piece.Index]
				if !ok {
					continue
				}
				for _, peerClient := range s.ConnectedPeers {
					if !peerClient.Choked && peerClient.Bitfield.HasPiece(pw.Index) {
						for i := range pw.Blocks {
							block := &pw.Blocks[i]
							if block.State == 0 {
								if len(peerClient.WorkQueue) < cap(peerClient.WorkQueue) {
									block.State = 1
									block.RequestedAt = time.Now()
									logger.Logf("RAREST-FIRST: Assigning (rarity %d) block %d of piece %d to peer %s",
										piece.Rarity, block.Offset, pw.Index, peerClient.Conn.RemoteAddr())
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

	logger.Logf("Download complete!\n")
	return nil
}

func (s *TorrentSession) preallocateFiles() error {
	logger.Logf("Preparing download directory: %s\n", s.DownloadDir)
	if err := os.MkdirAll(s.DownloadDir, 0755); err != nil {
		return fmt.Errorf("failed to create download directory %s: %w", s.DownloadDir, err)
	}

	if len(s.MetaInfo.Info.Files) > 0 {
		torrentBaseDir := filepath.Join(s.DownloadDir, s.MetaInfo.Info.Name)
		logger.Logf("Multi-file torrent. Base directory: %s\n", torrentBaseDir)
		if err := os.MkdirAll(torrentBaseDir, 0755); err != nil {
			return fmt.Errorf("failed to create base torrent directory %s: %w", torrentBaseDir, err)
		}
		for _, fileInfo := range s.MetaInfo.Info.Files {
			pathParts := append([]string{torrentBaseDir}, fileInfo.Path...)
			fullFilePath := filepath.Join(pathParts...)
			if err := os.MkdirAll(filepath.Dir(fullFilePath), 0755); err != nil {
				return fmt.Errorf("failed to create subdirectory for %s: %w", fullFilePath, err)
			}
			logger.Logf("Pre-allocating file: %s (size: %d bytes)\n", fullFilePath, fileInfo.Length)
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
		logger.Logf("Single-file torrent. File: %s (size: %d bytes)\n", fullFilePath, s.MetaInfo.Info.Length)
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
	logger.Logf("File pre-allocation complete.\n")
	return nil
}

func (s *TorrentSession) announceToTrackers() (*tracker.TrackerResponse, error) {
	logger.Logf("Attempting to announce to tracker(s)...")

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
				logger.Logf("Skipping non-HTTP(S) tracker: %s\n", trackerURL)
			}
		}
	}
	if len(httpAnnounceURLs) == 0 {
		return nil, errors.New("no HTTP/HTTPS tracker announce URLs found")
	}

	var trackerResponse *tracker.TrackerResponse
	var lastAnnounceErr error
	for _, announceURL := range httpAnnounceURLs {
		logger.Logf("Announcing to: %s\n", announceURL)
		currentResponse, err := tracker.Announce(announceURL, s.TrackerRequest)
		if err != nil {
			logger.Logf("Warning: Failed to announce to %s: %v\n", announceURL, err)
			lastAnnounceErr = err
			continue
		}
		if currentResponse.FailureReason != "" {
			logger.Logf("Tracker at %s returned failure: %s\n", announceURL, currentResponse.FailureReason)
			lastAnnounceErr = fmt.Errorf("tracker failure at %s: %s", announceURL, currentResponse.FailureReason)
			continue
		}
		logger.Logf("Successfully received response from: %s\n", announceURL)
		trackerResponse = currentResponse

		// Sačuvaj interval za kasniju upotrebu u trackerLoop
		s.mu.Lock()
		s.trackerInterval = trackerResponse.Interval
		s.mu.Unlock()

		break
	}
	if trackerResponse == nil {
		return nil, fmt.Errorf("failed to announce to any available HTTP/HTTPS tracker, last error: %w", lastAnnounceErr)
	}
	return trackerResponse, nil
}

func (s *TorrentSession) writePieceToDisk(pieceIndex uint32, pieceBuffer []byte) error {
	logger.Logf("Attempting to write piece %d to disk...\n", pieceIndex)
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

			logger.Logf("Wrote %d bytes of piece %d to %s\n", n, pieceIndex, fullFilePath)
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
		logger.Logf("Wrote %d bytes of piece %d to %s\n", n, pieceIndex, fullFilePath)
	}
	return nil
}
