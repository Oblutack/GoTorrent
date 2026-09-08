package torrent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bitfield"
	"github.com/Oblutack/GoTorrent/internal/choker"
	"github.com/Oblutack/GoTorrent/internal/ipfilter"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/picker"
	"github.com/Oblutack/GoTorrent/internal/proxy"
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
	// FilePriorities is one entry per file, in file order — nil until
	// metadata is known (see Torrent.SetFilePriority).
	FilePriorities []picker.Priority
	// SeedRatio is Uploaded / (Downloaded, falling back to TotalLength if
	// nothing has been downloaded this session — the initial-seed case) — see
	// seedRatio. Meaningless before metadata is known.
	SeedRatio float64
	// SeedingDuration is cumulative time spent in StateSeeding, across
	// however many Pause/Resume cycles happened since the process started —
	// see Torrent.setState. Not persisted across a restart.
	SeedingDuration time.Duration
}

// Config configures a Torrent.
type Config struct {
	// DownloadDir is where the torrent's files live.
	DownloadDir string
	// ResumeDir overrides where resume data is kept. Defaults to ResumeDir().
	ResumeDir string
	// ListenPort is what we advertise to trackers and DHT peers, and (via
	// Engine.Listen) actually accept inbound connections on.
	ListenPort uint16
	// OurID is this client's peer ID. If left zero, generated fresh —
	// tracker.GeneratePeerID() normally, or tracker.GenerateAnonymousPeerID()
	// (no identifying prefix) when AnonymousMode is set.
	OurID [20]byte
	// AnonymousMode selects GenerateAnonymousPeerID over GeneratePeerID for
	// OurID (3.7) — see its own doc comment above. Meaningless if OurID is
	// already non-zero.
	AnonymousMode bool
	// Allocation selects sparse or full file pre-allocation.
	Allocation storage.Allocation
	// ContentLayout selects whether this torrent's data gets a wrapping
	// <DownloadDir>/<name>/ directory. Defaults to storage.LayoutOriginal.
	ContentLayout storage.ContentLayout
	// PickerStrategy selects piece ordering. Defaults to rarest-first.
	PickerStrategy picker.Strategy
	// DownLimit and UpLimit cap this torrent's aggregate transfer rate,
	// shared across every peer connection it opens — every entry is waited
	// on (see peer.Limits), so a torrent can carry both a process-wide cap
	// (the same *ratelimit.Limiter an engine hands to every torrent it
	// manages, bounding the whole process rather than each torrent
	// independently) and its own per-torrent cap at once. Nil or empty
	// means unlimited.
	DownLimit []*ratelimit.Limiter
	UpLimit   []*ratelimit.Limiter
	// UploadSlots overrides how many peers this torrent unchokes at once
	// (choker.WithSlots) — 0 means choker.DefaultSlots.
	UploadSlots int
	// ExcludeLANFromLimits skips DownLimit/UpLimit entirely for a peer whose
	// address is a private or loopback IP, so a same-LAN transfer always
	// runs at full local speed regardless of the internet-facing cap.
	ExcludeLANFromLimits bool
	// IPFilter rejects a peer address before ever dialing or accepting it
	// (3.7) — checked in dial and acceptIncoming. Nil (the default, and
	// also what a nil *ipfilter.Filter itself does — see its own doc
	// comment) blocks nothing; an engine typically hands every torrent it
	// manages the same shared *ipfilter.Filter, the same "one instance,
	// several owners" shape as DownLimit/UpLimit.
	IPFilter *ipfilter.Filter
	// ProxyDialer (3.7) tunnels every outbound peer connection through a
	// configured SOCKS5/HTTP proxy — passed straight through to
	// peer.NewClient. Nil (the default, and also what a nil *proxy.Dialer
	// itself does) dials directly. The HTTP(S) tracker client is built
	// from the same Dialer in newTorrent, so both share one proxy
	// configuration; UDP trackers and DHT are not proxied (SOCKS5 UDP
	// ASSOCIATE is a real protocol extension this client does not
	// implement — a deliberate, documented gap).
	ProxyDialer *proxy.Dialer
	// Trackers seeds the announce loop before metadata is known — a magnet
	// link's tr= parameters. Ignored once mi is set: from then on
	// announceURLs reads mi.AnnounceURLs() instead. Meaningless for a
	// Config passed to New, which always has metadata from the start.
	Trackers []string
	// DHT is this torrent's peer-discovery route into the mainline DHT
	// (BEP 5), typically one node shared by every torrent an engine.Engine
	// manages — see dht.go. Nil disables DHT for this torrent entirely,
	// which is also what happens automatically, mid-flight, if metadata
	// later reveals the torrent is private (BEP 27).
	DHT DHTClient
	// FilePriorities sets each file's initial download priority, in the
	// same order as the torrent's own file list (one entry for a
	// single-file torrent). Empty (the default) starts every file at
	// picker.PriorityNormal. A file starting at picker.PrioritySkip is
	// never allocated on disk at all — see storage.WithSkipFiles — which is
	// the only time "never allocated" is fully achievable; changing a
	// file's priority later, via SetFilePriority, only ever changes what
	// gets requested from peers from that point on, not what already
	// exists on disk. Meaningless before metadata is known: applied once,
	// in openMetadata.
	FilePriorities []picker.Priority
	// SeedRatioLimit pauses the torrent once SeedRatio (see Stats) reaches
	// this value, checked continuously while Seeding. 0 (the default) means
	// unlimited. Note that resuming a torrent that is already over its ratio
	// limit pauses it again on the very next tick — this is deliberate, the
	// same "hard limit" semantics most clients use, not a bug.
	SeedRatioLimit float64
	// SeedTimeLimit pauses the torrent once it has spent this much cumulative
	// time in StateSeeding (see Stats.SeedingDuration), checked continuously
	// while Seeding. 0 (the default) means unlimited.
	SeedTimeLimit time.Duration
	// FirstLastPieceFirst raises the first and last piece of every non-skip
	// file to picker.PriorityHigh, on top of whatever FilePriorities already
	// set — 3.1's "makes media previewable" mode: a video file's start and
	// end arrive early regardless of PickerStrategy, so a player pointed at
	// the (still incomplete) file can open it and show something.
	FirstLastPieceFirst bool
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

	// peerInfo is this peer's dial-back address — known for a peer we
	// dialed ourselves (it's exactly what we dialed), zero-value for one
	// that connected to us: the BitTorrent handshake carries no "my
	// listening port" field, so an inbound connection's source port is
	// almost always an ephemeral client port, not a real dial-back address.
	// broadcastPEX (run.go) only ever advertises peers with a non-zero
	// Port here, which is what keeps unreachable inbound-only addresses out
	// of what this client tells others via BEP 11.
	peerInfo tracker.PeerInfo

	pipelineTarget int       // current desired outstanding-request count
	outstanding    int       // requests sent to this peer, awaiting a Piece
	lastAdaptBytes int64     // pc.downloaded at the last adaptation
	lastAdaptTime  time.Time // when pipelineTarget was last recomputed

	// lastUploaded is pc.client.Uploaded() as of the last flushUploaded call,
	// so its delta can be folded into t.uploaded without double-counting.
	// Upload bytes are counted on peer.Client's own read-loop goroutine (it
	// serves requests as they arrive), not the actor, so this is how that
	// count reaches the actor-owned aggregate.
	lastUploaded int64
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

	// extraTrackers is every URL added at runtime via AddTracker (3.6),
	// written only by the actor (doAddTracker) but read by announceLoop/
	// announceOnce, which run on their own spawned goroutines — the same
	// publish-once-read-many shape as mi/haveSnapshot above, hence the same
	// atomic.Pointer treatment. Nil means none added. Not persisted: a
	// reloaded torrent starts back with just what its own .torrent/magnet
	// already specified.
	extraTrackers atomic.Pointer[[]string]

	state atomic.Int32 // State, readable from any goroutine

	// --- actor-owned: touched only from run() in run.go ---
	pick    *picker.Picker
	choke   *choker.Choker
	peers   map[string]*peerConn
	dialing map[string]bool

	// metadataFetch tracks an in-progress BEP 9 metadata download. Non-nil
	// only while mi is nil; see maybeStartMetadataFetch.
	metadataFetch *metadataAssembly

	// filePriorities is one entry per file, set from Config.FilePriorities
	// once metadata is known (openMetadata) and updated by SetFilePriority
	// after that. piecePriorities(mi, filePriorities) is what actually
	// drives the picker; this slice is the source of truth it's derived
	// from, since a single file's priority change needs the whole thing
	// recomputed (a piece can span several files).
	filePriorities []picker.Priority

	// pexKnownPeers is the addr-keyed snapshot of dialable peers (see
	// peerConn.peerInfo) as of the last PEX broadcast — broadcastPEX (pex.go)
	// diffs it against t.peers to compute each cycle's added/dropped lists.
	pexKnownPeers map[string]tracker.PeerInfo

	piecesVerifiedSinceCheckpoint int
	lastCheckpoint                time.Time

	// seedingStartedAt is when the current unbroken run of StateSeeding
	// began, zero when not currently seeding. seedingDuration accumulates
	// each such run's length as it ends (see setState), so
	// currentSeedingDuration's sum survives a Pause/Resume cycle within this
	// process — but not a restart, since neither field is in resume data.
	seedingStartedAt time.Time
	seedingDuration  time.Duration

	// announceCancel stops the currently-running announceLoop, if any. It
	// exists so Pause/Resume/Recheck can restart the loop cleanly instead of
	// accumulating a duplicate every cycle — see restartAnnounceLoop.
	announceCancel context.CancelFunc
	// dhtCancel is announceCancel's DHT-loop counterpart — see
	// restartDHTLoop in dht.go.
	dhtCancel context.CancelFunc
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

	// onSeedLimitReached is set before Run and never touched again, same as
	// onStateChange. See OnSeedLimitReached.
	onSeedLimitReached func()
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
		generate := tracker.GeneratePeerID
		if cfg.AnonymousMode {
			generate = tracker.GenerateAnonymousPeerID
		}
		id, err := generate()
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

	var chokerOpts []choker.Option
	if cfg.UploadSlots > 0 {
		chokerOpts = append(chokerOpts, choker.WithSlots(cfg.UploadSlots))
	}

	// HTTP(S) tracker announces go through the same proxy as peer
	// connections (cfg.ProxyDialer, nil-safe — a nil Dialer here just
	// leaves httpClient nil too, and tracker.NewClient(nil) already means
	// "use a plain default client"). UDP trackers are not proxied — see
	// Config.ProxyDialer's own doc comment.
	var httpClient *http.Client
	if cfg.ProxyDialer != nil {
		httpClient = &http.Client{Transport: &http.Transport{DialContext: cfg.ProxyDialer.DialContext}}
	}

	t := &Torrent{
		infoHash:      hash,
		cfg:           cfg,
		trackerClient: tracker.NewClient(httpClient),
		peers:         make(map[string]*peerConn),
		dialing:       make(map[string]bool),
		pexKnownPeers: make(map[string]tracker.PeerInfo),
		choke:         choker.New(chokerOpts...),
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

// OurID is this client's peer ID for this torrent — Config.OurID as given,
// or whatever newTorrent generated in its place. cfg is never mutated
// after construction, so this is safe to call from any goroutine.
func (t *Torrent) OurID() [20]byte { return t.cfg.OurID }

// Metadata returns the parsed .torrent info, or nil if it is not known yet.
func (t *Torrent) Metadata() *metainfo.MetaInfo { return t.mi.Load() }

// ContentPath is the on-disk root of this torrent's data: the file itself
// for a single-file torrent, or the directory holding every file for a
// multi-file one (see storage.Layout) — recomputed from metadata and
// Config rather than read off the actor-owned storage field, so it is safe
// to call from any goroutine at any time, not just ones the actor itself
// spawned. Empty until metadata is known.
func (t *Torrent) ContentPath() string {
	mi := t.mi.Load()
	if mi == nil {
		return ""
	}
	layout, err := storage.NewLayout(t.cfg.DownloadDir, mi.Info.Name, mi.Info.IsMultiFile(), t.cfg.ContentLayout)
	if err != nil {
		return ""
	}
	if mi.Info.IsMultiFile() {
		return layout.Base()
	}
	path, err := layout.Resolve(nil)
	if err != nil {
		return ""
	}
	return path
}

// State is the current lifecycle state.
func (t *Torrent) State() State { return State(t.state.Load()) }

// OnStateChange installs a callback fired from the actor goroutine on every
// transition. It must not block or call back into the Torrent. Must be set
// before Run.
func (t *Torrent) OnStateChange(fn func(State)) { t.onStateChange = fn }

// OnSeedLimitReached installs a callback fired once each time
// Config.SeedRatioLimit/SeedTimeLimit trips (3.4) — after the automatic
// pause that always happens first, regardless of what fn goes on to do.
// Fired from checkSeedLimits, which runs on the actor's own tick
// goroutine, so like OnStateChange it must not block or call back into
// this Torrent synchronously. Must be set before Run. Engine uses this to
// implement remove/remove-and-delete-data as an action beyond pausing.
func (t *Torrent) OnSeedLimitReached(fn func()) { t.onSeedLimitReached = fn }

func (t *Torrent) setState(next State) {
	cur := t.State()
	if cur == next {
		return
	}
	if !cur.CanTransition(next) {
		logger.Warning.Printf("torrent %s: illegal transition %s -> %s (ignored)\n", t.infoHash, cur, next)
		return
	}
	switch {
	case next == StateSeeding:
		t.seedingStartedAt = time.Now()
	case cur == StateSeeding:
		t.seedingDuration += time.Since(t.seedingStartedAt)
		t.seedingStartedAt = time.Time{}
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
		s.FilePriorities = fromActor.FilePriorities
		s.SeedRatio = fromActor.SeedRatio
		s.SeedingDuration = fromActor.SeedingDuration
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
	} else {
		t.setState(StateFetchingMetadata)
	}
	// Started unconditionally: a magnet-link torrent announces to its
	// magnet-supplied trackers (Config.Trackers) from the moment it starts,
	// and the same loop picks up mi.AnnounceURLs() once metadata arrives —
	// see announceLoop's comment.
	t.restartAnnounceLoop(tracker.EventStarted)
	t.restartDHTLoop()

	t.run(t.ctx)

	t.shutdownPeers()
	if t.mi.Load() != nil {
		t.checkpoint()
	}
	t.announceOnce(tracker.EventStopped, shutdownAnnounceT)
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
	t.filePriorities = normalizedFilePriorities(mi, t.cfg.FilePriorities)
	skip := make([]bool, len(t.filePriorities))
	for i, pr := range t.filePriorities {
		skip[i] = pr == picker.PrioritySkip
	}

	st, err := storage.New(t.cfg.DownloadDir, mi,
		storage.WithAllocation(t.cfg.Allocation),
		storage.WithContentLayout(t.cfg.ContentLayout),
		storage.WithSkipFiles(skip))
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
	pp := piecePriorities(mi, t.filePriorities)
	if t.cfg.FirstLastPieceFirst {
		boostFirstAndLastPiece(mi, t.filePriorities, pp)
	}
	if err := pk.SetPriorities(pp); err != nil {
		return fmt.Errorf("applying file priorities: %w", err)
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

// SetFilePriority changes one file's download priority, identified by its
// index into the torrent's own file list (a single-file torrent has
// exactly one, index 0). It blocks until the change has taken effect: every
// piece's effective priority recomputed, and — for a file newly moved off
// PrioritySkip that was never allocated — that allocation actually done, so
// a caller that gets a nil error back knows requests for that file's pieces
// can start immediately. Requires metadata to already be known.
func (t *Torrent) SetFilePriority(fileIndex int, priority picker.Priority) error {
	resp := make(chan error, 1)
	select {
	case t.control <- controlMsg{kind: ctrlSetFilePriority, fileIndex: fileIndex, priority: priority, errReply: resp}:
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

// AddTracker adds url to this torrent's tracker list at runtime (3.6), as
// its own announce-list tier (BEP 12) — the announce loop picks it up on
// its next iteration (or immediately, if the torrent is Paused and later
// Resumed) without needing a restart. Not persisted anywhere: a torrent
// reloaded from the engine's manifest starts back with just what its own
// .torrent/magnet already specified — see ExportTorrentFile if the point is
// to keep it.
func (t *Torrent) AddTracker(url string) error {
	resp := make(chan error, 1)
	select {
	case t.control <- controlMsg{kind: ctrlAddTracker, trackerURL: url, errReply: resp}:
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

// ExportTorrentFile reconstructs a standalone .torrent file's raw bytes
// from this torrent's current metadata and tracker list (including
// anything AddTracker has added) — see metainfo.Export. This is what lets
// a magnet-added torrent be saved as an ordinary .torrent once its
// metadata is known. Returns metainfo.ErrNoMetadata before that. Safe to
// call from any goroutine: mi and extraTrackers are both published via
// atomic.Pointer specifically so reads like this one never need to go
// through the actor.
func (t *Torrent) ExportTorrentFile() ([]byte, error) {
	mi := t.mi.Load()
	if mi == nil {
		return nil, metainfo.ErrNoMetadata
	}
	var trackers []string
	if p := t.extraTrackers.Load(); p != nil {
		trackers = *p
	}
	return metainfo.Export(mi, trackers)
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

// AcceptPeer hands off an inbound connection whose handshake the caller has
// already read (the engine's shared listener, which must read it to learn
// the infohash and route the connection to this torrent in the first
// place). It does not block; if the torrent has already stopped, conn is
// closed instead of leaking.
func (t *Torrent) AcceptPeer(conn net.Conn, hs *peer.Handshake) {
	select {
	case t.events <- eventIncomingPeer{conn: conn, hs: hs}:
	case <-t.done:
		conn.Close()
	}
}
