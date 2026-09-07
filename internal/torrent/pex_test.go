package torrent

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// fakePexWire mirrors peer package's unexported utPexWire closely enough to
// interoperate: this fixture plays a real peer on the wire, so it needs its
// own encoding of BEP 11's ut_pex message, not access to peer's internals.
type fakePexWire struct {
	Added   []byte `bencode:"added,omitempty"`
	Dropped []byte `bencode:"dropped,omitempty"`
}

func encodeFakeCompactPeers(peers []tracker.PeerInfo) []byte {
	buf := make([]byte, 0, 6*len(peers))
	for _, p := range peers {
		entry := make([]byte, 6)
		copy(entry[0:4], p.IP.To4())
		binary.BigEndian.PutUint16(entry[4:6], p.Port)
		buf = append(buf, entry...)
	}
	return buf
}

func decodeFakeCompactPeers(raw []byte) []tracker.PeerInfo {
	const entry = 6
	out := make([]tracker.PeerInfo, 0, len(raw)/entry)
	for off := 0; off+entry <= len(raw); off += entry {
		ip := make(net.IP, 4)
		copy(ip, raw[off:off+4])
		out = append(out, tracker.PeerInfo{IP: ip, Port: binary.BigEndian.Uint16(raw[off+4 : off+6])})
	}
	return out
}

// fakePEXPeer is a minimal real peer purpose-built for exercising BEP 11 end
// to end: it completes a real handshake and extended handshake advertising
// ut_pex under selfPexID, learns the real client's own advertised ut_pex id
// from its handshake (the same asymmetric-id dance torrent_test.go's
// fakeSeeder plays for ut_metadata), delivers every PEX message it receives
// on Received, and can send one back via SendPEX once connected.
type fakePEXPeer struct {
	t         *testing.T
	ln        net.Listener
	selfPexID int

	Received chan fakePexWire

	mu          sync.Mutex
	conn        net.Conn
	clientPexID int
	connReady   chan struct{}
}

func newFakePEXPeer(t *testing.T, selfPexID int) *fakePEXPeer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakePEXPeer{
		t: t, ln: ln, selfPexID: selfPexID,
		Received:  make(chan fakePexWire, 8),
		connReady: make(chan struct{}),
	}
	t.Cleanup(func() { ln.Close() })
	go f.acceptOne()
	return f
}

func (f *fakePEXPeer) peerInfo() tracker.PeerInfo {
	addr := f.ln.Addr().(*net.TCPAddr)
	return tracker.PeerInfo{IP: addr.IP, Port: uint16(addr.Port)}
}

func (f *fakePEXPeer) acceptOne() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	f.handle(conn)
}

func (f *fakePEXPeer) handle(conn net.Conn) {
	defer conn.Close()

	hs := make([]byte, 68)
	if _, err := io.ReadFull(conn, hs); err != nil {
		return
	}
	var infoHash [20]byte
	copy(infoHash[:], hs[28:48])
	var id [20]byte
	copy(id[:], "-PEXT01-fakepexpeer0")
	if _, err := conn.Write(peer.NewHandshake(infoHash, id).Serialize()); err != nil {
		return
	}
	// No Bitfield sent at all: this fixture only exists to be dialed and to
	// exchange PEX, not to serve pieces — and unlike torrent_test.go's
	// fakeSeeder (which advertises a real, correctly-sized bitfield for a
	// torrent it can actually serve), the real Torrent under test here
	// already knows its own NumPieces from real metadata, so anything but a
	// correctly-sized Bitfield would be a protocol violation and get this
	// connection dropped. BEP 3 permits skipping Bitfield when a peer has
	// nothing to offer, which this fixture never claims to.
	if err := writeMsg(conn, peer.MsgUnchoke, nil); err != nil {
		return
	}

	ourHS := struct {
		M map[string]int `bencode:"m"`
	}{M: map[string]int{"ut_pex": f.selfPexID}}
	body, err := bencode.Marshal(ourHS)
	if err != nil {
		f.t.Errorf("marshal fake extended handshake: %v", err)
		return
	}
	if err := writeMsg(conn, peer.MsgExtended, append([]byte{0}, body...)); err != nil {
		return
	}

	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()

	for {
		id, payload, err := readMsg(conn)
		if err != nil {
			return
		}
		if id != peer.MsgExtended || len(payload) < 1 {
			continue
		}
		extID, body := payload[0], payload[1:]
		if extID == 0 {
			var hs struct {
				M map[string]int `bencode:"m"`
			}
			if bencode.Unmarshal(body, &hs) == nil {
				if id, ok := hs.M["ut_pex"]; ok {
					f.mu.Lock()
					f.clientPexID = id
					f.mu.Unlock()
					close(f.connReady)
				}
			}
			continue
		}
		if int(extID) != f.selfPexID {
			continue
		}
		var wire fakePexWire
		if bencode.Unmarshal(body, &wire) != nil {
			continue
		}
		select {
		case f.Received <- wire:
		default:
		}
	}
}

// SendPEX sends one ut_pex message to the real client, addressed by the id
// the client itself advertised — blocks until the client's extended
// handshake has actually arrived (connReady), so a test calling this
// immediately after DialPeer doesn't race the handshake.
func (f *fakePEXPeer) SendPEX(t *testing.T, added, dropped []tracker.PeerInfo) {
	t.Helper()
	select {
	case <-f.connReady:
	case <-time.After(5 * time.Second):
		t.Fatal("fakePEXPeer: client's extended handshake never arrived")
	}
	f.mu.Lock()
	conn, id := f.conn, f.clientPexID
	f.mu.Unlock()

	wire := fakePexWire{Added: encodeFakeCompactPeers(added), Dropped: encodeFakeCompactPeers(dropped)}
	body, err := bencode.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal fake ut_pex message: %v", err)
	}
	if err := writeMsg(conn, peer.MsgExtended, append([]byte{byte(id)}, body...)); err != nil {
		t.Fatalf("send fake ut_pex message: %v", err)
	}
}

func waitForPEXUpdate(t *testing.T, ch chan fakePexWire) fakePexWire {
	t.Helper()
	select {
	case wire := <-ch:
		return wire
	case <-time.After(5 * time.Second):
		t.Fatal("no PEX message arrived")
		return fakePexWire{}
	}
}

// TestPEXBroadcastsNewlyDialedPeersToEachOther connects a torrent to two
// peers by dialing both (DialPeer, exactly what a tracker/DHT/PEX discovery
// would also do), then proves the torrent's own periodic PEX broadcast
// tells each about the other.
func TestPEXBroadcastsNewlyDialedPeersToEachOther(t *testing.T) {
	orig := pexInterval
	pexInterval = 20 * time.Millisecond
	t.Cleanup(func() { pexInterval = orig })

	mi, _ := buildTorrent(t, "pex-a.bin", 16384, []fileSpec{{length: 16384}})
	a := newFakePEXPeer(t, 11)
	b := newFakePEXPeer(t, 22)

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	tr.DialPeer(a.peerInfo())
	tr.DialPeer(b.peerInfo())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && tr.Stats().PeerCount < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := tr.Stats().PeerCount; got != 2 {
		t.Fatalf("PeerCount = %d, want 2", got)
	}

	wireFromA := waitForPEXUpdate(t, a.Received)
	added := decodeFakeCompactPeers(wireFromA.Added)
	var sawB bool
	for _, p := range added {
		if p.Port == b.peerInfo().Port {
			sawB = true
		}
	}
	if !sawB {
		t.Fatalf("peer A's PEX message did not mention peer B: %+v", added)
	}

	wireFromB := waitForPEXUpdate(t, b.Received)
	added = decodeFakeCompactPeers(wireFromB.Added)
	var sawA bool
	for _, p := range added {
		if p.Port == a.peerInfo().Port {
			sawA = true
		}
	}
	if !sawA {
		t.Fatalf("peer B's PEX message did not mention peer A: %+v", added)
	}
}

// TestPEXReceivedUpdateDialsTheAddedPeer proves the receive side: a PEX
// message from a connected peer, naming a third peer the torrent has never
// heard of, results in the torrent dialing that third peer on its own.
func TestPEXReceivedUpdateDialsTheAddedPeer(t *testing.T) {
	mi, content := buildTorrent(t, "pex-receive.bin", 16384, []fileSpec{{length: 16384}})
	a := newFakePEXPeer(t, 33)
	c := newFakeSeeder(t, mi, content) // the peer PEX will introduce

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	tr.DialPeer(a.peerInfo())
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && tr.Stats().PeerCount < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := tr.Stats().PeerCount; got != 1 {
		t.Fatalf("PeerCount = %d after dialing A, want 1", got)
	}

	a.SendPEX(t, []tracker.PeerInfo{c.peerInfo()}, nil)

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && tr.Stats().PeerCount < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := tr.Stats().PeerCount; got != 2 {
		t.Fatalf("PeerCount = %d after A's PEX introduced C, want 2 (the torrent should have dialed C)", got)
	}
}

// TestPEXDisabledForPrivateTorrent proves BEP 27: a private torrent's actor
// never broadcasts PEX, even with peers connected and support advertised on
// both sides.
func TestPEXDisabledForPrivateTorrent(t *testing.T) {
	orig := pexInterval
	pexInterval = 20 * time.Millisecond
	t.Cleanup(func() { pexInterval = orig })

	mi, _ := buildTorrent(t, "pex-private.bin", 16384, []fileSpec{{length: 16384}})
	mi.Info.Private = true
	a := newFakePEXPeer(t, 44)
	b := newFakePEXPeer(t, 55)

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	tr.DialPeer(a.peerInfo())
	tr.DialPeer(b.peerInfo())
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && tr.Stats().PeerCount < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := tr.Stats().PeerCount; got != 2 {
		t.Fatalf("PeerCount = %d, want 2", got)
	}

	select {
	case wire := <-a.Received:
		t.Fatalf("a private torrent sent a PEX message: %+v", wire)
	case <-time.After(500 * time.Millisecond):
	}
}
