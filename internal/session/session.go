package session

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"

	"strings"

	"github.com/Oblutack/GoTorrent/internal/logger"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/storage"
	"github.com/Oblutack/GoTorrent/internal/tracker"

	"sort"
	"sync"

	"net"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

const defaultBlockLength uint32 = 16384
const maxPeers = 50
const blockRequestTimeout = 15 * time.Second

// maxInFlightPieces bounds how many pieces may hold a full piece buffer at
// once. Peak memory is roughly maxInFlightPieces * Info.PieceLength, which is
// what keeps a 6 GB torrent from allocating 6 GB of heap at startup.
const maxInFlightPieces = 64

// defaultAnnounceInterval is used until a tracker tells us otherwise.
const defaultAnnounceInterval = 30 * time.Minute

// announceTimeout bounds one round of announces across all trackers.
const announceTimeout = 60 * time.Second

type PieceWork struct {
	Index          uint32
	Length         int64
	Hash           metainfo.Hash
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

// errHashMismatch marks a piece whose SHA-1 did not match the metainfo.
var errHashMismatch = errors.New("piece hash mismatch")

// pieceResult reports the outcome of the asynchronous verify-and-write step
// back to downloadLoop. This used to be signalled in-band on PieceWork itself
// (a nil Buffer meant "write failed", ReceivedBlocks == -1 meant "hash
// mismatch"), which made it impossible to recycle the buffer.
type pieceResult struct {
	pw  *PieceWork
	err error
}

type TorrentSession struct {
	MetaInfo    *metainfo.MetaInfo
	OurPeerID   [20]byte
	ListenPort  uint16
	DownloadDir string
	OurBitfield peer.Bitfield

	// ConnectedPeers is keyed by remote address. The peer ID from the
	// handshake is attacker-controlled, so keying on it let one peer evict
	// another's entry just by claiming its ID.
	ConnectedPeers map[string]*peer.Client

	// dialing tracks addresses with a connection attempt in flight, so the
	// same peer is not dialled twice from overlapping tracker responses.
	dialing            map[string]bool
	TrackerRequest     tracker.AnnounceRequest
	numPiecesInTorrent int

	// layout is the only thing in the session allowed to turn a
	// torrent-supplied name into a filesystem path.
	layout *storage.Layout

	PieceWorkQueue chan *PieceWork
	Results        chan *peer.PieceBlock

	mu           sync.Mutex
	ActivePieces map[uint32]*PieceWork

	// bufferPool recycles full-size piece buffers between active pieces.
	bufferPool sync.Pool

	muDownloaded     sync.Mutex
	bytesDownloaded  int64
	lastSampledTime  time.Time
	lastSampledBytes int64
	currentSpeedBps  float64

	trackerClient   *tracker.Client
	trackerInterval time.Duration
}

type pieceRarity struct {
	Index  uint32
	Rarity int
}

func (s *TorrentSession) stateFilePath() string {
	return filepath.Join(s.DownloadDir, fmt.Sprintf(".%s.state", s.MetaInfo.InfoHash))
}

// saveState writes OurBitfield to disk.
func (s *TorrentSession) saveState() error {
	logger.Logf("Saving download state to %s\n", s.stateFilePath())
	return os.WriteFile(s.stateFilePath(), s.OurBitfield, 0644)
}

// loadState reads OurBitfield back from disk.
func (s *TorrentSession) loadState() error {
	filePath := s.stateFilePath()
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		logger.Logf("No previous state file found. Starting from scratch.")
		return nil // A missing state file just means a fresh download.
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("could not read state file: %w", err)
	}

	if len(data) != len(s.OurBitfield) {
		return fmt.Errorf("state file has incorrect size. Expected %d, got %d. Starting fresh.",
			len(s.OurBitfield), len(data))
	}

	copy(s.OurBitfield, data)
	logger.Logf("Successfully loaded download state from %s\n", filePath)

	// Recompute Downloaded/Left from the loaded bitfield. This assumes every
	// piece is the full piece length except the last one.
	var downloadedBytes int64
	for i := 0; i < s.numPiecesInTorrent; i++ {
		if s.OurBitfield.HasPiece(uint32(i)) {
			var pieceLength int64
			if i == s.numPiecesInTorrent-1 {
				pieceLength = s.MetaInfo.TotalLength - (int64(s.numPiecesInTorrent-1) * s.MetaInfo.Info.PieceLength)
			} else {
				pieceLength = s.MetaInfo.Info.PieceLength
			}
			downloadedBytes += pieceLength
		}
	}
	s.TrackerRequest.Downloaded = downloadedBytes
	s.bytesDownloaded = downloadedBytes // keep the speed counter consistent
	s.lastSampledBytes = downloadedBytes
	s.TrackerRequest.Left = s.MetaInfo.TotalLength - downloadedBytes
	logger.Logf("Resuming download. Downloaded: %d, Left: %d\n", s.TrackerRequest.Downloaded, s.TrackerRequest.Left)

	return nil
}

func New(metaInfo *metainfo.MetaInfo, listenPort uint16, downloadDir string) (*TorrentSession, error) {
	peerID, err := tracker.GeneratePeerID()
	if err != nil {
		return nil, err
	}
	logger.Logf("Generated Peer ID (first 8 chars): %s (hex: %x)\n", string(peerID[:8]), peerID)

	layout, err := storage.NewLayout(downloadDir, metaInfo.Info.Name, len(metaInfo.Info.Files) > 0)
	if err != nil {
		return nil, err
	}

	numPieces := metaInfo.NumPieces()
	trackerReq := tracker.AnnounceRequest{
		InfoHash:   metaInfo.InfoHash,
		PeerID:     peerID,
		Port:       listenPort,
		Uploaded:   0,
		Downloaded: 0,
		Left:       metaInfo.TotalLength,
		Compact:    true,
		Event:      tracker.EventStarted,
		NumWant:    50,
	}

	s := &TorrentSession{
		MetaInfo:           metaInfo,
		ActivePieces:       make(map[uint32]*PieceWork),
		OurPeerID:          peerID,
		ListenPort:         listenPort,
		DownloadDir:        downloadDir,
		layout:             layout,
		OurBitfield:        peer.NewBitfield(numPieces),
		ConnectedPeers:     make(map[string]*peer.Client),
		dialing:            make(map[string]bool),
		TrackerRequest:     trackerReq,
		trackerClient:      tracker.NewClient(nil),
		numPiecesInTorrent: numPieces,
		PieceWorkQueue:     make(chan *PieceWork, numPieces),
		Results:            make(chan *peer.PieceBlock, 100),
		lastSampledTime:    time.Now(),
	}

	pieceLength := metaInfo.Info.PieceLength
	s.bufferPool.New = func() interface{} {
		buf := make([]byte, pieceLength)
		return &buf
	}

	if err := s.loadState(); err != nil {
		logger.Logf("Warning: could not load previous state: %v. Continuing with a fresh download.", err)
		// Reset OurBitfield in case loadState partially succeeded before failing.
		s.OurBitfield = peer.NewBitfield(metaInfo.NumPieces())
	}

	return s, nil
}

func (s *TorrentSession) Run() error {
	logger.Logf("Starting torrent session...\n")

	if err := s.preallocateFiles(); err != nil {
		return fmt.Errorf("session setup failed during file pre-allocation: %w", err)
	}

	// New already loaded the resume state; loading it a second time here only
	// duplicated the work.
	if s.TrackerRequest.Left == 0 {
		logger.Logf("All pieces already present. Starting in seeding mode.\n")
	} else {
		// There is something to fetch, so fill the work queue.
		s.populateWorkQueue()
	}

	trackerResponse, err := s.announceToTrackers()
	if err != nil {
		// Even if the tracker fails we can still seed, provided we have the
		// complete file.
		if s.TrackerRequest.Left > 0 {
			return fmt.Errorf("session setup failed during tracker announce: %w", err)
		}
		logger.Warning.Printf("Could not announce to tracker, but will proceed in seeding mode: %v\n", err)
	}

	if trackerResponse != nil {
		logger.Logf("-----------------------------------------------------\n")
		logger.Logf("Tracker Response:\n")
		logger.Logf("  Interval: %d seconds\n", trackerResponse.Interval)
		logger.Logf("  Seeders: %d, Leechers: %d\n", trackerResponse.Complete, trackerResponse.Incomplete)
		logger.Logf("  Received %d peers.\n", len(trackerResponse.Peers))
		logger.Logf("-----------------------------------------------------\n")

		// connectToPeer enforces the maxPeers cap and deduplicates by
		// address, so a tracker returning thousands of peers cannot make us
		// open thousands of sockets.
		for _, peerInfo := range trackerResponse.Peers {
			go s.connectToPeer(peerInfo)
		}
	}

	// Graceful shutdown setup
	interruptChan := make(chan os.Signal, 1)
	signal.Notify(interruptChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-interruptChan
		logger.Logf("\nShutdown signal received. Saving state and stopping...\n")

		if err := s.saveState(); err != nil {
			logger.Error.Printf("Error saving state on exit: %v\n", err)
		}

		s.TrackerRequest.Event = tracker.EventStopped
		_, err := s.announceToTrackers()
		if err != nil {
			logger.Warning.Printf("Failed to send 'stopped' event to tracker: %v\n", err)
		}
		os.Exit(0)
	}()

	// Start the background loops.
	go s.displayLoop()
	go s.trackerLoop()
	go s.chokingLoop()

	// Only run the download loop if there is anything left to fetch.
	if s.TrackerRequest.Left > 0 {
		err = s.downloadLoop()
		if err != nil {
			logger.Error.Printf("Download loop finished with error: %v\n", err)
		}
		logger.Logf("\nDownload complete.\n")
		// Tell the tracker we finished.
		s.TrackerRequest.Event = tracker.EventCompleted
		go s.announceToTrackers()
		s.TrackerRequest.Event = tracker.EventNone
	}

	// Whether we just finished downloading or started out complete, we end up
	// seeding.
	logger.Logf("Entering seeding mode. Press Ctrl-C to exit.\n")

	// Block until Ctrl-C tears the process down.
	select {}
}

func (s *TorrentSession) displayLoop() {
	// Hide cursor during display
	fmt.Print("\033[?25l")
	// Ensure cursor is shown again on exit
	defer fmt.Print("\033[?25h")

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Local variables for speed calculation
	var lastBytes int64 = 0
	var lastTime time.Time = time.Now()

	// Pre-load initial downloaded bytes if resuming
	s.muDownloaded.Lock()
	lastBytes = s.bytesDownloaded
	s.muDownloaded.Unlock()

	for {
		select {
		case <-ticker.C:
			// Get current total downloaded bytes
			s.muDownloaded.Lock()
			currentBytes := s.bytesDownloaded
			s.muDownloaded.Unlock()

			// Calculate speed
			now := time.Now()
			elapsed := now.Sub(lastTime).Seconds()
			var speed float64 = 0
			if elapsed > 0.1 { // Avoid division by zero and noisy values
				speed = float64(currentBytes-lastBytes) / elapsed
			}

			// Update for the next iteration
			lastTime = now
			lastBytes = currentBytes

			// Get other stats (verified downloaded bytes, peer count)
			s.mu.Lock()
			verifiedDownloadedBytes := s.TrackerRequest.Downloaded
			numPeers := len(s.ConnectedPeers)
			s.mu.Unlock()

			totalSize := s.MetaInfo.TotalLength
			percent := 0.0
			if totalSize > 0 {
				percent = (float64(verifiedDownloadedBytes) / float64(totalSize)) * 100
			}

			// Format speed for display
			speedStr := fmt.Sprintf("%.2f B/s", speed)
			if speed > 1024*1024 {
				speedStr = fmt.Sprintf("%.2f MB/s", speed/(1024*1024))
			} else if speed > 1024 {
				speedStr = fmt.Sprintf("%.2f KB/s", speed/1024)
			}

			// Use verified bytes for Downloaded MB to be consistent with percentage
			downloadedMB := float64(verifiedDownloadedBytes) / (1024 * 1024)
			totalSizeMB := float64(totalSize) / (1024 * 1024)

			// Print the status line
			// \r returns cursor to start, \033[K clears the rest of the line
			fmt.Printf("\rProgress: %.2f%% | Downloaded: %.2f/%.2f MB | Speed: %s | Peers: %d \033[K",
				percent,
				downloadedMB,
				totalSizeMB,
				speedStr,
				numPeers)

			// Exit condition for the display loop
			if totalSize > 0 && verifiedDownloadedBytes >= totalSize {
				fmt.Println() // Move to a new line after 100%
				logger.Logf("Display loop finished: Download complete.\n")
				return
			}
		}
	}
}

func (s *TorrentSession) trackerLoop() {
	s.mu.Lock()
	initialInterval := s.trackerInterval
	if initialInterval <= 0 {
		initialInterval = defaultAnnounceInterval
	}
	s.mu.Unlock()

	logger.Logf("Tracker loop started. Announce interval: %s.\n", initialInterval)
	ticker := time.NewTicker(initialInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			left := s.TrackerRequest.Left
			s.mu.Unlock()

			if left == 0 {
				// While seeding we keep announcing periodically. We could stop the
				// ticker here instead if re-announcing as a seeder is unwanted.
			}

			s.TrackerRequest.Event = tracker.EventNone

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
				logger.Logf("Tracker returned new interval: %s.\n", newInterval)
				ticker.Reset(newInterval)
			}

			for _, peerInfo := range trackerResponse.Peers {
				go s.connectToPeer(peerInfo)
			}
		}
	}
}

// getPieceBuffer hands out a buffer of exactly length bytes, reusing a pooled
// allocation when one is available. The returned slice always has the pool's
// full-size backing array so putPieceBuffer can hand it straight back.
func (s *TorrentSession) getPieceBuffer(length int64) []byte {
	bufPtr := s.bufferPool.Get().(*[]byte)
	buf := *bufPtr
	if int64(cap(buf)) < length {
		buf = make([]byte, length)
	}
	return buf[:length]
}

// putPieceBuffer returns a buffer to the pool. Passing nil is a no-op.
func (s *TorrentSession) putPieceBuffer(buf []byte) {
	if buf == nil {
		return
	}
	full := buf[:cap(buf)]
	s.bufferPool.Put(&full)
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

			// Buffer stays nil here. It is allocated from bufferPool only when
			// the piece becomes active, so queued work costs a few hundred
			// bytes per piece instead of a full piece length.
			pw := &PieceWork{
				Index:  idx,
				Length: pieceLength,
				Hash:   s.MetaInfo.PieceHashes[i],
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
				fullFilePath, err := s.layout.Resolve(fileInfo.Path)
				if err != nil {
					return nil, err
				}

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
		fullFilePath, err := s.layout.Resolve(nil)
		if err != nil {
			return nil, err
		}
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

// hasPiece reports whether we have a verified copy of a piece. Peer
// connections call this from their own goroutines, so it must take the mutex
// rather than sharing OurBitfield directly.
func (s *TorrentSession) hasPiece(index uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.OurBitfield.HasPiece(index)
}

// connectToPeer dials one peer and pumps its results into the session. It is
// safe to call for an address that is already connected or already being
// dialled: the duplicate is dropped before the socket is opened.
func (s *TorrentSession) connectToPeer(peerInfo tracker.PeerInfo) {
	address := net.JoinHostPort(peerInfo.IP.String(), strconv.Itoa(int(peerInfo.Port)))

	s.mu.Lock()
	switch {
	case s.ConnectedPeers[address] != nil:
		s.mu.Unlock()
		logger.Logf("Already connected to %s, skipping.\n", address)
		return
	case s.dialing[address]:
		s.mu.Unlock()
		logger.Logf("Already dialling %s, skipping.\n", address)
		return
	case len(s.ConnectedPeers)+len(s.dialing) >= maxPeers:
		s.mu.Unlock()
		logger.Logf("Peer limit of %d reached, not dialling %s.\n", maxPeers, address)
		return
	}
	s.dialing[address] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.dialing, address)
		s.mu.Unlock()
	}()

	logger.Logf("Attempting to connect and handshake with peer: %s\n", address)

	torrentInfo := peer.TorrentInfo{
		InfoHash:    s.MetaInfo.InfoHash,
		NumPieces:   s.numPiecesInTorrent,
		PieceLength: s.MetaInfo.Info.PieceLength,
		TotalLength: s.MetaInfo.TotalLength,
	}
	client, err := peer.NewClient(peerInfo, torrentInfo, s.OurPeerID, s.hasPiece, s.readBlockFromDisk)
	if err != nil {
		logger.Warning.Printf("Failed to connect or handshake with peer %s: %v\n", address, err)
		return
	}

	s.mu.Lock()
	s.ConnectedPeers[address] = client
	s.mu.Unlock()

	go client.Run()

	for pieceBlock := range client.Results {
		s.Results <- pieceBlock
	}

	logger.Logf("Peer %s disconnected.\n", client.Conn.RemoteAddr())

	s.mu.Lock()
	delete(s.ConnectedPeers, address)
	s.mu.Unlock()
}

func (s *TorrentSession) downloadLoop() error {
	// Ticker for timeout checks (e.g. 5 seconds)
	timeoutTicker := time.NewTicker(5 * time.Second)
	defer timeoutTicker.Stop()

	// Ticker for work assignment (e.g. 50ms for pipelining)
	workTicker := time.NewTicker(50 * time.Millisecond)
	defer workTicker.Stop()

	// Channel for async hash&disk ops
	verifiedPiecesCh := make(chan *pieceResult, 100)

	for {
		s.mu.Lock()
		left := s.TrackerRequest.Left
		activeCount := len(s.ActivePieces)
		s.mu.Unlock()

		if left <= 0 {
			break
		}

		// Every active piece holds a full piece buffer, so the in-flight cap is
		// what bounds memory. Receiving from a nil channel blocks forever, which
		// disables the intake arm of the select until a piece completes.
		var workQueue <-chan *PieceWork
		if activeCount < maxInFlightPieces {
			workQueue = s.PieceWorkQueue
		}

		select {
		case pieceWork := <-workQueue:
			s.mu.Lock()
			if pieceWork.Buffer == nil {
				pieceWork.Buffer = s.getPieceBuffer(pieceWork.Length)
			}
			s.ActivePieces[pieceWork.Index] = pieceWork
			s.mu.Unlock()
			logger.Logf("Piece %d moved to active work.\n", pieceWork.Index)

		case resultBlock := <-s.Results:
			s.muDownloaded.Lock()
			s.bytesDownloaded += int64(len(resultBlock.Block))
			s.muDownloaded.Unlock()

			s.mu.Lock()
			pw, ok := s.ActivePieces[resultBlock.Index]
			if ok {
				blockFound := false
				for i := range pw.Blocks {
					block := &pw.Blocks[i]
					if block.Offset == resultBlock.Begin && block.State == 1 {
						// The peer layer already bounds the block to the piece,
						// but the block must also be exactly the size we asked
						// for or the piece would contain a hole.
						if uint32(len(resultBlock.Block)) != block.Length {
							logger.Warning.Printf("Discarding block for piece %d offset %d: got %d bytes, expected %d.\n",
								resultBlock.Index, resultBlock.Begin, len(resultBlock.Block), block.Length)
							blockFound = true
							break
						}
						block.State = 2
						copy(pw.Buffer[resultBlock.Begin:], resultBlock.Block)
						pw.ReceivedBlocks++
						blockFound = true
						logger.Logf("Stored block for piece %d. Progress: %d/%d blocks.\n", pw.Index, pw.ReceivedBlocks, pw.TotalBlocks)
						break
					}
				}
				if !blockFound {
					// Likely an overlapping request from our fast-snatch logic. Just discard silently.
					// logger.Logf("Received unsolicited/late block for piece %d, offset %d. Discarding.\n", resultBlock.Index, resultBlock.Begin)
				}
				if pw.TotalBlocks > 0 && pw.ReceivedBlocks == pw.TotalBlocks {
					delete(s.ActivePieces, pw.Index) // Remove from active immediately

					// Offload hash & disk write to a goroutine so downloadLoop
					// keeps servicing the wire while SHA-1 and I/O happen.
					go func(offloadPw *PieceWork) {
						expectedHash := s.MetaInfo.PieceHashes[offloadPw.Index]
						if metainfo.Hash(sha1.Sum(offloadPw.Buffer)) != expectedHash {
							logger.Warning.Printf("!!!!!!!! Piece %d HASH MISMATCH! Re-queueing. !!!!!!!!\n", offloadPw.Index)
							verifiedPiecesCh <- &pieceResult{pw: offloadPw, err: errHashMismatch}
							return
						}
						if err := s.writePieceToDisk(offloadPw.Index, offloadPw.Buffer); err != nil {
							logger.Error.Printf("CRITICAL: Failed to write piece %d: %v. Re-queueing.\n", offloadPw.Index, err)
							verifiedPiecesCh <- &pieceResult{pw: offloadPw, err: err}
							return
						}
						verifiedPiecesCh <- &pieceResult{pw: offloadPw}
					}(pw)
				}
			} else {
				logger.Logf("Received block for non-active piece %d.\n", resultBlock.Index)
			}
			s.mu.Unlock()

		case result := <-verifiedPiecesCh:
			verifiedPw := result.pw

			// The buffer has done its job either way: on success the bytes are
			// on disk, on failure the piece is re-downloaded from scratch.
			s.putPieceBuffer(verifiedPw.Buffer)
			verifiedPw.Buffer = nil

			if result.err != nil {
				for i := range verifiedPw.Blocks {
					verifiedPw.Blocks[i].State = 0
				}
				verifiedPw.ReceivedBlocks = 0
				// PieceWorkQueue is buffered to numPieces and a given piece can
				// only be in it once, so this send cannot block.
				s.PieceWorkQueue <- verifiedPw
				continue
			}

			s.mu.Lock()
			logger.Logf("========== Piece %d VERIFIED AND WRITTEN ==========\n", verifiedPw.Index)
			s.OurBitfield.SetPiece(verifiedPw.Index)
			s.TrackerRequest.Downloaded += verifiedPw.Length
			s.TrackerRequest.Left -= verifiedPw.Length
			logger.Logf("Updated downloaded/left: %d/%d\n", s.TrackerRequest.Downloaded, s.TrackerRequest.Left)
			logger.Logf("Sending HAVE message for piece %d to all peers.\n", verifiedPw.Index)
			for _, peerClient := range s.ConnectedPeers {
				if err := peerClient.SendHave(verifiedPw.Index); err != nil {
					logger.Warning.Printf("Failed to send HAVE: %v\n", err)
				}
			}
			s.mu.Unlock()

		case <-timeoutTicker.C:
			// This case runs periodically for timeouts.
			s.mu.Lock()

			inEndgame := len(s.ActivePieces) < 15

			// 0. Tracker rapid-re-announce if we are stuck.
			// If we are missing pieces but have no active pieces (all connected peers don't have what we need),
			// we should wake up the tracker Loop! We'll just do a hacky check here:
			activePws := 0
			for _, pw := range s.ActivePieces {
				for i := range pw.Blocks {
					if pw.Blocks[i].State == 1 {
						activePws++
						break
					}
				}
			}

			// If we are totally stalled despite having pieces left, and we have few peers, let's just close peers that aren't useful anymore.
			if activePws == 0 && s.TrackerRequest.Left > 0 {
				for _, peerClient := range s.ConnectedPeers {
					now := time.Now().Unix()
					// If they haven't sent a piece in 45s and we're stalled, drop them to force tracker/reconnect
					if now-peerClient.LastPieceReceivedUnix() > 45 {
						logger.Warning.Printf("Stalled download. Dropping inactive peer %s to find seeders.\n", peerClient.Conn.RemoteAddr())
						peerClient.Close()
					}
				}
			}

			// 1. Check for timed out block requests
			for _, pw := range s.ActivePieces {
				for i := range pw.Blocks {
					block := &pw.Blocks[i]
					timeout := blockRequestTimeout
					if inEndgame {
						timeout = 3 * time.Second
					}
					if block.State == 1 && time.Since(block.RequestedAt) > timeout {
						logger.Warning.Printf("TIMEOUT for block offset %d of piece %d. Re-queueing.\n", block.Offset, pw.Index)
						block.State = 0 // Reset state to 'Needed'
					}
				}
			}
			s.mu.Unlock()

		case <-workTicker.C:
			// Rarest First and Work Assignment
			s.mu.Lock()
			rarityMap := make(map[uint32]int)
			for index := range s.ActivePieces {
				if s.OurBitfield.HasPiece(index) {
					continue
				}
				count := 0
				for _, peerClient := range s.ConnectedPeers {
					if peerClient.HasPiece(index) {
						count++
					}
				}
				if count > 0 {
					rarityMap[index] = count
				}
			}
			raritySlice := make([]pieceRarity, 0, len(rarityMap))
			for index, count := range rarityMap {
				raritySlice = append(raritySlice, pieceRarity{Index: index, Rarity: count})
			}
			// Shuffle first to randomize ties
			rand.Shuffle(len(raritySlice), func(i, j int) {
				raritySlice[i], raritySlice[j] = raritySlice[j], raritySlice[i]
			})
			sort.SliceStable(raritySlice, func(i, j int) bool { return raritySlice[i].Rarity < raritySlice[j].Rarity })

			// 3. Distribute blocks from the rarest pieces across all available peers
			for _, piece := range raritySlice {
				pw, ok := s.ActivePieces[piece.Index]
				if !ok {
					continue
				}

				for i := range pw.Blocks {
					block := &pw.Blocks[i]

					// If the block is strictly completed, skip it.
					if block.State == 2 {
						continue
					}
					// If the block is currently in-flight but taking longer than 2.5 seconds,
					// allow another peer to snatch it (overlapping/endgame strategy).
					if block.State == 1 && time.Since(block.RequestedAt) < 2500*time.Millisecond {
						continue
					}

					for _, peerClient := range s.ConnectedPeers {
						if !peerClient.PeerChoking() && peerClient.HasPiece(pw.Index) {
							if len(peerClient.WorkQueue) < cap(peerClient.WorkQueue) {
								block.State = 1
								block.RequestedAt = time.Now()
								logger.Logf("Assigning (rarity %d) block %d of piece %d to peer %s\n",
									piece.Rarity, block.Offset, pw.Index, peerClient.Conn.RemoteAddr())

								peerClient.WorkQueue <- &peer.BlockRequest{Index: pw.Index, Begin: block.Offset, Length: block.Length}

								goto nextBlockInPiece
							}
						}
					}
				nextBlockInPiece:
				}
			}
			s.mu.Unlock()
		}
	}

	logger.Logf("\nDownload complete!\n")
	return nil
}

func (s *TorrentSession) preallocateFiles() error {
	logger.Logf("Preparing download directory: %s\n", s.DownloadDir)
	if err := os.MkdirAll(s.DownloadDir, 0755); err != nil {
		return fmt.Errorf("failed to create download directory %s: %w", s.DownloadDir, err)
	}

	if len(s.MetaInfo.Info.Files) > 0 {
		torrentBaseDir := s.layout.Base()
		logger.Logf("Multi-file torrent. Base directory: %s\n", torrentBaseDir)
		if err := os.MkdirAll(torrentBaseDir, 0755); err != nil {
			return fmt.Errorf("failed to create base torrent directory %s: %w", torrentBaseDir, err)
		}
		for _, fileInfo := range s.MetaInfo.Info.Files {
			fullFilePath, err := s.layout.Resolve(fileInfo.Path)
			if err != nil {
				return err
			}
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
		fullFilePath, err := s.layout.Resolve(nil)
		if err != nil {
			return err
		}
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

func (s *TorrentSession) announceToTrackers() (*tracker.AnnounceResponse, error) {
	logger.Logf("Attempting to announce to tracker(s)...")

	var announceURLs []string
	for _, u := range s.MetaInfo.AnnounceURLs() {
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			announceURLs = append(announceURLs, u)
		} else {
			logger.Logf("Skipping non-HTTP(S) tracker: %s\n", u)
		}
	}
	if len(announceURLs) == 0 {
		return nil, errors.New("no HTTP/HTTPS tracker announce URLs found")
	}

	s.mu.Lock()
	req := s.TrackerRequest
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), announceTimeout)
	defer cancel()

	var lastErr error
	for _, announceURL := range announceURLs {
		logger.Logf("Announcing to: %s\n", announceURL)
		resp, err := s.trackerClient.Announce(ctx, announceURL, req)
		if err != nil {
			logger.Logf("Warning: announce to %s failed: %v\n", announceURL, err)
			lastErr = err
			continue
		}
		if resp.WarningMessage != "" {
			logger.Warning.Printf("Tracker %s: %s\n", announceURL, resp.WarningMessage)
		}
		logger.Logf("Successfully received response from: %s\n", announceURL)

		// Remember the interval for trackerLoop.
		s.mu.Lock()
		s.trackerInterval = resp.Interval
		s.mu.Unlock()

		return resp, nil
	}
	return nil, fmt.Errorf("failed to announce to any HTTP/HTTPS tracker: %w", lastErr)
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

			fullFilePath, err := s.layout.Resolve(fileInfo.Path)
			if err != nil {
				return err
			}
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
		fullFilePath, err := s.layout.Resolve(nil)
		if err != nil {
			return err
		}
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

func (s *TorrentSession) chokingLoop() {
	const unchokeSlots = 4 // how many peers we unchoke at once

	// Re-evaluate who is unchoked every 10 seconds.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.mu.Lock()

			// TODO: sort by upload rate for real tit-for-tat. For now we just
			// take the first N interested peers.
			unchokedCount := 0
			for _, peerClient := range s.ConnectedPeers {
				shouldUnchoke := peerClient.PeerInterested() && unchokedCount < unchokeSlots
				if shouldUnchoke {
					unchokedCount++
				}

				switch {
				case shouldUnchoke && peerClient.AmChoking():
					// SendUnchoke owns the AmChoking flag, so the flag and the
					// wire message cannot drift apart.
					if err := peerClient.SendUnchoke(); err != nil {
						logger.Logf("Failed to send Unchoke to %s: %v", peerClient.Conn.RemoteAddr(), err)
					} else {
						logger.Logf("Optimistically unchoking peer %s", peerClient.Conn.RemoteAddr())
					}
				case !shouldUnchoke && !peerClient.AmChoking():
					if err := peerClient.SendChoke(); err != nil {
						logger.Logf("Failed to send Choke to %s: %v", peerClient.Conn.RemoteAddr(), err)
					} else {
						logger.Logf("Choking peer %s (no longer in top uploaders)", peerClient.Conn.RemoteAddr())
					}
				}
			}
			s.mu.Unlock()

			// TODO: Quit channel
		}
	}
}
