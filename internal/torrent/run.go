package torrent

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	pexTicker := time.NewTicker(pexInterval)
	defer pexTicker.Stop()

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

		case <-pexTicker.C:
			t.broadcastPEX()
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
		if t.filePriorities != nil {
			s.FilePriorities = append([]picker.Priority(nil), t.filePriorities...)
		}
		var total int64
		if mi := t.mi.Load(); mi != nil {
			total = mi.TotalLength
		}
		s.SeedRatio = seedRatio(t.uploaded.Load(), t.downloaded.Load(), total)
		s.SeedingDuration = t.currentSeedingDuration(time.Now())
		msg.statsReply <- s

	case ctrlPause:
		msg.errReply <- t.doPause()

	case ctrlResume:
		msg.errReply <- t.doResume()

	case ctrlRecheck:
		msg.errReply <- t.doRecheck()

	case ctrlSetMetadata:
		msg.errReply <- t.doSetMetadata(msg.metadata)

	case ctrlSetFilePriority:
		msg.errReply <- t.doSetFilePriority(msg.fileIndex, msg.priority)

	case ctrlAddTracker:
		msg.errReply <- t.doAddTracker(msg.trackerURL)
	}
}

func (t *Torrent) doPause() error {
	if !t.State().Active() {
		return nil
	}
	t.stopAnnounceLoop()
	t.stopDHTLoop()
	t.shutdownPeers()
	t.checkpoint()
	t.announceOnce(tracker.EventStopped, announceTimeout)
	t.setState(StatePaused)
	return nil
}

func (t *Torrent) doResume() error {
	if t.State() != StatePaused {
		return nil
	}
	mi := t.mi.Load()
	switch {
	case mi == nil:
		t.setState(StateFetchingMetadata)
	case t.pick.Complete():
		t.setState(StateSeeding)
	default:
		t.setState(StateDownloading)
	}
	t.restartAnnounceLoop(tracker.EventStarted)
	t.restartDHTLoop()
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
	t.restartAnnounceLoop(tracker.EventNone)
	t.restartDHTLoop()
	return nil
}

// doSetMetadata does not touch the announce loop at all: one has been
// running continuously since Run started (announcing to Config.Trackers,
// the magnet's tr= parameters), and announceURLs will pick up
// mi.AnnounceURLs() on its very next iteration now that t.mi is set.
func (t *Torrent) doSetMetadata(mi *metainfo.MetaInfo) error {
	if t.State() != StateFetchingMetadata {
		return errors.New("torrent: metadata already known")
	}
	t.mi.Store(mi)
	if err := t.openMetadata(mi); err != nil {
		t.setState(StateError)
		return err
	}

	// Every peer connected before now did its handshake against NumPieces
	// == 0: their Client has no meaningful NumPieces/PieceLength to validate
	// against, and — BEP 3 sends Bitfield at most once — no way to ever
	// re-learn what pieces they have if that message arrived and was
	// necessarily left unapplied. UpgradeMetadata fixes both in one call;
	// see its doc comment. Without this, a torrent that fetched its own
	// metadata over BEP 9 would sit in Downloading forever with a peer
	// already connected and willing, because HasPiece would never agree.
	info := t.peerTorrentInfo()
	for _, pc := range t.peers {
		if err := pc.client.UpgradeMetadata(info); err != nil {
			logger.Warning.Printf("torrent %s: %s's cached bitfield didn't fit the real piece count, dropping: %v\n",
				t.infoHash, pc.addr, err)
			pc.client.Close()
			continue
		}
		t.pick.Availability().AddPeer(pc.client.BitfieldSnapshot())
	}
	return nil
}

// doSetFilePriority changes one file's priority and recomputes every
// piece's effective priority from scratch — piecePriorities has to run
// again in full because a single piece can span several files, so one
// file's change can shift what a piece straddling it is entitled to.
//
// A file moving out of PrioritySkip that was never allocated (it started
// skipped, or was skipped before ever being allocated) is allocated here,
// on demand — see storage.EnsureFileAllocated. A file moving into
// PrioritySkip is never retroactively deleted; this only ever changes what
// gets requested from peers from this point on.
func (t *Torrent) doSetFilePriority(fileIndex int, priority picker.Priority) error {
	mi := t.mi.Load()
	if mi == nil {
		return errors.New("torrent: no metadata yet")
	}
	n := numFiles(mi)
	if fileIndex < 0 || fileIndex >= n {
		return fmt.Errorf("torrent: file index %d out of range (%d files)", fileIndex, n)
	}

	wasSkip := t.filePriorities[fileIndex] == picker.PrioritySkip
	t.filePriorities[fileIndex] = priority

	if wasSkip && priority != picker.PrioritySkip {
		if err := t.storage.EnsureFileAllocated(t.ctx, fileIndex); err != nil {
			t.filePriorities[fileIndex] = picker.PrioritySkip // still isn't there; don't pretend it is
			return fmt.Errorf("torrent: allocating file %d: %w", fileIndex, err)
		}
	}

	pp := piecePriorities(mi, t.filePriorities)
	if t.cfg.FirstLastPieceFirst {
		boostFirstAndLastPiece(mi, t.filePriorities, pp)
	}
	if err := t.pick.SetPriorities(pp); err != nil {
		return fmt.Errorf("torrent: applying file priorities: %w", err)
	}

	// A priority change can flip completeness in either direction: skipping
	// the only remaining wanted files finishes the torrent; un-skipping a
	// file that isn't fully downloaded un-finishes it. Seeding has no
	// direct edge back to Downloading in the state table (by design — see
	// state.go), so that direction goes through CheckingFiles, same as a
	// Recheck, except there is no actual re-verify to do: every piece this
	// torrent has is already known to be have or not-have.
	switch {
	case t.pick.Complete() && t.State() == StateDownloading:
		t.setState(StateSeeding)
		t.checkpoint()
	case !t.pick.Complete() && t.State() == StateSeeding:
		t.setState(StateCheckingFiles)
		t.setState(StateDownloading)
	}
	return nil
}

// doAddTracker appends url to extraTrackers, publishing the whole updated
// slice as a new snapshot — extraTrackers is read by announceLoop/
// announceOnce, both running on their own goroutines, so this is a
// publish, not an in-place mutation (a reader could be mid-range over the
// old slice).
func (t *Torrent) doAddTracker(url string) error {
	if url == "" {
		return errors.New("torrent: tracker URL is empty")
	}
	var current []string
	if p := t.extraTrackers.Load(); p != nil {
		current = *p
	}
	updated := append(append([]string(nil), current...), url)
	t.extraTrackers.Store(&updated)

	// Nudge the announce loop to pick up the new tracker right away rather
	// than waiting out whatever interval it's currently idling on (up to
	// defaultAnnounceInterval, if nothing has ever answered yet) — only
	// when a loop should actually be running; Paused leaves it stopped on
	// purpose, and AddTracker doesn't override that.
	if t.State().Active() {
		t.restartAnnounceLoop(tracker.EventNone)
	}
	return nil
}

// --- event handling -----------------------------------------------------

func (t *Torrent) handleEvent(ev any) {
	switch e := ev.(type) {
	case eventDialRequest:
		t.dial(e.addr)
	case eventDialFailed:
		delete(t.dialing, e.addr)
	case eventIncomingPeer:
		t.acceptIncoming(e.conn, e.hs)
	case eventPeerConnected:
		t.registerPeer(e.pc)
	case eventPeerBlock:
		t.onBlock(e.pc, e.block)
	case eventPeerControl:
		t.onPeerControl(e.pc, e.ev)
	case eventMetadataPiece:
		t.onMetadataPiece(e.pc, e.piece)
	case eventPEXUpdate:
		t.onPEXUpdate(e.pc, e.update)
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
	if t.cfg.IPFilter.Blocked(pi.IP) {
		return
	}
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

// acceptIncoming applies the same dedup-by-address and peer-cap rules dial
// does, keyed by the connection's remote address, before handing the reply
// handshake off to a spawned goroutine. Unlike dial, there is no outcome to
// wait for here beyond that check: the connection already exists, so a
// rejection just means closing it instead of never opening it.
func (t *Torrent) acceptIncoming(conn net.Conn, hs *peer.Handshake) {
	addr := conn.RemoteAddr().String()
	if t.cfg.IPFilter.Blocked(remoteIP(conn)) {
		conn.Close()
		return
	}
	if t.peers[addr] != nil || t.dialing[addr] {
		conn.Close()
		return
	}
	if len(t.peers)+len(t.dialing) >= maxPeers {
		conn.Close()
		return
	}
	t.dialing[addr] = true

	t.wg.Add(1)
	go t.acceptAndPump(t.ctx, conn, hs)
}

// connectAndPump dials, handshakes, registers on success, and then pumps the
// connection's events back to the actor until it disconnects. It runs
// entirely off the actor goroutine; the only actor state it touches is via
// events sent over t.events.
func (t *Torrent) connectAndPump(ctx context.Context, pi tracker.PeerInfo) {
	defer t.wg.Done()

	client, err := peer.NewClient(pi, t.peerTorrentInfo(), t.cfg.OurID,
		peer.Callbacks{HasPiece: t.hasPieceSafe, ReadBlock: t.readBlockSafe, MetadataBytes: t.metadataBytesSafe},
		t.peerLimits(pi.Addr()), t.cfg.ProxyDialer.DialContext)
	if err != nil {
		t.sendEvent(ctx, eventDialFailed{addr: pi.Addr()})
		return
	}

	t.registerAndPump(ctx, &peerConn{addr: pi.Addr(), client: client, peerInfo: pi})
}

// acceptAndPump completes the reply half of an inbound handshake and then
// pumps the connection exactly like connectAndPump — the two differ only in
// how the *peer.Client comes to exist.
func (t *Torrent) acceptAndPump(ctx context.Context, conn net.Conn, hs *peer.Handshake) {
	defer t.wg.Done()

	addr := conn.RemoteAddr().String()
	client, err := peer.AcceptClient(conn, hs, t.peerTorrentInfo(), t.cfg.OurID,
		peer.Callbacks{HasPiece: t.hasPieceSafe, ReadBlock: t.readBlockSafe, MetadataBytes: t.metadataBytesSafe},
		t.peerLimits(addr))
	if err != nil {
		t.sendEvent(ctx, eventDialFailed{addr: addr})
		return
	}

	t.registerAndPump(ctx, &peerConn{addr: addr, client: client})
}

// registerAndPump reports a newly-constructed connection to the actor and
// then relays its Results/Events/MetadataPieces/PEXUpdates to the actor
// until it closes, finally reporting eventPeerGone. Shared by
// connectAndPump and acceptAndPump once each has its own *peer.Client,
// regardless of which side initiated the connection.
func (t *Torrent) registerAndPump(ctx context.Context, pc *peerConn) {
	select {
	case t.events <- eventPeerConnected{pc: pc}:
	case <-ctx.Done():
		pc.client.Close()
		return
	}

	go pc.client.Run()

	client := pc.client
	resultsOpen, eventsOpen, metadataOpen, pexOpen := true, true, true, true
	for resultsOpen || eventsOpen || metadataOpen || pexOpen {
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
			for metadataOpen {
				if _, ok := <-client.MetadataPieces; !ok {
					metadataOpen = false
				}
			}
			for pexOpen {
				if _, ok := <-client.PEXUpdates; !ok {
					pexOpen = false
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
		case mp, ok := <-client.MetadataPieces:
			if !ok {
				metadataOpen = false
				continue
			}
			t.sendEvent(ctx, eventMetadataPiece{pc: pc, piece: mp})
		case pu, ok := <-client.PEXUpdates:
			if !ok {
				pexOpen = false
				continue
			}
			t.sendEvent(ctx, eventPEXUpdate{pc: pc, update: pu})
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
	t.flushUploaded(pc) // bank its final total before pc is discarded
	if t.pick != nil {
		t.pick.Availability().RemovePeer(pc.client.BitfieldSnapshot())
	}
	t.abandonMetadataFetch(pc)
}

// flushUploaded folds however many more bytes pc has served since the last
// flush into t.uploaded. peer.Client counts upload bytes on its own
// read-loop goroutine as it serves requests, so this delta is how that count
// reaches the actor-owned aggregate without double-counting across calls.
func (t *Torrent) flushUploaded(pc *peerConn) {
	total := pc.client.Uploaded()
	if delta := total - pc.lastUploaded; delta > 0 {
		t.uploaded.Add(delta)
		pc.lastUploaded = total
	}
}

// onPeerControl folds a peer's Have/Bitfield/choke/interest change into the
// picker's availability index, and drives the BEP 9 metadata fetch state
// machine from the two event kinds that matter before metadata exists.
func (t *Torrent) onPeerControl(pc *peerConn, ev peer.Event) {
	switch ev.Kind {
	case peer.EventExtendedHandshake:
		// This is exactly the state where t.pick is nil (no metadata yet),
		// so it has to be handled before the pick==nil early return below,
		// not after it.
		t.maybeStartMetadataFetch()
		return
	case peer.EventMetadataReject:
		if t.metadataFetch != nil && t.metadataFetch.peer == pc {
			logger.Warning.Printf("torrent %s: %s rejected metadata piece %d, trying another peer\n",
				t.infoHash, pc.addr, ev.PieceIndex)
			t.abandonMetadataFetch(pc)
		}
		return
	}

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
	case peer.EventRejectRequest:
		// BEP 6: the peer has explicitly told us this block is not coming,
		// rather than us finding out only once the picker's own
		// RequestTimeout elapses. The picker's pending-request bookkeeping
		// for that block still resolves itself via the normal Expire() path
		// on the next tick — this just frees the pipeline slot immediately
		// so tick doesn't keep this connection under-utilized in the
		// meantime waiting on a block that will never arrive.
		if pc.outstanding > 0 {
			pc.outstanding--
		}
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
		// Independent of whether the peer is choking us (that only affects
		// what we can request from them): we may be uploading to them
		// regardless, and this is where that count reaches t.uploaded.
		t.flushUploaded(pc)

		// A choked peer isn't skipped outright: BEP 6's AllowedFast lets it
		// grant specific pieces we may request despite the choke, which is
		// exactly what hasPiece below enforces — Pick simply finds nothing
		// for a choked peer that granted none, the common case, at the same
		// "cheap, not a swarm scan" cost tick already assumes for everyone.
		choked := pc.client.PeerChoking()
		adaptPipeline(pc, now)

		room := pc.pipelineTarget - pc.outstanding
		if queueRoom := cap(pc.client.WorkQueue) - len(pc.client.WorkQueue); queueRoom < room {
			room = queueRoom
		}
		if room <= 0 {
			continue
		}
		hasPiece := func(i int) bool {
			if !pc.client.HasPiece(uint32(i)) {
				return false
			}
			return !choked || pc.client.IsAllowedFast(uint32(i))
		}
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
	t.checkSeedLimits(now)
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

// metadataBytesSafe answers a peer's BEP 9 ut_metadata request. t.mi is an
// atomic pointer, so this needs no actor round-trip either — nil means we
// don't have metadata ourselves yet.
func (t *Torrent) metadataBytesSafe() []byte {
	if mi := t.mi.Load(); mi != nil {
		return mi.InfoBytes
	}
	return nil
}

// peerLimits builds the rate limits a new connection to addr should carry —
// empty (unlimited) for a LAN peer when Config.ExcludeLANFromLimits is set,
// Config.DownLimit/UpLimit otherwise.
func (t *Torrent) peerLimits(addr string) peer.Limits {
	if t.cfg.ExcludeLANFromLimits && isLANAddr(addr) {
		return peer.Limits{}
	}
	return peer.Limits{Down: t.cfg.DownLimit, Up: t.cfg.UpLimit}
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
