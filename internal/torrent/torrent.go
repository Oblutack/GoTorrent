package torrent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bitfield"
	"github.com/Oblutack/GoTorrent/internal/choker"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/picker"
	"github.com/Oblutack/GoTorrent/internal/ratelimit"
	"github.com/Oblutack/GoTorrent/internal/storage"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

const (
	maxPeers          = 50
	pickInterval      = 100 * time.Millisecond
	chokeInterval     = 10 * time.Second
	checkpointEvery   = 30 * time.Second
	checkpointPieces  = 16 // also checkpoint after this many pieces verify
	announceTimeout   = 60 * time.Second
	shutdownAnnounceT = 5 * time.Second
)

// ErrClosed is returned by operations attempted after Stop.
var ErrClosed = errors.New("torrent: already stopped")

// Stats is a snapshot of a torrent's progress, safe to read from any
// goroutine.
type Stats struct {
	State       State
	Downloaded  int64
	Uploaded    int64
	Left        int64
	TotalLength int64
	NumPieces   int
	HavePieces  int
	PeerCount   int
	InEndgame   bool
}

// Config configures a Torrent.
type Config struct {
	// DownloadDir is where the torrent's files live.
	DownloadDir string
	// ResumeDir overrides where resume data is kept. Defaults to ResumeDir().
	ResumeDir string
	// ListenPort is what we advertise to trackers. There is no inbound
	// listener yet (Phase 2), so this is informational only.
	ListenPort uint16
	// OurID is this client's peer ID. GeneratePeerID() if left zero.
	OurID [20]byte
	// Allocation selects sparse or full file pre-allocation.
	Allocation storage.Allocation
	// PickerStrategy selects piece ordering. Defaults to rarest-first.
	PickerStrategy picker.Strategy
	// DownLimit and UpLimit cap this torrent's aggregate transfer rate,
	// shared across every peer connection it opens. Nil means unlimited. An
	// engine managing several torrents typically hands every one of them the
	// same *ratelimit.Limiter, so the cap bounds the whole process rather
	// than each torrent independently.
	DownLimit *ratelimit.Limiter
	UpLimit   *ratelimit.Limiter
}

// peerConn is one connected peer plus the bookkeeping the actor needs that
// peer.Client does not itself track. It implements choker.Peer.
//
// pipelineTarget, outstanding, and the lastAdapt* fields are touched only
// from run() (tick and onBlock), like t.peers itself — see adaptPipeline.
type peerConn struct {
	addr       string
	client     *peer.Client
	downloaded atomic.Int64 // cumulative bytes received, for the choker

	pipelineTarget int       // current desired outstanding-request count
	outstanding    int       // requests sent to this peer, awaiting a Piece
	lastAdaptBytes int64     // pc.downloaded at the last adaptation
	lastAdaptTime  time.Time // when pipelineTarget was last recomputed
}

func (p *peerConn) ID() string             { return p.addr }
func (p *peerConn) Interested() bool       { return p.client.PeerInterested() }
func (p *peerConn) Choking() bool          { return p.client.AmChoking() }
func (p *peerConn) Choke() error           { return p.client.SendChoke() }
func (p *peerConn) Unchoke() error         { return p.client.SendUnchoke() }
func (p *peerConn) BytesDownloaded() int64 { return p.downloaded.Load() }

// Torrent is one torrent's actor: a single goroutine (run, in run.go) owns
// every piece of state in the "actor-owned" group below, and everything
// else — peer goroutines, the tracker loop, callers of the exported
// methods — talks to it through the events and control channels instead of
// touching that state directly.
//
// The exceptions are deliberate and documented at each field: values that are
// set once before any other goroutine can observe them, and values that are
// genuinely shared and therefore atomic.
type Torrent struct {
	infoHash metainfo.Hash
	cfg      Config

	// mi is nil until metadata is known. It is written by the actor alone
	// (openMetadata, or a SetMetadata control message) but read by peer
	// goroutines serving upload requests, so it is an atomic pointer rather
	// than a bare field.
	mi atomic.Pointer[metainfo.MetaInfo]

	// storage is set once by the actor in openMetadata, before any peer
	// goroutine is started, and never reassigned. Storage's own ReadAt/
	// WriteAt are safe for concurrent use, so once published this way it
	// needs no further synchronization.
	storage *storage.Storage

	// haveSnapshot is a read-only copy of the verified-pieces bitfield, swept
	// forward by the actor every time a piece verifies. Peer goroutines read
	// it to answer "do we have piece N" when serving uploads; the picker's
	// own Have() bitfield is not safe for that because it is actor-owned and
	// mutates in place.
	haveSnapshot atomic.Pointer[bitfield.Bitfield]

	state atomic.Int32 // State, readable from any goroutine

	// --- actor-owned: touched only from run() in run.go ---
	pick    *picker.Picker
	choke   *choker.Choker
	peers   map[string]*peerConn
	dialing map[string]bool

	piecesVerifiedSinceCheckpoint int
	lastCheckpoint                time.Time
	// --- end actor-owned ---

	downloaded atomic.Int64
	uploaded   atomic.Int64

	trackerClient *tracker.Client

	events  chan any
	control chan controlMsg

	// ctx/cancel are created at construction time, not inside Run, so Stop
	// can call cancel the instant it is invoked even if the goroutine running
	// Run has not been scheduled yet — see Stop's comment. Run folds
	// whatever context it is given into this one via a watcher goroutine
	// rather than deriving a fresh child from it.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // peer + tracker goroutines
	done   chan struct{}  // closed when run() returns

	// onStateChange is set before Run and never touched again, so reading it
	// from the actor goroutine needs no synchronization.
	onStateChange func(State)
}

// --- construction ------------------------------------------------------

// New creates a torrent from complete metadata — the normal path for a
// .torrent file. It does not start the actor; call Run.
func New(mi *metainfo.MetaInfo, cfg Config) (*Torrent, error) {
	if mi == nil {
		return nil, errors.New("torrent: metainfo is required, use NewFromInfoHash for a magnet link")
	}
	t, err := newTorrent(mi.InfoHash, cfg)
	if err != nil {
		return nil, err
	}
	t.mi.Store(mi)
	t.state.Store(int32(StateAdded))
	return t, nil
}

// NewFromInfoHash creates a torrent whose metadata is not known yet. This is
// the magnet-link shape: the actor can connect to peers and sit in
// StateFetchingMetadata, and SetMetadata transitions it forward once the info
// dictionary is available (BEP 9, Phase 2 — nothing drives that transition
// yet, but the seam exists so Phase 2 only has to plug in the exchange).
func NewFromInfoHash(hash metainfo.Hash, cfg Config) (*Torrent, error) {
	t, err := newTorrent(hash, cfg)
	if err != nil {
		return nil, err
	}
	t.state.Store(int32(StateAdded))
	return t, nil
}

func newTorrent(hash metainfo.Hash, cfg Config) (*Torrent, error) {
	if cfg.DownloadDir == "" {
		return nil, errors.New("torrent: DownloadDir is required")
	}
	if cfg.OurID == ([20]byte{}) {
		id, err := tracker.GeneratePeerID()
		if err != nil {
			return nil, err
		}
		cfg.OurID = id
	}
	if cfg.ResumeDir == "" {
		dir, err := ResumeDir()
		if err != nil {
			return nil, err
		}
		cfg.ResumeDir = dir
	}

	t := &Torrent{
		infoHash:      hash,
		cfg:           cfg,
		trackerClient: tracker.NewClient(nil),
		peers:         make(map[string]*peerConn),
		dialing:       make(map[string]bool),
		choke:         choker.New(),
		events:        make(chan any, 256),
		control:       make(chan controlMsg),
		done:          make(chan struct{}),
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())
	t.haveSnapshot.Store(bitfield.New(0))
	return t, nil
}

// InfoHash is this torrent's identity. It never changes.
func (t *Torrent) InfoHash() metainfo.Hash { return t.infoHash }

// Metadata returns the parsed .torrent info, or nil if it is not known yet.
func (t *Torrent) Metadata() *metainfo.MetaInfo { return t.mi.Load() }

// State is the current lifecycle state.
func (t *Torrent) State() State { return State(t.state.Load()) }

// OnStateChange installs a callback fired from the actor goroutine on every
// transition. It must not block or call back into the Torrent. Must be set
// before Run.
func (t *Torrent) OnStateChange(fn func(State)) { t.onStateChange = fn }

func (t *Torrent) setState(next State) {
	cur := t.State()
	if cur == next {
		return
	}
	if !cur.CanTransition(next) {
		logger.Warning.Printf("torrent %s: illegal transition %s -> %s (ignored)\n", t.infoHash, cur, next)
		return
	}
	t.state.Store(int32(next))
	logger.Logf("torrent %s: %s -> %s\n", t.infoHash, cur, next)
	if t.onStateChange != nil {
		t.onStateChange(next)
	}
}

// Stats returns a snapshot safe to call from any goroutine, including while
// the actor is running.
func (t *Torrent) Stats() Stats {
	s := Stats{
		State:      t.State(),
		Downloaded: t.downloaded.Load(),
		Uploaded:   t.uploaded.Load(),
	}
	if mi := t.mi.Load(); mi != nil {
		s.TotalLength = mi.TotalLength
		s.NumPieces = mi.NumPieces()
		s.Left = s.TotalLength - s.Downloaded
		if s.Left < 0 {
			s.Left = 0
		}
	}

	// HavePieces, PeerCount and InEndgame live on the picker and the peers
	// map, both actor-owned, so they can only come from the actor itself.
	// If it has already stopped, the atomics above are the final answer.
	resp := make(chan Stats, 1)
	select {
	case t.control <- controlMsg{kind: ctrlStats, statsReply: resp}:
	case <-t.done:
		return s
	}
	select {
	case fromActor := <-resp:
		s.HavePieces = fromActor.HavePieces
		s.PeerCount = fromActor.PeerCount
		s.InEndgame = fromActor.InEndgame
	case <-t.done:
	}
	return s
}

// --- lifecycle -----------------------------------------------------------

// Run starts the actor and blocks until the context is cancelled or Stop is
// called. It always returns nil; errors that stop the torrent move it to
// StateError instead of propagating, since one torrent's disk failure must
// not take down whatever is running several of these side by side.
func (t *Torrent) Run(ctx context.Context) error {
	defer close(t.done)

	// t.ctx/t.cancel already exist (set at construction, in newTorrent) so
	// that Stop can cancel this torrent even if called before this goroutine
	// gets scheduled — a real race Stop's own doc comment used to gloss
	// over, caught by an engine test that called Stop immediately after
	// spawning Run. Fold the caller's ctx into that same cancel scope
	// instead of deriving a fresh child from it.
	watcherDone := make(chan struct{})
	defer close(watcherDone)
	go func() {
		select {
		case <-ctx.Done():
			t.cancel()
		case <-watcherDone:
		}
	}()

	if mi := t.mi.Load(); mi != nil {
		if err := t.openMetadata(mi); err != nil {
			logger.Error.Printf("torrent %s: %v\n", t.infoHash, err)
			t.setState(StateError)
			return nil
		}
		t.wg.Add(1)
		go t.announceLoop(t.ctx, tracker.EventStarted)
	} else {
		t.setState(StateFetchingMetadata)
	}

	t.run(t.ctx)

	t.shutdownPeers()
	if mi := t.mi.Load(); mi != nil {
		t.checkpoint()
		t.announceOnce(mi, tracker.EventStopped, shutdownAnnounceT)
	}
	t.wg.Wait()

	// Only safe once wg.Wait has returned: a straggling verify goroutine
	// still holds a reference to t.storage and calls ReadAt on it.
	if t.storage != nil {
		if err := t.storage.Close(); err != nil {
			logger.Warning.Printf("torrent %s: closing storage: %v\n", t.infoHash, err)
		}
	}
	return nil
}

// openMetadata builds storage and the picker once mi is known, loads resume
// data if it is trustworthy, and otherwise runs a full verify. This is the
// CheckingFiles state, whichever way the torrent got here.
func (t *Torrent) openMetadata(mi *metainfo.MetaInfo) error {
	st, err := storage.New(t.cfg.DownloadDir, mi, storage.WithAllocation(t.cfg.Allocation))
	if err != nil {
		return fmt.Errorf("opening storage: %w", err)
	}
	if err := st.Allocate(t.ctx); err != nil {
		return fmt.Errorf("allocating files: %w", err)
	}
	t.storage = st

	pk, err := picker.New(picker.Config{
		NumPieces:   mi.NumPieces(),
		PieceLength: mi.PieceLen,
		Strategy:    t.cfg.PickerStrategy,
	})
	if err != nil {
		return fmt.Errorf("creating picker: %w", err)
	}
	t.pick = pk

	t.setState(StateCheckingFiles)

	if rd, err := loadResume(t.cfg.ResumeDir, t.infoHash, mi, st); err == nil {
		if have, berr := bitfield.FromBytes(rd.PieceBits, mi.NumPieces()); berr == nil {
			if serr := t.pick.SetHave(have); serr == nil {
				t.downloaded.Store(rd.Downloaded)
				t.uploaded.Store(rd.Uploaded)
				t.publishHave(have)
				logger.Logf("torrent %s: resumed from checkpoint, %d/%d pieces\n",
					t.infoHash, have.Count(), mi.NumPieces())
				t.afterVerify()
				return nil
			}
		}
	}

	logger.Logf("torrent %s: no usable resume data, verifying on disk\n", t.infoHash)
	have, err := t.verifyAndBuildBitfield(mi)
	if err != nil {
		return fmt.Errorf("verifying: %w", err)
	}
	if err := t.pick.SetHave(have); err != nil {
		return fmt.Errorf("applying verify results: %w", err)
	}
	t.publishHave(have)
	t.downloaded.Store(bytesForBitfield(mi, have))
	t.afterVerify()
	return nil
}

// verifyAndBuildBitfield runs Verify with a callback that records exactly
// which pieces passed, since VerifyResult only reports a count.
func (t *Torrent) verifyAndBuildBitfield(mi *metainfo.MetaInfo) (*bitfield.Bitfield, error) {
	have := bitfield.New(mi.NumPieces())
	var mu sync.Mutex
	_, err := t.storage.Verify(t.ctx, mi, storage.VerifyOptions{
		OnPiece: func(index int, ok bool) {
			if ok {
				mu.Lock()
				have.Set(index)
				mu.Unlock()
			}
		},
	})
	return have, err
}

func bytesForBitfield(mi *metainfo.MetaInfo, have *bitfield.Bitfield) int64 {
	var total int64
	have.Each(func(i int) bool {
		total += mi.PieceLen(i)
		return true
	})
	return total
}

// publishHave refreshes the read-only snapshot peer goroutines use to answer
// "do we have piece N" when serving upload requests.
func (t *Torrent) publishHave(have *bitfield.Bitfield) {
	t.haveSnapshot.Store(have.Clone())
}

// afterVerify moves to Downloading or Seeding depending on what verification
// found.
func (t *Torrent) afterVerify() {
	if t.pick.Complete() {
		t.setState(StateSeeding)
	} else {
		t.setState(StateDownloading)
	}
}

// SetMetadata supplies the info dictionary for a torrent created with
// NewFromInfoHash, verifying it against the infohash before accepting it.
// This is the seam Phase 2's BEP 9 exchange plugs into; nothing calls it yet.
func (t *Torrent) SetMetadata(mi *metainfo.MetaInfo) error {
	if mi.InfoHash != t.infoHash {
		return fmt.Errorf("torrent: metadata hash %s does not match torrent %s", mi.InfoHash, t.infoHash)
	}
	resp := make(chan error, 1)
	select {
	case t.control <- controlMsg{kind: ctrlSetMetadata, metadata: mi, errReply: resp}:
	case <-t.done:
		return ErrClosed
	}
	select {
	case err := <-resp:
		return err
	case <-t.done:
		return ErrClosed
	}
}

// Pause stops network activity and peer connections but keeps piece state, so
// Resume picks up without a re-verify. It blocks until the pause takes effect.
func (t *Torrent) Pause() error { return t.sendControl(ctrlPause) }

// Resume reconnects and resumes transferring after Pause.
func (t *Torrent) Resume() error { return t.sendControl(ctrlResume) }

// Recheck forces a full re-verification of the data on disk.
func (t *Torrent) Recheck() error { return t.sendControl(ctrlRecheck) }

// Stop shuts the torrent down for good: peers are disconnected, a final
// checkpoint is written, and Run returns. It is safe to call more than once,
// from any goroutine, and even before Run has been given a chance to start —
// t.cancel exists from construction for exactly that reason, so there is no
// window where a Stop racing a freshly-spawned "go tr.Run(ctx)" gets lost.
func (t *Torrent) Stop() {
	t.cancel()
	<-t.done
}

func (t *Torrent) sendControl(kind controlKind) error {
	resp := make(chan error, 1)
	select {
	case t.control <- controlMsg{kind: kind, errReply: resp}:
	case <-t.done:
		return ErrClosed
	}
	select {
	case err := <-resp:
		return err
	case <-t.done:
		return ErrClosed
	}
}

// DialPeer connects to one peer directly, bypassing tracker/DHT/PEX
// discovery. Phase 2 plugs those in behind the same entry point; today it is
// also what lets tests drive a torrent without a live tracker.
func (t *Torrent) DialPeer(pi tracker.PeerInfo) {
	select {
	case t.events <- eventDialRequest{addr: pi}:
	case <-t.done:
	}
}
