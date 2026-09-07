package dht

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

const (
	// queryTimeout bounds one KRPC round trip. UDP has no delivery guarantee
	// and no connection to notice a dead peer with, so every query needs its
	// own timeout rather than relying on the transport to fail fast.
	queryTimeout = 5 * time.Second

	// maxPacketSize is generous for a KRPC message — a find_node/get_peers
	// reply carrying 8 compact nodes is a few hundred bytes at most.
	maxPacketSize = 4096

	// peerTTL is how long a peer this node stored via announce_peer is kept
	// before expireStoredPeers drops it — long enough to survive several
	// re-announce cycles from a real client (see dhtReannounceInterval in
	// internal/torrent), short enough that a peer which vanished without a
	// goodbye is not handed out forever.
	peerTTL = 30 * time.Minute

	maintainInterval = 5 * time.Minute
)

// Config configures a DHT node.
type Config struct {
	// Port is the UDP port to listen on. 0 lets the OS pick an ephemeral
	// port — useful for tests, and for a future random-port option.
	Port uint16
	// StatePath, if set, is where the routing table is persisted between
	// runs: read once at New, written by the periodic maintenance sweep and
	// by Close. Empty disables persistence.
	StatePath string
}

// DHT is one BitTorrent mainline DHT node (BEP 5): a UDP socket, a Kademlia
// routing table, and the KRPC query/response machinery both directions of
// that socket need — this node answering other nodes' queries, and this
// node's own outgoing queries (used by the iterative lookups in lookup.go).
type DHT struct {
	id     NodeID
	conn   *net.UDPConn
	table  *Table
	tokens *tokenIssuer

	statePath string

	txMu      sync.Mutex
	txCounter uint32
	pending   map[string]chan *message // keyed by the raw transaction id

	// peers indexes infohashes this node has learned peers for via
	// announce_peer, so it can hand them back out of its own get_peers
	// responses — this node's slice of the DHT's distributed storage.
	peersMu sync.Mutex
	peers   map[NodeID][]peerEntry

	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup
}

type peerEntry struct {
	addr   *net.UDPAddr
	stored time.Time
}

// New binds a UDP socket and starts a DHT node on it. It does not block on
// bootstrapping the network — call Bootstrap separately, typically from a
// spawned goroutine, once New returns.
func New(cfg Config) (*DHT, error) {
	id, err := RandomNodeID()
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: int(cfg.Port)})
	if err != nil {
		return nil, fmt.Errorf("dht: listening on UDP port %d: %w", cfg.Port, err)
	}

	d := &DHT{
		id:        id,
		conn:      conn,
		table:     newTable(id),
		tokens:    newTokenIssuer(),
		statePath: cfg.StatePath,
		pending:   make(map[string]chan *message),
		peers:     make(map[NodeID][]peerEntry),
		done:      make(chan struct{}),
	}

	if cfg.StatePath != "" {
		if saved, err := loadState(cfg.StatePath); err == nil {
			for _, n := range saved {
				d.table.Insert(n.id, n.addr)
			}
		}
	}

	d.wg.Add(2)
	go d.readLoop()
	go d.maintain()

	return d, nil
}

// ID is this node's own identifier.
func (d *DHT) ID() NodeID { return d.id }

// Addr is the local address this node is listening on.
func (d *DHT) Addr() *net.UDPAddr { return d.conn.LocalAddr().(*net.UDPAddr) }

// NodeCount is how many contacts are currently in the routing table.
func (d *DHT) NodeCount() int { return d.table.Count() }

// Close shuts the node down: the socket is closed (unblocking readLoop), the
// maintenance goroutine stops, and — if StatePath was set — the routing
// table is saved one last time.
func (d *DHT) Close() error {
	d.closeOnce.Do(func() {
		close(d.done)
		d.conn.Close()
	})
	d.wg.Wait()
	if d.statePath != "" {
		d.saveState()
	}
	return nil
}

// readLoop is the only goroutine that ever reads the socket, dispatching
// each datagram to either a pending query's waiter or the query handler.
func (d *DHT) readLoop() {
	defer d.wg.Done()
	buf := make([]byte, maxPacketSize)
	for {
		d.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, addr, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-d.done:
				return
			default:
			}
			continue // a read timeout (the common case) or transient error
		}
		msg, err := parseMessage(buf[:n])
		if err != nil {
			continue // malformed packet: BEP 5 warrants no reply, just ignore it
		}
		d.handleMessage(msg, addr)
	}
}

func (d *DHT) handleMessage(msg *message, addr *net.UDPAddr) {
	switch msg.Y {
	case "r", "e":
		d.txMu.Lock()
		ch, ok := d.pending[msg.T]
		if ok {
			delete(d.pending, msg.T)
		}
		d.txMu.Unlock()
		if ok {
			ch <- msg
		}
		if msg.Y == "r" {
			var idr idResponse
			if err := bencode.Unmarshal(msg.R, &idr); err == nil {
				d.table.Insert(idr.ID, addr)
			}
		}
	case "q":
		d.handleQuery(msg, addr)
	}
}

// sendQuery sends one KRPC query and waits for its matching reply, retrying
// nothing itself — a failed or timed-out query is the caller's business
// (typically: mark the contact failed and move on, see lookup.go).
func (d *DHT) sendQuery(ctx context.Context, addr *net.UDPAddr, q string, args any) (*message, error) {
	t := d.nextTxID()
	payload, err := newQuery(t, q, args)
	if err != nil {
		return nil, err
	}

	ch := make(chan *message, 1)
	d.txMu.Lock()
	d.pending[t] = ch
	d.txMu.Unlock()
	defer func() {
		d.txMu.Lock()
		delete(d.pending, t)
		d.txMu.Unlock()
	}()

	if _, err := d.conn.WriteToUDP(payload, addr); err != nil {
		return nil, fmt.Errorf("dht: writing %s to %s: %w", q, addr, err)
	}

	timer := time.NewTimer(queryTimeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		if resp.Y == "e" {
			return nil, krpcErrorFromE(resp.E)
		}
		return resp, nil
	case <-timer.C:
		return nil, fmt.Errorf("dht: %s to %s timed out", q, addr)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-d.done:
		return nil, fmt.Errorf("dht: node closed")
	}
}

func (d *DHT) nextTxID() string {
	n := atomic.AddUint32(&d.txCounter, 1)
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], uint16(n))
	return string(b[:])
}

// --- outgoing queries (client side) ---------------------------------------

func (d *DHT) ping(ctx context.Context, addr *net.UDPAddr) (NodeID, error) {
	resp, err := d.sendQuery(ctx, addr, "ping", pingArgs{ID: d.id})
	if err != nil {
		return NodeID{}, err
	}
	var r idResponse
	if err := bencode.Unmarshal(resp.R, &r); err != nil {
		return NodeID{}, fmt.Errorf("dht: bad ping response from %s: %w", addr, err)
	}
	d.table.Insert(r.ID, addr)
	return r.ID, nil
}

func (d *DHT) findNode(ctx context.Context, addr *net.UDPAddr, target NodeID) ([]nodeAddr, error) {
	resp, err := d.sendQuery(ctx, addr, "find_node", findNodeArgs{ID: d.id, Target: target})
	if err != nil {
		return nil, err
	}
	var r findNodeResponse
	if err := bencode.Unmarshal(resp.R, &r); err != nil {
		return nil, fmt.Errorf("dht: bad find_node response from %s: %w", addr, err)
	}
	d.table.Insert(r.ID, addr)
	return parseCompactNodes(r.Nodes), nil
}

// getPeersResult is one node's answer to a get_peers query: either peers it
// has stored for the infohash, or the nodes closest to it that it knows
// about — plus, always, the token this node needs back to announce_peer
// here.
type getPeersResult struct {
	from  NodeID
	token string
	peers []tracker.PeerInfo
	nodes []nodeAddr
}

func (d *DHT) getPeers(ctx context.Context, addr *net.UDPAddr, infoHash NodeID) (*getPeersResult, error) {
	resp, err := d.sendQuery(ctx, addr, "get_peers", getPeersArgs{ID: d.id, InfoHash: infoHash})
	if err != nil {
		return nil, err
	}
	var r getPeersResponse
	if err := bencode.Unmarshal(resp.R, &r); err != nil {
		return nil, fmt.Errorf("dht: bad get_peers response from %s: %w", addr, err)
	}
	d.table.Insert(r.ID, addr)
	result := &getPeersResult{from: r.ID, token: r.Token, peers: parseCompactPeers(r.Values)}
	if len(r.Nodes) > 0 {
		result.nodes = parseCompactNodes(r.Nodes)
	}
	return result, nil
}

func (d *DHT) announcePeer(ctx context.Context, addr *net.UDPAddr, infoHash NodeID, port uint16, token string) error {
	_, err := d.sendQuery(ctx, addr, "announce_peer", announcePeerArgs{
		ID: d.id, InfoHash: infoHash, Port: int64(port), Token: token,
	})
	return err
}

// --- incoming queries (server side) ---------------------------------------

func (d *DHT) handleQuery(msg *message, addr *net.UDPAddr) {
	switch msg.Q {
	case "ping":
		var a pingArgs
		if bencode.Unmarshal(msg.A, &a) == nil {
			d.table.Insert(a.ID, addr)
		}
		d.reply(msg.T, addr, idResponse{ID: d.id})

	case "find_node":
		var a findNodeArgs
		if err := bencode.Unmarshal(msg.A, &a); err != nil {
			return
		}
		d.table.Insert(a.ID, addr)
		closest := d.table.FindClosest(a.Target, bucketSize)
		d.reply(msg.T, addr, findNodeResponse{ID: d.id, Nodes: encodeCompactNodes(closest)})

	case "get_peers":
		var a getPeersArgs
		if err := bencode.Unmarshal(msg.A, &a); err != nil {
			return
		}
		d.table.Insert(a.ID, addr)
		resp := getPeersResponse{ID: d.id, Token: d.tokens.issue(addr)}
		if peers := d.storedPeers(a.InfoHash); len(peers) > 0 {
			resp.Values = make([][]byte, len(peers))
			for i, p := range peers {
				resp.Values[i] = encodeCompactPeer(p.addr.IP, uint16(p.addr.Port))
			}
		} else {
			resp.Nodes = encodeCompactNodes(d.table.FindClosest(a.InfoHash, bucketSize))
		}
		d.reply(msg.T, addr, resp)

	case "announce_peer":
		var a announcePeerArgs
		if err := bencode.Unmarshal(msg.A, &a); err != nil {
			return
		}
		if !d.tokens.valid(addr, a.Token) {
			d.replyError(msg.T, addr, 203, "bad token")
			return
		}
		d.table.Insert(a.ID, addr)
		port := uint16(a.Port)
		if a.ImpliedPort != 0 {
			port = uint16(addr.Port)
		}
		d.storePeer(a.InfoHash, &net.UDPAddr{IP: addr.IP, Port: int(port)})
		d.reply(msg.T, addr, idResponse{ID: d.id})

	default:
		d.replyError(msg.T, addr, 204, "method unknown: "+msg.Q)
	}
}

func (d *DHT) reply(t string, addr *net.UDPAddr, r any) {
	payload, err := newResponse(t, r)
	if err != nil {
		logger.Warning.Printf("dht: encoding reply to %s: %v\n", addr, err)
		return
	}
	if _, err := d.conn.WriteToUDP(payload, addr); err != nil {
		logger.Logf("dht: replying to %s: %v\n", addr, err)
	}
}

func (d *DHT) replyError(t string, addr *net.UDPAddr, code int, msg string) {
	payload, err := newKRPCError(t, code, msg)
	if err != nil {
		return
	}
	d.conn.WriteToUDP(payload, addr)
}

func (d *DHT) storedPeers(infoHash NodeID) []peerEntry {
	d.peersMu.Lock()
	defer d.peersMu.Unlock()
	entries := d.peers[infoHash]
	out := make([]peerEntry, len(entries))
	copy(out, entries)
	return out
}

func (d *DHT) storePeer(infoHash NodeID, addr *net.UDPAddr) {
	d.peersMu.Lock()
	defer d.peersMu.Unlock()
	for i, e := range d.peers[infoHash] {
		if e.addr.IP.Equal(addr.IP) && e.addr.Port == addr.Port {
			d.peers[infoHash][i].stored = time.Now()
			return
		}
	}
	d.peers[infoHash] = append(d.peers[infoHash], peerEntry{addr: addr, stored: time.Now()})
}

// --- maintenance -----------------------------------------------------------

// maintain periodically resolves questionable contacts one way or the other
// (ping them; a failure moves them toward eviction, a reply refreshes them),
// expires stored peers past their TTL, and — if configured — checkpoints the
// routing table to disk.
func (d *DHT) maintain() {
	defer d.wg.Done()
	ticker := time.NewTicker(maintainInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.pingQuestionable()
			d.expireStoredPeers()
			if d.statePath != "" {
				d.saveState()
			}
		}
	}
}

func (d *DHT) pingQuestionable() {
	for _, n := range d.table.Questionable() {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		_, err := d.ping(ctx, n.addr)
		cancel()
		if err != nil {
			d.table.MarkFailed(n.id)
		}
	}
}

func (d *DHT) expireStoredPeers() {
	d.peersMu.Lock()
	defer d.peersMu.Unlock()
	for hash, entries := range d.peers {
		kept := entries[:0]
		for _, e := range entries {
			if time.Since(e.stored) < peerTTL {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(d.peers, hash)
		} else {
			d.peers[hash] = kept
		}
	}
}

func (d *DHT) saveState() {
	if err := saveState(d.statePath, d.table.All()); err != nil {
		logger.Warning.Printf("dht: saving routing table: %v\n", err)
	}
}
