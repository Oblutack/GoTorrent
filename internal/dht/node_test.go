package dht

import (
	"context"
	"net"
	"testing"
	"time"
)

// newTestNode starts a real DHT node on an OS-assigned loopback port, with
// no persistence, and registers it to close on test cleanup.
func newTestNode(t *testing.T) *DHT {
	t.Helper()
	d, err := New(Config{Port: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// loopback returns d's bound port as a loopback address. New binds to the
// wildcard address (so a real node accepts packets on every interface), and
// a wildcard address is not itself a valid destination to send to — every
// test here runs two or more nodes on loopback, so this is what a real
// remote node's address would resolve to from any of the others' point of
// view.
func loopback(d *DHT) *net.UDPAddr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: d.Addr().Port}
}

func TestPingRoundTrip(t *testing.T) {
	a := newTestNode(t)
	b := newTestNode(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	gotID, err := a.ping(ctx, loopback(b))
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if gotID != b.ID() {
		t.Fatalf("ping returned id %s, want %s", gotID, b.ID())
	}

	// A real ping should have populated each side's routing table with the
	// other, exactly like handling a live query does.
	if got := a.table.FindClosest(b.ID(), 1); len(got) != 1 || got[0].id != b.ID() {
		t.Fatal("a's routing table does not contain b after a successful ping")
	}
	if got := b.table.FindClosest(a.ID(), 1); len(got) != 1 || got[0].id != a.ID() {
		t.Fatal("b's routing table does not contain a after answering a's ping")
	}
}

func TestPingUnreachableNodeTimesOut(t *testing.T) {
	a := newTestNode(t)

	// Nothing is listening here (a bound-then-closed loopback socket), so
	// this should reliably time out rather than get a reply.
	deadListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("binding a throwaway socket: %v", err)
	}
	deadAddr := deadListener.LocalAddr().(*net.UDPAddr)
	deadListener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout+2*time.Second)
	defer cancel()
	if _, err := a.ping(ctx, deadAddr); err == nil {
		t.Fatal("ping to a dead address succeeded, want an error")
	}
}

func TestFindNodeReturnsCloserNodes(t *testing.T) {
	a := newTestNode(t)
	b := newTestNode(t)
	c := newTestNode(t)

	// Seed b's table with c directly (bypassing the network) so a's
	// find_node through b has something real to return.
	b.table.Insert(c.ID(), loopback(c))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nodes, err := a.findNode(ctx, loopback(b), c.ID())
	if err != nil {
		t.Fatalf("findNode: %v", err)
	}

	var found bool
	for _, n := range nodes {
		if n.id == c.ID() {
			found = true
		}
	}
	if !found {
		t.Fatalf("find_node through b did not return c: %+v", nodes)
	}
}

func TestGetPeersReturnsStoredPeersAndAToken(t *testing.T) {
	a := newTestNode(t)
	b := newTestNode(t)

	infoHash := NodeID{0xaa, 0xbb}
	peerAddr := &net.UDPAddr{IP: net.IPv4(203, 0, 113, 5), Port: 51413}
	b.storePeer(infoHash, peerAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := a.getPeers(ctx, loopback(b), infoHash)
	if err != nil {
		t.Fatalf("getPeers: %v", err)
	}
	if res.token == "" {
		t.Fatal("get_peers response carried no token")
	}
	if len(res.peers) != 1 || !res.peers[0].IP.Equal(peerAddr.IP) || res.peers[0].Port != uint16(peerAddr.Port) {
		t.Fatalf("got peers %+v, want %s", res.peers, peerAddr)
	}
}

func TestGetPeersReturnsClosestNodesWhenNoPeersStored(t *testing.T) {
	a := newTestNode(t)
	b := newTestNode(t)
	c := newTestNode(t)
	b.table.Insert(c.ID(), loopback(c))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := a.getPeers(ctx, loopback(b), NodeID{0x77})
	if err != nil {
		t.Fatalf("getPeers: %v", err)
	}
	if len(res.peers) != 0 {
		t.Fatalf("got %d peers with none stored, want 0", len(res.peers))
	}
	var found bool
	for _, n := range res.nodes {
		if n.id == c.ID() {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected c among the closest nodes returned, got %+v", res.nodes)
	}
}

func TestAnnouncePeerStoresAndRequiresAValidToken(t *testing.T) {
	a := newTestNode(t)
	b := newTestNode(t)
	infoHash := NodeID{0x55}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.announcePeer(ctx, loopback(b), infoHash, 12345, "not-a-real-token"); err == nil {
		t.Fatal("announce_peer with a bogus token succeeded, want an error")
	}

	res, err := a.getPeers(ctx, loopback(b), infoHash)
	if err != nil {
		t.Fatalf("getPeers: %v", err)
	}
	if err := a.announcePeer(ctx, loopback(b), infoHash, 12345, res.token); err != nil {
		t.Fatalf("announce_peer with a real token: %v", err)
	}

	stored := b.storedPeers(infoHash)
	if len(stored) != 1 || stored[0].addr.Port != 12345 {
		t.Fatalf("b did not store the announced peer: %+v", stored)
	}
}

func TestAnnouncePeerHonorsImpliedPort(t *testing.T) {
	a := newTestNode(t)
	b := newTestNode(t)
	infoHash := NodeID{0x66}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := a.getPeers(ctx, loopback(b), infoHash)
	if err != nil {
		t.Fatalf("getPeers: %v", err)
	}

	// announce_peer with implied_port=1 and a bogus declared port: b must
	// use the UDP packet's actual source port, not the declared one.
	_, err = a.sendQuery(ctx, loopback(b), "announce_peer", announcePeerArgs{
		ID: a.id, ImpliedPort: 1, InfoHash: infoHash, Port: 1, Token: res.token,
	})
	if err != nil {
		t.Fatalf("announce_peer: %v", err)
	}

	stored := b.storedPeers(infoHash)
	if len(stored) != 1 {
		t.Fatalf("got %d stored peers, want 1", len(stored))
	}
	if stored[0].addr.Port != a.Addr().Port {
		t.Fatalf("stored port %d, want a's real source port %d (implied_port should override the declared 1)",
			stored[0].addr.Port, a.Addr().Port)
	}
}

func TestFindPeersEndToEnd(t *testing.T) {
	a := newTestNode(t)
	b := newTestNode(t)

	infoHash := NodeID{0x42}
	stashed := &net.UDPAddr{IP: net.IPv4(198, 51, 100, 7), Port: 6881}
	b.storePeer(infoHash, stashed)

	// a needs b in its table to have anywhere to start the lookup from.
	a.table.Insert(b.ID(), loopback(b))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	peers := a.FindPeers(ctx, infoHash, 6882)

	var found bool
	for _, p := range peers {
		if p.IP.Equal(stashed.IP) && p.Port == uint16(stashed.Port) {
			found = true
		}
	}
	if !found {
		t.Fatalf("FindPeers did not return the peer b had stored: got %+v", peers)
	}

	// FindPeers should also have announced a to b, since b returned a token.
	selfAnnounced := b.storedPeers(infoHash)
	var sawA bool
	for _, e := range selfAnnounced {
		if e.addr.IP.Equal(loopback(a).IP) && e.addr.Port == 6882 {
			sawA = true
		}
	}
	if !sawA {
		t.Fatalf("FindPeers did not announce_peer back to b: b has %+v", selfAnnounced)
	}
}

func TestBootstrapPopulatesTableFromASeedNode(t *testing.T) {
	seed := newTestNode(t)
	other := newTestNode(t)
	seed.table.Insert(other.ID(), loopback(other))

	fresh := newTestNode(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fresh.Bootstrap(ctx, []string{loopback(seed).String()})

	if fresh.NodeCount() == 0 {
		t.Fatal("Bootstrap left the routing table empty")
	}
	if got := fresh.table.FindClosest(seed.ID(), 1); len(got) != 1 || got[0].id != seed.ID() {
		t.Fatal("Bootstrap did not add the seed node itself to the table")
	}
}

func TestRoutingTableStatePersistsAcrossRestart(t *testing.T) {
	statePath := t.TempDir() + "/dht.nodes"

	first, err := New(Config{Port: 0, StatePath: statePath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	other := newTestNode(t)
	first.table.Insert(other.ID(), loopback(other))
	first.saveState()
	first.Close()

	second, err := New(Config{Port: 0, StatePath: statePath})
	if err != nil {
		t.Fatalf("New (second): %v", err)
	}
	defer second.Close()

	if got := second.table.FindClosest(other.ID(), 1); len(got) != 1 || got[0].id != other.ID() {
		t.Fatalf("reloaded table does not contain the persisted contact: %+v", got)
	}
}
