// Package engine manages a fleet of torrents: adding, listing, and removing
// them as a set, and persisting that set across restarts. internal/torrent
// already proved a single process can run many independent Torrent actors
// side by side (TestConcurrentTorrents); Engine is the layer that tracks
// which torrents exist, so a caller (the CLI, or eventually a daemon) does
// not have to.
package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Oblutack/GoTorrent/internal/dht"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/lsd"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/picker"
	"github.com/Oblutack/GoTorrent/internal/portmap"
	"github.com/Oblutack/GoTorrent/internal/ratelimit"
	"github.com/Oblutack/GoTorrent/internal/storage"
	"github.com/Oblutack/GoTorrent/internal/torrent"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// maxInboundPerIP caps how many concurrent inbound connections one source IP
// may hold open at once, so a single misbehaving or hostile address cannot
// exhaust this process's connection slots, goroutines, or file descriptors
// by opening connection after connection. Deliberately generous: a real peer
// can legitimately hold several connections open at once across different
// torrents this engine manages.
const maxInboundPerIP = 8

// DefaultStateDir returns the directory an Engine's manifest lives in when a
// caller has no preference, mirroring torrent.ResumeDir: a per-user config
// directory, portable across OSes via os.UserConfigDir.
func DefaultStateDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("engine: could not locate a config directory: %w", err)
	}
	return filepath.Join(base, "GoTorrent", "engine"), nil
}

// Defaults configures every torrent the Engine starts. DownLimit and UpLimit
// are typically shared *ratelimit.Limiter instances across the whole fleet,
// so the cap bounds the process's total transfer rate rather than each
// torrent independently.
type Defaults struct {
	DownloadDir string
	ResumeDir   string
	ListenPort  uint16
	// BindAddress restricts the inbound TCP listener (Listen/
	// ListenRandomPort) to one local address — a specific interface's IP,
	// or "127.0.0.1" to refuse anything but loopback connections. Empty
	// (the default) listens on every interface, same as before this field
	// existed.
	BindAddress    string
	Allocation     storage.Allocation
	PickerStrategy picker.Strategy
	DownLimit      *ratelimit.Limiter
	UpLimit        *ratelimit.Limiter
	// SeedRatioLimit and SeedTimeLimit apply to every torrent this Engine
	// starts — see torrent.Config for what each does. Zero (the default)
	// means unlimited, for both.
	SeedRatioLimit float64
	SeedTimeLimit  time.Duration
	// FirstLastPieceFirst applies to every torrent this Engine starts — see
	// torrent.Config.FirstLastPieceFirst.
	FirstLastPieceFirst bool
	// MaxActiveDownloads, MaxActiveSeeds, and MaxActiveTotal cap how many
	// managed torrents may be Downloading, Seeding, or either at once — see
	// queue.go. 0 (the default, for each independently) means unlimited.
	MaxActiveDownloads int
	MaxActiveSeeds     int
	MaxActiveTotal     int
}

// Summary is a point-in-time view of one managed torrent, safe to read from
// any goroutine.
type Summary struct {
	InfoHash metainfo.Hash
	// Source is what Add was given: a .torrent file path, or a magnet: URI.
	Source string
	// Name is the best name available: the verified name from metadata once
	// known (Torrent.Metadata().Info.Name), else a magnet's dn= hint, else
	// the infohash. The dn= case is a hint from whoever authored the magnet,
	// not verified against anything — display it as such, don't treat it as
	// authoritative.
	Name        string
	DownloadDir string
	Stats       torrent.Stats
	// Private is BEP 27's info.private flag, once metadata reveals it —
	// always false for a magnet-shaped torrent whose metadata hasn't
	// arrived yet, which is also why nothing here needs to react to it
	// changing: DHT/PEX/LSD's own private-torrent gating (torrent.dhtLoop,
	// broadcastPEX, onPEXUpdate, engine's LSD loops) all re-check
	// Info.Private directly against live metadata on every cycle rather
	// than trusting a value cached here — this field exists purely to let
	// a caller (the CLI today, any future UI) show the user which torrents
	// BEP 27 applies to, not to drive any behavior itself.
	Private bool
	// QueuePosition and ForceStart are the queue's view of this torrent —
	// see queue.go. QueuePosition is assigned in Add order and only ever
	// meaningful relative to other managed torrents' positions; it is not
	// persisted across a restart (same known-gap shape as 3.2/3.4's runtime
	// state), so a reload starts everyone back at Add order.
	QueuePosition int
	ForceStart    bool
}

// managedTorrent is what the Engine tracks per torrent beyond what Torrent
// itself already knows — the source, resolved download directory, and (for
// a magnet with no metadata yet) its display-name hint need to survive a
// restart, so they are exactly what the manifest records.
type managedTorrent struct {
	t           *torrent.Torrent
	source      string
	downloadDir string
	displayName string

	// queuePos, forceStart, and queueHeld are queue.go's bookkeeping — see
	// its package doc comment for what each means and reevaluateQueue for
	// how they're used. All three are guarded by Engine.mu like every other
	// managedTorrent field.
	queuePos   int
	forceStart bool
	queueHeld  bool
}

// displayNameFor picks the best name available for mt — see Summary.Name.
func displayNameFor(mt *managedTorrent) string {
	if mi := mt.t.Metadata(); mi != nil {
		return mi.Info.Name
	}
	if mt.displayName != "" {
		return mt.displayName
	}
	return mt.t.InfoHash().String()
}

// Engine owns a set of running torrents and the manifest that lets them
// survive a process restart. It is safe for concurrent use.
type Engine struct {
	mu       sync.Mutex
	stateDir string
	defaults Defaults
	torrents map[metainfo.Hash]*managedTorrent

	// listener is non-nil once Listen has bound a port, so Shutdown knows to
	// close it. Guarded by mu like everything else here.
	listener net.Listener

	// dhtNode is non-nil once StartDHT has bound a UDP socket. One node is
	// shared by every torrent this engine manages — see StartDHT and
	// torrentConfig — the same reasoning as one shared TCP listener in
	// Listen: a DHT node is a property of the process, not of one torrent.
	dhtNode *dht.DHT

	// portmapClient is non-nil once StartPortMapping has successfully
	// mapped a port through the local NAT (UPnP or NAT-PMP — see
	// internal/portmap). Nil just means no gateway was found or mapping
	// failed, which is not fatal: inbound connections still work if the
	// port is already reachable some other way.
	portmapClient *portmap.Client

	// lsdNode is non-nil once StartLSD has joined the multicast group — see
	// StartLSD. One node for the whole fleet, same reasoning as dhtNode and
	// listener: LSD is a single multicast socket, not a per-torrent thing.
	lsdNode *lsd.LSD

	// inboundMu and inboundCounts implement maxInboundPerIP, tracking how
	// many inbound connections are currently open per source IP.
	inboundMu     sync.Mutex
	inboundCounts map[string]int

	// nextQueuePos assigns each newly-Added torrent's initial queue
	// position — see queue.go.
	nextQueuePos int
}

// New creates an Engine whose manifest lives under stateDir. It does not load
// any previously-persisted torrents; call Load for that.
func New(stateDir string, defaults Defaults) (*Engine, error) {
	if stateDir == "" {
		return nil, errors.New("engine: state directory is required")
	}
	return &Engine{
		stateDir: stateDir,
		defaults: defaults,
		torrents: make(map[metainfo.Hash]*managedTorrent),
	}, nil
}

// Add starts a torrent running under the engine's management from either a
// .torrent file path or a magnet: URI — anything metainfo.ParseMagnet
// recognises by its "magnet:" prefix is treated as the latter. downloadDir
// overrides the engine's default for this torrent alone; pass "" to use the
// default. The torrent is persisted to the manifest before it is started, so
// Add either leaves the fleet exactly as it was or commits both the
// in-memory and on-disk state together.
func (e *Engine) Add(source, downloadDir string) (metainfo.Hash, error) {
	var (
		hash     metainfo.Hash
		mi       *metainfo.MetaInfo
		trackers []string
		dn       string
	)

	if strings.HasPrefix(source, "magnet:") {
		m, err := metainfo.ParseMagnet(source)
		if err != nil {
			return metainfo.Hash{}, fmt.Errorf("engine: parsing magnet: %w", err)
		}
		hash, trackers, dn = m.InfoHash, m.Trackers, m.DisplayName
	} else {
		loaded, err := metainfo.Load(source)
		if err != nil {
			return metainfo.Hash{}, fmt.Errorf("engine: loading %s: %w", source, err)
		}
		mi, hash = loaded, loaded.InfoHash
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.torrents[hash]; exists {
		return hash, fmt.Errorf("engine: %s is already added", hash)
	}
	if downloadDir == "" {
		downloadDir = e.defaults.DownloadDir
	}
	if downloadDir == "" {
		return metainfo.Hash{}, errors.New("engine: no download directory given and no default configured")
	}

	cfg := e.torrentConfig(downloadDir)
	cfg.Trackers = trackers

	var tr *torrent.Torrent
	var err error
	if mi != nil {
		tr, err = torrent.New(mi, cfg)
	} else {
		tr, err = torrent.NewFromInfoHash(hash, cfg)
	}
	if err != nil {
		return metainfo.Hash{}, fmt.Errorf("engine: creating torrent: %w", err)
	}

	mt := &managedTorrent{t: tr, source: source, downloadDir: downloadDir, displayName: dn, queuePos: e.nextQueuePos}
	e.nextQueuePos++
	e.torrents[hash] = mt

	if err := e.saveManifestLocked(); err != nil {
		delete(e.torrents, hash)
		return metainfo.Hash{}, fmt.Errorf("engine: persisting manifest: %w", err)
	}

	// Must be set before Run (see OnStateChange's own doc comment), and must
	// never call back into tr itself from the actor goroutine it fires on —
	// reevaluateQueue can Pause/Resume tr, which would deadlock if run
	// synchronously here, so this only ever schedules it to run separately.
	tr.OnStateChange(func(torrent.State) { go e.reevaluateQueue() })

	go func() {
		if err := tr.Run(context.Background()); err != nil {
			logger.Error.Printf("engine: torrent %s: %v\n", hash, err)
		}
	}()

	return hash, nil
}

// Load reloads every torrent recorded in the manifest, e.g. at process
// startup. A torrent that fails to load is logged and skipped rather than
// aborting the rest of the fleet — one moved or deleted .torrent file
// shouldn't take every other torrent down with it.
func (e *Engine) Load() error {
	entries, err := e.readManifest()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("engine: reading manifest: %w", err)
	}
	for _, ent := range entries {
		if _, err := e.Add(ent.Source, ent.DownloadDir); err != nil {
			logger.Warning.Printf("engine: could not reload %s: %v\n", ent.Source, err)
		}
	}
	return nil
}

// Remove stops a managed torrent for good and drops it from the manifest.
// It blocks until the torrent has actually shut down.
func (e *Engine) Remove(hash metainfo.Hash) error {
	e.mu.Lock()
	mt, ok := e.torrents[hash]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("engine: %s is not managed by this engine", hash)
	}
	delete(e.torrents, hash)
	if err := e.saveManifestLocked(); err != nil {
		e.torrents[hash] = mt // keep in-memory and on-disk state consistent
		e.mu.Unlock()
		return fmt.Errorf("engine: persisting manifest: %w", err)
	}
	e.mu.Unlock()

	// Stop blocks on the torrent's own shutdown; it must not be called while
	// holding e.mu; List/Get calls a Remove-in-progress torrent would
	// otherwise deadlock behind still need to work.
	mt.t.Stop()
	// A removed torrent may have been occupying a slot a queued one was
	// waiting on; reconcile promptly rather than waiting for the periodic
	// safety-net pass (see queue.go).
	e.reevaluateQueue()
	return nil
}

// Get returns the managed Torrent for hash, if any, for callers that need
// direct access (Pause/Resume/DialPeer/Stats beyond the Summary).
func (e *Engine) Get(hash metainfo.Hash) (*torrent.Torrent, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	mt, ok := e.torrents[hash]
	if !ok {
		return nil, false
	}
	return mt.t, true
}

// List returns a snapshot of every managed torrent, ordered by infohash for
// a stable, deterministic listing.
func (e *Engine) List() []Summary {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]Summary, 0, len(e.torrents))
	for hash, mt := range e.torrents {
		var private bool
		if mi := mt.t.Metadata(); mi != nil {
			private = mi.Info.Private
		}
		out = append(out, Summary{
			InfoHash:      hash,
			Source:        mt.source,
			Name:          displayNameFor(mt),
			DownloadDir:   mt.downloadDir,
			Stats:         mt.t.Stats(),
			Private:       private,
			QueuePosition: mt.queuePos,
			ForceStart:    mt.forceStart,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InfoHash.String() < out[j].InfoHash.String() })
	return out
}

// Listen opens a single TCP listener shared by every torrent this engine
// manages and starts routing inbound connections in the background: one
// listener per process, not one per torrent, because only something that
// knows about the whole fleet can read a connection's handshake and decide
// which torrent's infohash it matches. It returns once the port is bound (so
// a caller learns synchronously whether the port was available); accepting
// happens in a spawned goroutine that runs until ctx is cancelled or
// Shutdown closes the listener. A zero ListenPort in Defaults means "don't
// listen" and is a no-op, so callers that never configured a port don't need
// to guard this call themselves.
func (e *Engine) Listen(ctx context.Context) error {
	if e.defaults.ListenPort == 0 {
		return nil
	}
	_, err := e.listenOn(ctx, e.defaults.ListenPort)
	return err
}

// ListenRandomPort behaves like Listen but always binds a port the OS picks
// rather than reading Defaults.ListenPort, and returns whichever port that
// turned out to be — every subsequently-built torrent Config.ListenPort
// (and any later StartDHT/StartPortMapping/StartLSD call, since those also
// bind their own ports) needs to use exactly that number, so the caller
// (cmd/gottrent's -random-port flag) must sequence this before any of them.
func (e *Engine) ListenRandomPort(ctx context.Context) (uint16, error) {
	return e.listenOn(ctx, 0)
}

// listenOn does the real binding for both Listen and ListenRandomPort. It
// always writes the actually-bound port back into Defaults.ListenPort —
// a no-op for Listen's fixed-port case (net.Listen binds exactly what was
// asked or fails), the entire point for ListenRandomPort's port-0 case.
func (e *Engine) listenOn(ctx context.Context, port uint16) (uint16, error) {
	addr := e.defaults.BindAddress + ":" + strconv.Itoa(int(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("engine: listening on %s: %w", addr, err)
	}
	actual := uint16(ln.Addr().(*net.TCPAddr).Port)

	e.mu.Lock()
	e.listener = ln
	e.defaults.ListenPort = actual
	e.mu.Unlock()

	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go e.acceptLoop(ln)
	return actual, nil
}

// acceptLoop accepts connections until ln is closed (by ctx cancellation or
// Shutdown), handing each one off to its own goroutine so a slow or stalled
// handshake from one peer cannot delay accepting the next.
func (e *Engine) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go e.handleIncoming(conn)
	}
}

// handleIncoming reads just enough of an inbound connection to route it: the
// handshake, which carries the infohash. A torrent this engine does not
// manage, or a malformed handshake, just gets the connection closed — there
// is nobody to hand it to. Enforces maxInboundPerIP before doing anything
// else with the connection, including the handshake read, so a source IP
// already at its limit cannot even tie up a goroutine reading from it.
func (e *Engine) handleIncoming(conn net.Conn) {
	ip := remoteIP(conn)
	if !e.reserveInboundSlot(ip) {
		logger.Logf("engine: %s already has %d inbound connections, closing this one\n", ip, maxInboundPerIP)
		conn.Close()
		return
	}
	// countedConn's Close releases the slot exactly once, however the
	// connection eventually ends: rejected here for a bad handshake,
	// rejected by acceptIncoming's own dedup/cap check, or (the common
	// case) closed much later by peer.Client once the torrent actor is done
	// with it. The engine loses visibility into the connection the moment
	// AcceptPeer hands it off, so this is the only point that can reliably
	// free the slot.
	conn = &countedConn{Conn: conn, release: func() { e.releaseInboundSlot(ip) }}

	hs, err := peer.ReadHandshake(conn)
	if err != nil {
		conn.Close()
		return
	}

	e.mu.Lock()
	mt, ok := e.torrents[metainfo.Hash(hs.InfoHash)]
	e.mu.Unlock()
	if !ok {
		logger.Logf("engine: inbound connection from %s for unmanaged torrent %x, closing\n",
			conn.RemoteAddr(), hs.InfoHash)
		conn.Close()
		return
	}
	mt.t.AcceptPeer(conn, hs)
}

// remoteIP returns just the host part of conn's remote address, so
// maxInboundPerIP counts by IP rather than by IP:port (every connection has
// a distinct source port, which would make the limit meaningless).
func remoteIP(conn net.Conn) string {
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return conn.RemoteAddr().String()
	}
	return host
}

func (e *Engine) reserveInboundSlot(ip string) bool {
	e.inboundMu.Lock()
	defer e.inboundMu.Unlock()
	if e.inboundCounts == nil {
		e.inboundCounts = make(map[string]int)
	}
	if e.inboundCounts[ip] >= maxInboundPerIP {
		return false
	}
	e.inboundCounts[ip]++
	return true
}

func (e *Engine) releaseInboundSlot(ip string) {
	e.inboundMu.Lock()
	defer e.inboundMu.Unlock()
	e.inboundCounts[ip]--
	if e.inboundCounts[ip] <= 0 {
		delete(e.inboundCounts, ip)
	}
}

// countedConn wraps an inbound net.Conn so closing it — from anywhere, any
// number of times — releases its maxInboundPerIP slot exactly once.
type countedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *countedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// StartDHT brings up the mainline DHT node shared by every torrent this
// engine manages that wants one (anything not private, per BEP 27 — see
// torrent.dhtLoop), then begins bootstrapping it against the well-known
// public routers in the background. It returns once the UDP socket is
// bound; bootstrapping and ongoing lookups continue after that. A zero port
// means "don't start DHT" and is a no-op, matching Listen's convention for
// the TCP side.
func (e *Engine) StartDHT(ctx context.Context, port uint16) error {
	if port == 0 {
		return nil
	}

	var statePath string
	if e.stateDir != "" {
		statePath = filepath.Join(e.stateDir, "dht.nodes")
	}
	node, err := dht.New(dht.Config{Port: port, StatePath: statePath})
	if err != nil {
		return fmt.Errorf("engine: starting DHT: %w", err)
	}

	e.mu.Lock()
	e.dhtNode = node
	e.mu.Unlock()

	go func() {
		<-ctx.Done()
		node.Close()
	}()
	go node.Bootstrap(ctx, dht.DefaultBootstrapNodes)
	return nil
}

// StartPortMapping attempts to open internalPort through the local NAT via
// UPnP or NAT-PMP (see internal/portmap), keeping the mapping alive and
// renewed for as long as ctx is not cancelled. On success, every
// subsequently-built torrent Config.ListenPort — and so every tracker
// announce and DHT node this engine advertises — carries the external port
// the gateway actually granted (usually, but not guaranteed to be, the same
// number) rather than the internal one: trackers and DHT peers need the
// address the outside world can actually reach, not the one this process
// happens to have bound. Torrents already running when mapping succeeds are
// not retroactively updated — call this before Load/Add, as
// cmd/gottrent does, for it to matter.
//
// Failure is not fatal and is returned rather than logged here: this is a
// best-effort convenience for the common "behind a home router with
// UPnP/NAT-PMP enabled" case, not a requirement — a manually port-forwarded
// or publicly-routed setup works fine without it, so the caller decides
// whether and how loudly to report it (see cmd/gottrent). A zero port means
// "don't attempt mapping" and is a no-op, matching Listen and StartDHT's
// convention.
func (e *Engine) StartPortMapping(ctx context.Context, internalPort uint16) error {
	if internalPort == 0 {
		return nil
	}
	client, mapping, err := portmap.Start(ctx, "TCP", internalPort)
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.portmapClient = client
	if mapping.ExternalPort != 0 {
		e.defaults.ListenPort = mapping.ExternalPort
	}
	e.mu.Unlock()

	logger.Logf("engine: mapped external port %d -> internal %d via %s (external IP %s)\n",
		mapping.ExternalPort, internalPort, client.GatewayKind(), mapping.ExternalIP)
	return nil
}

// StartLSD joins the local multicast group (BEP 14) and starts announcing
// every non-private managed torrent's infohash on internalPort every
// lsd.AnnounceInterval, while dispatching whatever peers other local nodes
// announce back to the matching managed torrent via DialPeer. internalPort
// is deliberately what LSD advertises, never StartPortMapping's rewritten
// external port: an LSD peer is on the same LAN by definition and connects
// directly to this machine's local address, not through any NAT mapping.
// A zero port means "don't start LSD" and is a no-op, matching Listen and
// StartDHT's convention.
func (e *Engine) StartLSD(ctx context.Context, internalPort uint16) error {
	if internalPort == 0 {
		return nil
	}
	node, err := lsd.New()
	if err != nil {
		return fmt.Errorf("engine: starting LSD: %w", err)
	}

	e.mu.Lock()
	e.lsdNode = node
	e.mu.Unlock()

	go func() {
		<-ctx.Done()
		node.Close()
	}()
	go e.lsdAnnounceLoop(ctx, node, internalPort)
	go e.lsdDispatchLoop(node.Found())
	return nil
}

// lsdAnnounceLoop announces every non-private managed torrent once
// immediately (no reason to wait a full interval for the very first one)
// and then on lsd.AnnounceInterval's cadence for as long as ctx allows.
func (e *Engine) lsdAnnounceLoop(ctx context.Context, node *lsd.LSD, port uint16) {
	e.lsdAnnounceOnce(node, port)
	ticker := time.NewTicker(lsd.AnnounceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.lsdAnnounceOnce(node, port)
		}
	}
}

func (e *Engine) lsdAnnounceOnce(node *lsd.LSD, port uint16) {
	for _, hash := range e.lsdAnnounceableHashes() {
		if err := node.Announce(hash, port); err != nil {
			logger.Logf("engine: LSD announce for %s: %v\n", hash, err)
		}
	}
}

// lsdAnnounceableHashes is every managed torrent's infohash except private
// ones (BEP 27: never advertise those over LSD) — split out from
// lsdAnnounceOnce so the filtering logic is testable without a real
// multicast socket.
func (e *Engine) lsdAnnounceableHashes() []metainfo.Hash {
	e.mu.Lock()
	defer e.mu.Unlock()
	hashes := make([]metainfo.Hash, 0, len(e.torrents))
	for hash, mt := range e.torrents {
		if mi := mt.t.Metadata(); mi != nil && mi.Info.Private {
			continue
		}
		hashes = append(hashes, hash)
	}
	return hashes
}

// lsdDispatchLoop hands every peer LSD hears about to whichever managed
// torrent its infohash matches, if any — the LSD equivalent of
// handleIncoming routing an inbound TCP connection by infohash. Takes the
// channel rather than a *lsd.LSD directly so it can be driven by a fake one
// in tests, without a real multicast socket. Runs until found closes.
func (e *Engine) lsdDispatchLoop(found <-chan lsd.PeerFound) {
	for f := range found {
		e.mu.Lock()
		mt, ok := e.torrents[metainfo.Hash(f.InfoHash)]
		e.mu.Unlock()
		if !ok {
			continue
		}
		if mi := mt.t.Metadata(); mi != nil && mi.Info.Private {
			continue // BEP 27: ignore even an unsolicited LSD peer for a private torrent
		}
		mt.t.DialPeer(tracker.PeerInfo{IP: f.Addr.IP, Port: uint16(f.Addr.Port)})
	}
}

// Shutdown stops every managed torrent and waits for all of them to finish.
// Torrents are stopped concurrently — Stop is documented safe to call from
// any goroutine — so shutting down N torrents costs the slowest one, not the
// sum of all of them.
func (e *Engine) Shutdown() {
	e.mu.Lock()
	if e.listener != nil {
		e.listener.Close()
		e.listener = nil
	}
	dhtNode := e.dhtNode
	e.dhtNode = nil
	portmapClient := e.portmapClient
	e.portmapClient = nil
	lsdNode := e.lsdNode
	e.lsdNode = nil
	torrents := make([]*torrent.Torrent, 0, len(e.torrents))
	for _, mt := range e.torrents {
		torrents = append(torrents, mt.t)
	}
	e.mu.Unlock()

	// dhtNode.Close can take up to ~1s (its read loop polls its done channel
	// on a 1s deadline), and portmapClient.Close makes a final network round
	// trip (bounded at 5s) to withdraw the mapping — both worth doing off
	// the lock, and concurrently with stopping every torrent, rather than
	// serially in front of them.
	var wg sync.WaitGroup
	if dhtNode != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dhtNode.Close()
		}()
	}
	if portmapClient != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			portmapClient.Close()
		}()
	}
	if lsdNode != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lsdNode.Close()
		}()
	}
	wg.Add(len(torrents))
	for _, tr := range torrents {
		go func(tr *torrent.Torrent) {
			defer wg.Done()
			tr.Stop()
		}(tr)
	}
	wg.Wait()
}

func (e *Engine) torrentConfig(downloadDir string) torrent.Config {
	cfg := torrent.Config{
		DownloadDir:         downloadDir,
		ResumeDir:           e.defaults.ResumeDir,
		ListenPort:          e.defaults.ListenPort,
		Allocation:          e.defaults.Allocation,
		PickerStrategy:      e.defaults.PickerStrategy,
		DownLimit:           e.defaults.DownLimit,
		UpLimit:             e.defaults.UpLimit,
		SeedRatioLimit:      e.defaults.SeedRatioLimit,
		SeedTimeLimit:       e.defaults.SeedTimeLimit,
		FirstLastPieceFirst: e.defaults.FirstLastPieceFirst,
	}
	// Only assign when non-nil: cfg.DHT is a torrent.DHTClient interface, and
	// assigning a nil *dht.DHT to it would leave the interface non-nil (it
	// would hold a nil pointer, not be nil itself) — the classic Go gotcha —
	// which would make dhtLoop's "cfg.DHT == nil means disabled" check pass
	// right up until the first real method call panicked.
	if e.dhtNode != nil {
		cfg.DHT = e.dhtNode
	}
	return cfg
}
