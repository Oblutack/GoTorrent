package torrent

import (
	"context"
	"errors"
	"time"

	"github.com/Oblutack/GoTorrent/internal/choker"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/picker"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// run is the actor's main loop. It is the only goroutine that ever touches
// t.pick, t.peers, or t.dialing — every other goroutine reaches this state
// exclusively through t.events or t.control.
func (t *Torrent) run(ctx context.Context) {
	pickTicker := time.NewTicker(pickInterval)
	defer pickTicker.Stop()
	chokeTicker := time.NewTicker(chokeInterval)
	defer chokeTicker.Stop()
	checkpointTicker := time.NewTicker(checkpointEvery)
	defer checkpointTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case msg := <-t.control:
			t.handleControl(msg)

		case ev := <-t.events:
			t.handleEvent(ev)

		case now := <-pickTicker.C:
			t.tick(now)

		case now := <-chokeTicker.C:
			t.runChoker(now)

		case <-checkpointTicker.C:
			t.checkpoint()
		}
	}
}

// --- control handling -------------------------------------------------

func (t *Torrent) handleControl(msg controlMsg) {
	switch msg.kind {
	case ctrlStats:
		s := Stats{PeerCount: len(t.peers)}
		if t.pick != nil {
			s.HavePieces = t.pick.Have().Count()
			s.InEndgame = t.pick.InEndgame()
		}
		msg.statsReply <- s

	case ctrlPause:
		msg.errReply <- t.doPause()

	case ctrlResume:
		msg.errReply <- t.doResume()

	case ctrlRecheck:
		msg.errReply <- t.doRecheck()

	case ctrlSetMetadata:
		msg.errReply <- t.doSetMetadata(msg.metadata)
	}
}

func (t *Torrent) doPause() error {
	if !t.State().Active() {
		return nil
	}
	t.shutdownPeers()
	t.checkpoint()
	if mi := t.mi.Load(); mi != nil {
		t.announceOnce(mi, tracker.EventStopped, announceTimeout)
	}
	t.setState(StatePaused)
	return nil
}

func (t *Torrent) doResume() error {
	if t.State() != StatePaused {
		return nil
	}
	mi := t.mi.Load()
	if mi == nil {
		t.setState(StateFetchingMetadata)
		return nil
	}
	if t.pick.Complete() {
		t.setState(StateSeeding)
	} else {
		t.setState(StateDownloading)
	}
	t.wg.Add(1)
	go t.announceLoop(t.ctx, tracker.EventStarted)
	return nil
}

func (t *Torrent) doRecheck() error {
	mi := t.mi.Load()
	if mi == nil {
		return errors.New("torrent: no metadata to verify against")
	}
	t.shutdownPeers()
	t.setState(StateCheckingFiles)

	have, err := t.verifyAndBuildBitfield(mi)
	if err != nil {
		t.setState(StateError)
		return err
	}
	if err := t.pick.SetHave(have); err != nil {
		t.setState(StateError)
		return err
	}
	t.publishHave(have)
	t.downloaded.Store(bytesForBitfield(mi, have))
	t.afterVerify()
	t.wg.Add(1)
	go t.announceLoop(t.ctx, tracker.EventNone)
	return nil
}

func (t *Torrent) doSetMetadata(mi *metainfo.MetaInfo) error {
	if t.State() != StateFetchingMetadata {
		return errors.New("torrent: metadata already known")
	}
	t.mi.Store(mi)
	if err := t.openMetadata(mi); err != nil {
		t.setState(StateError)
		return err
	}
	t.wg.Add(1)
	go t.announceLoop(t.ctx, tracker.EventStarted)
	return nil
}

// --- event handling -----------------------------------------------------

func (t *Torrent) handleEvent(ev any) {
	switch e := ev.(type) {
	case eventDialRequest:
		t.dial(e.addr)
	case eventDialFailed:
		delete(t.dialing, e.addr)
	case eventPeerConnected:
		t.registerPeer(e.pc)
	case eventPeerBlock:
		t.onBlock(e.pc, e.block)
	case eventPeerControl:
		t.onPeerControl(e.pc, e.ev)
	case eventPeerGone:
		t.removePeer(e.pc)
	case eventPieceVerified:
		t.onPieceVerified(e.index, e.ok, e.err)
	case eventTrackerPeers:
		for _, pi := range e.peers {
			t.dial(pi)
		}
	default:
		logger.Warning.Printf("torrent %s: unhandled event %T\n", t.infoHash, ev)
	}
}

// dial decides whether to open a connection to pi, applying the same
// dedup-by-address and peer-cap rules the rest of the swarm logic assumes.
// This is the sole writer of t.dialing and t.peers, so these checks cannot
// race with a concurrent dial from another source.
func (t *Torrent) dial(pi tracker.PeerInfo) {
	addr := pi.Addr()
	if t.peers[addr] != nil || t.dialing[addr] {
		return
	}
	if len(t.peers)+len(t.dialing) >= maxPeers {
		return
	}
	t.dialing[addr] = true

	t.wg.Add(1)
	go t.connectAndPump(t.ctx, pi)
}

// connectAndPump dials, handshakes, registers on success, and then pumps the
// connection's events back to the actor until it disconnects. It runs
// entirely off the actor goroutine; the only actor state it touches is via
// events sent over t.events.
func (t *Torrent) connectAndPump(ctx context.Context, pi tracker.PeerInfo) {
	defer t.wg.Done()

	client, err := peer.NewClient(pi, t.peerTorrentInfo(), t.cfg.OurID, t.hasPieceSafe, t.readBlockSafe,
		peer.Limits{Down: t.cfg.DownLimit, Up: t.cfg.UpLimit})
	if err != nil {
		t.sendEvent(ctx, eventDialFailed{addr: pi.Addr()})
		return
	}

	pc := &peerConn{addr: pi.Addr(), client: client}
	select {
	case t.events <- eventPeerConnected{pc: pc}:
	case <-ctx.Done():
		client.Close()
		return
	}

	go client.Run()

	resultsOpen, eventsOpen := true, true
	for resultsOpen || eventsOpen {
		select {
		case <-ctx.Done():
			client.Close()
			// Drain until Run's deferred closes happen, so this goroutine
			// does not exit while Run is still mid-flight touching pc.
			for resultsOpen {
				if _, ok := <-client.Results; !ok {
					resultsOpen = false
				}
			}
			for eventsOpen {
				if _, ok := <-client.Events; !ok {
					eventsOpen = false
				}
			}
		case block, ok := <-client.Results:
			if !ok {
				resultsOpen = false
				continue
			}
			t.sendEvent(ctx, eventPeerBlock{pc: pc, block: block})
		case pev, ok := <-client.Events:
			if !ok {
				eventsOpen = false
				continue
			}
			t.sendEvent(ctx, eventPeerControl{pc: pc, ev: pev})
		}
	}

	t.sendEvent(ctx, eventPeerGone{pc: pc})
}

// sendEvent delivers an event to the actor, giving up if ctx is done.
//
// ctx here must be the same context the calling goroutine watches for its
// own shutdown (t.ctx, threaded through from connectAndPump/verifyPiece) —
// never t.done. t.done only closes after Run's wg.Wait() returns, and
// wg.Wait() is waiting on these very goroutines: falling back to t.done
// would deadlock the moment run()'s select stops reading t.events (which it
// does the instant ctx is cancelled) while a tracked goroutine is still
// trying to deliver one last event.
func (t *Torrent) sendEvent(ctx context.Context, ev any) {
	select {
	case t.events <- ev:
	case <-ctx.Done():
	}
}

func (t *Torrent) registerPeer(pc *peerConn) {
	delete(t.dialing, pc.addr)
	t.peers[pc.addr] = pc
}

func (t *Torrent) removePeer(pc *peerConn) {
	if _, ok := t.peers[pc.addr]; !ok {
		return
	}
	delete(t.peers, pc.addr)
	if t.pick != nil {
		t.pick.Availability().RemovePeer(pc.client.BitfieldSnapshot())
	}
}

// onPeerControl folds a peer's Have/Bitfield/choke/interest change into the
// picker's availability index. Choke/interest changes need no bookkeeping
// here — Pick and the choker read the peer's own accessors directly — so
// only Have and Bitfield do anything.
func (t *Torrent) onPeerControl(pc *peerConn, ev peer.Event) {
	if t.pick == nil {
		return // no metadata yet; availability has nothing to track
	}
	switch ev.Kind {
	case peer.EventBitfield:
		// Only the first Bitfield folds in via AddPeer. A second one (a
		// protocol oddity — BEP 3 sends it at most once) is not diffed
		// against the first; see the package-level note in torrent.go on
		// availability accounting for why this is an accepted simplification.
		t.pick.Availability().AddPeer(pc.client.BitfieldSnapshot())
	case peer.EventHave:
		t.pick.Availability().Add(int(ev.PieceIndex))
	}
}

// onBlock writes a received block to disk and folds it into the picker.
//
// The write happens synchronously, on the actor goroutine, which keeps the
// implementation simple: by the time Received reports a piece complete,
// every one of its blocks has already finished its WriteAt call in program
// order, so verifying by reading the piece straight back is always correct.
// The cost is that a slow disk delays the next tick — acceptable for a
// 16 KiB write on any storage this client is likely to run on; a bounded
// write-worker-pool with a completion barrier would be the next step if
// profiling ever shows otherwise.
func (t *Torrent) onBlock(pc *peerConn, block *peer.PieceBlock) {
	mi := t.mi.Load()
	if mi == nil || t.pick == nil {
		return
	}

	length := len(block.Block)
	pc.downloaded.Add(int64(length))
	// This block fills one of the slots adaptPipeline budgeted for pc,
	// whether or not the picker still wants the data (endgame can satisfy a
	// block from another peer first, in which case this is a harmless
	// no-op read of already-verified data below).
	if pc.outstanding > 0 {
		pc.outstanding--
	}

	offset := int64(block.Index)*mi.Info.PieceLength + int64(block.Begin)
	if _, err := t.storage.WriteAt(block.Block, offset); err != nil {
		logger.Error.Printf("torrent %s: write failed for piece %d block %d: %v\n",
			t.infoHash, block.Index, block.Begin, err)
		// Leave the block outstanding; the picker's timeout re-requests it,
		// possibly from a peer whose path to the disk works better — though
		// if the disk itself is the problem that will not help. Turning a
		// run of write failures into StateError is future work; today it
		// just retries forever, which is at least never wrong.
		return
	}

	completed, wanted := t.pick.Received(int(block.Index), int(block.Begin), length)
	if !wanted {
		return
	}
	t.downloaded.Add(int64(length))

	if completed {
		index := int(block.Index)
		if t.pick.InEndgame() {
			t.cancelDuplicates(mi, index)
		}
		t.wg.Add(1)
		go t.verifyPiece(t.ctx, mi, index)
	}
}

// cancelDuplicates tells every connected peer we no longer want any block of
// a piece that just completed. Only endgame ever creates duplicate in-flight
// requests (Pick only redunantly re-issues a pending block when InEndgame is
// true), so this only runs then. It broadcasts rather than targeting the
// specific peers a duplicate was sent to — the actor does not track
// per-peer-per-block assignments — which costs a few harmless no-op Cancels
// on peers that never had the request outstanding.
func (t *Torrent) cancelDuplicates(mi *metainfo.MetaInfo, index int) {
	length := mi.PieceLen(index)
	for begin := int64(0); begin < length; begin += picker.BlockLength {
		blockLen := int64(picker.BlockLength)
		if remaining := length - begin; remaining < blockLen {
			blockLen = remaining
		}
		for _, pc := range t.peers {
			if err := pc.client.SendCancel(uint32(index), uint32(begin), uint32(blockLen)); err != nil {
				logger.Logf("torrent %s: Cancel to %s: %v\n", t.infoHash, pc.addr, err)
			}
		}
	}
}

// verifyPiece hashes a completed piece against the metainfo and reports the
// result back to the actor. It runs off the actor goroutine so a slow hash
// (large piece length) does not stall picking or event handling.
func (t *Torrent) verifyPiece(ctx context.Context, mi *metainfo.MetaInfo, index int) {
	defer t.wg.Done()
	ok, err := t.storage.VerifyOne(ctx, mi, index)
	t.sendEvent(ctx, eventPieceVerified{index: index, ok: ok, err: err})
}

func (t *Torrent) onPieceVerified(index int, ok bool, err error) {
	if err != nil {
		logger.Error.Printf("torrent %s: verifying piece %d: %v\n", t.infoHash, index, err)
		t.pick.MarkFailed(index)
		return
	}
	if !ok {
		logger.Warning.Printf("torrent %s: piece %d failed hash check, re-downloading\n", t.infoHash, index)
		t.pick.MarkFailed(index)
		return
	}

	t.pick.MarkVerified(index)
	t.piecesVerifiedSinceCheckpoint++
	t.publishHave(t.pick.Have())

	for _, pc := range t.peers {
		if err := pc.client.SendHave(uint32(index)); err != nil {
			logger.Logf("torrent %s: Have to %s: %v\n", t.infoHash, pc.addr, err)
		}
	}

	if t.pick.Complete() {
		t.setState(StateSeeding)
		t.checkpoint()
	}
	if t.piecesVerifiedSinceCheckpoint >= checkpointPieces {
		t.checkpoint()
	}
}

// --- ticks ---------------------------------------------------------------

const (
	// minPipeline is the floor adaptPipeline ever sets: enough that one lost
	// or slow block does not stall a peer's whole queue, even fresh off a
	// connection with no throughput history yet.
	minPipeline = 4
	// pipelineWindow is the target amount of data adaptPipeline tries to
	// keep in flight to one peer, expressed as seconds of that peer's
	// measured download rate. This is the standard bandwidth-delay-product
	// heuristic (see e.g. libtorrent's request_queue_time): too small and a
	// fast peer sits idle between ticks waiting on a fresh Pick; too large
	// and a slow peer accumulates requests other peers could have served
	// faster, plus a bigger loss if it disconnects mid-piece.
	pipelineWindow = 2 * time.Second
	// pipelineAdaptInterval bounds how often a peer's target is recomputed.
	// Recomputing every 100ms tick would react to noise in a single block's
	// arrival time rather than sustained throughput.
	pipelineAdaptInterval = 1 * time.Second
)

// tick assigns work to every unchoked, capable peer and expires timed-out
// requests. Picker.Pick is a handful of map/slice operations against an
// index that is maintained incrementally, not a scan of the whole swarm, so
// running it every 100ms for every peer is cheap even at torrent scale.
func (t *Torrent) tick(now time.Time) {
	if t.pick == nil || !t.State().Active() {
		return
	}

	t.pick.Expire(now)

	for _, pc := range t.peers {
		if pc.client.PeerChoking() {
			continue
		}
		adaptPipeline(pc, now)

		room := pc.pipelineTarget - pc.outstanding
		if queueRoom := cap(pc.client.WorkQueue) - len(pc.client.WorkQueue); queueRoom < room {
			room = queueRoom
		}
		if room <= 0 {
			continue
		}
		hasPiece := func(i int) bool { return pc.client.HasPiece(uint32(i)) }
		reqs := t.pick.Pick(hasPiece, room, now)
		for _, r := range reqs {
			select {
			case pc.client.WorkQueue <- &peer.BlockRequest{Index: uint32(r.Index), Begin: uint32(r.Begin), Length: uint32(r.Length)}:
				pc.outstanding++
			default:
				// The queue filled between the room check and now (another
				// tick's leftover); the picker already marked it pending, so
				// it will be retried on timeout rather than lost.
			}
		}
	}

	if t.pick.Complete() && t.State() == StateDownloading {
		t.setState(StateSeeding)
		t.checkpoint()
	}
}

// adaptPipeline recomputes how many outstanding requests pc should be
// allowed, from its measured download rate since the last adaptation. This
// replaces a fixed pipeline depth (the old PipelineSize=50 for every peer
// regardless of speed) with one sized to each peer: a peer on a slow link
// gets few requests in flight so a disconnect loses little queued work, and
// a fast one gets enough that it is never left idle waiting on the next
// 100ms tick.
func adaptPipeline(pc *peerConn, now time.Time) {
	if pc.lastAdaptTime.IsZero() {
		pc.lastAdaptTime = now
		pc.lastAdaptBytes = pc.downloaded.Load()
		pc.pipelineTarget = minPipeline
		return
	}

	elapsed := now.Sub(pc.lastAdaptTime)
	if elapsed < pipelineAdaptInterval {
		return
	}

	current := pc.downloaded.Load()
	rate := float64(current-pc.lastAdaptBytes) / elapsed.Seconds()
	pc.lastAdaptBytes = current
	pc.lastAdaptTime = now

	target := int(rate * pipelineWindow.Seconds() / picker.BlockLength)
	if target < minPipeline {
		target = minPipeline
	}
	if target > peer.MaxPipelineSize {
		target = peer.MaxPipelineSize
	}
	pc.pipelineTarget = target
}

func (t *Torrent) runChoker(now time.Time) {
	peers := make([]choker.Peer, 0, len(t.peers))
	for _, pc := range t.peers {
		peers = append(peers, pc)
	}
	t.choke.Run(peers, now)
}

// --- shutdown and checkpointing ------------------------------------------

// shutdownPeers closes every connection and clears the swarm-derived state
// that only makes sense while peers are attached. It is called both for a
// Pause and for the final Stop, and is idempotent either way.
func (t *Torrent) shutdownPeers() {
	for _, pc := range t.peers {
		pc.client.Close()
	}
	t.peers = make(map[string]*peerConn)
	if t.pick != nil {
		t.pick.Availability().Reset()
		// Every in-flight request just lost its destination. Without this,
		// those blocks stay marked pending and Pick will not re-offer them
		// to a freshly (re)connected peer until RequestTimeout elapses.
		t.pick.ResetAllPending()
	}
}

func (t *Torrent) checkpoint() {
	mi := t.mi.Load()
	if mi == nil || t.pick == nil || t.storage == nil {
		return
	}
	rd := buildResume(t.infoHash, mi, t.storage, t.pick.Have().Bytes(), t.downloaded.Load(), t.uploaded.Load())
	if err := rd.save(t.cfg.ResumeDir); err != nil {
		logger.Error.Printf("torrent %s: checkpoint failed: %v\n", t.infoHash, err)
		return
	}
	t.piecesVerifiedSinceCheckpoint = 0
	t.lastCheckpoint = time.Now()
}

// --- peer-goroutine-safe accessors ---------------------------------------

// hasPieceSafe answers a peer's "do you have piece N" check for serving
// uploads. It runs on the peer's own goroutine, so it reads the published
// snapshot rather than the actor-owned picker.
func (t *Torrent) hasPieceSafe(index uint32) bool {
	return t.haveSnapshot.Load().Has(int(index))
}

// readBlockSafe answers a peer's read for an upload. storage.ReadAt is safe
// for concurrent use by design, so this needs no actor round-trip.
func (t *Torrent) readBlockSafe(index, begin, length uint32) ([]byte, error) {
	mi := t.mi.Load()
	if mi == nil || t.storage == nil {
		return nil, errors.New("torrent: no data available yet")
	}
	offset := int64(index)*mi.Info.PieceLength + int64(begin)
	buf := make([]byte, length)
	if _, err := t.storage.ReadAt(buf, offset); err != nil {
		return nil, err
	}
	return buf, nil
}

func (t *Torrent) peerTorrentInfo() peer.TorrentInfo {
	mi := t.mi.Load()
	if mi == nil {
		return peer.TorrentInfo{InfoHash: t.infoHash}
	}
	return peer.TorrentInfo{
		InfoHash:    t.infoHash,
		NumPieces:   mi.NumPieces(),
		PieceLength: mi.Info.PieceLength,
		TotalLength: mi.TotalLength,
	}
}
