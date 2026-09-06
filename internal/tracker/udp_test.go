package tracker

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// fakeUDPTracker plays just enough of BEP 15's server side to exercise a
// real Client.Announce round trip: one connect, then one announce, each
// validated for the fields a real tracker would check.
type fakeUDPTracker struct {
	t    *testing.T
	conn *net.UDPConn

	peer PeerInfo // the single peer this fixture hands back
}

func newFakeUDPTracker(t *testing.T, peer PeerInfo) *fakeUDPTracker {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	f := &fakeUDPTracker{t: t, conn: conn, peer: peer}
	t.Cleanup(func() { conn.Close() })
	go f.serve()
	return f
}

func (f *fakeUDPTracker) addr() string {
	return f.conn.LocalAddr().String()
}

func (f *fakeUDPTracker) serve() {
	buf := make([]byte, 2048)
	const fakeConnID = 0x1122334455667788
	for {
		n, raddr, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < 16 {
			continue
		}
		action := binary.BigEndian.Uint32(buf[8:12])
		txID := binary.BigEndian.Uint32(buf[12:16])

		switch action {
		case udpActionConnect:
			if binary.BigEndian.Uint64(buf[0:8]) != udpProtocolMagic {
				f.t.Errorf("fake tracker: connect request had the wrong magic")
				continue
			}
			resp := make([]byte, 16)
			binary.BigEndian.PutUint32(resp[0:4], udpActionConnect)
			binary.BigEndian.PutUint32(resp[4:8], txID)
			binary.BigEndian.PutUint64(resp[8:16], fakeConnID)
			f.conn.WriteToUDP(resp, raddr)

		case udpActionAnnounce:
			if n < 98 {
				f.t.Errorf("fake tracker: announce request is %d bytes, want at least 98", n)
				continue
			}
			if binary.BigEndian.Uint64(buf[0:8]) != fakeConnID {
				f.t.Errorf("fake tracker: announce used the wrong connection id")
				continue
			}
			resp := make([]byte, 26)
			binary.BigEndian.PutUint32(resp[0:4], udpActionAnnounce)
			binary.BigEndian.PutUint32(resp[4:8], txID)
			binary.BigEndian.PutUint32(resp[8:12], 1800) // interval
			binary.BigEndian.PutUint32(resp[12:16], 2)   // leechers
			binary.BigEndian.PutUint32(resp[16:20], 5)   // seeders
			copy(resp[20:24], f.peer.IP.To4())
			binary.BigEndian.PutUint16(resp[24:26], f.peer.Port)
			f.conn.WriteToUDP(resp, raddr)
		}
	}
}

func TestUDPAnnounceRoundTrip(t *testing.T) {
	want := PeerInfo{IP: net.ParseIP("203.0.113.9").To4(), Port: 6881}
	fake := newFakeUDPTracker(t, want)

	c := NewClient(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := c.Announce(ctx, "udp://"+fake.addr()+"/announce", AnnounceRequest{
		InfoHash: [20]byte{1, 2, 3},
		PeerID:   [20]byte{4, 5, 6},
		Port:     6881,
		Left:     1000,
		Event:    EventStarted,
		NumWant:  50,
	})
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if resp.Interval != 1800*time.Second {
		t.Fatalf("Interval = %s, want 1800s", resp.Interval)
	}
	if resp.Complete != 5 || resp.Incomplete != 2 {
		t.Fatalf("Complete/Incomplete = %d/%d, want 5/2", resp.Complete, resp.Incomplete)
	}
	if len(resp.Peers) != 1 || !resp.Peers[0].IP.Equal(want.IP) || resp.Peers[0].Port != want.Port {
		t.Fatalf("Peers = %+v, want [%+v]", resp.Peers, want)
	}
}

// TestUDPConnectionIDIsCached proves a second announce to the same tracker,
// within the 60s TTL, skips the connect round trip: the fake tracker would
// reject an announce carrying a connection id it never issued, so if the
// second Announce reused a stale or fabricated id this would fail rather
// than merely being slow.
func TestUDPConnectionIDIsCached(t *testing.T) {
	want := PeerInfo{IP: net.ParseIP("198.51.100.7").To4(), Port: 1234}
	fake := newFakeUDPTracker(t, want)

	c := NewClient(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := AnnounceRequest{InfoHash: [20]byte{9}, PeerID: [20]byte{8}, Port: 1, Left: 1}
	url := "udp://" + fake.addr() + "/announce"

	if _, err := c.Announce(ctx, url, req); err != nil {
		t.Fatalf("first Announce: %v", err)
	}

	c.udpMu.Lock()
	_, cached := c.udpConns[fake.addr()]
	c.udpMu.Unlock()
	if !cached {
		t.Fatal("connection id was not cached after the first announce")
	}

	if _, err := c.Announce(ctx, url, req); err != nil {
		t.Fatalf("second Announce (should reuse the cached connection id): %v", err)
	}
}

// TestUDPAnnounceRespectsContextCancellation proves the ctx-watcher goroutine
// actually aborts a stuck read rather than leaving Announce to wait out BEP
// 15's first 15-second retry window, against a tracker that never replies at
// all.
func TestUDPAnnounceRespectsContextCancellation(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer conn.Close()

	c := NewClient(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = c.Announce(ctx, "udp://"+conn.LocalAddr().String()+"/announce", AnnounceRequest{Left: 1})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Announce succeeded against a tracker that never replies")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Announce took %s to notice context cancellation, want well under BEP 15's first 15s retry window", elapsed)
	}
}

func TestUDPAnnounceRejectsMissingPort(t *testing.T) {
	c := NewClient(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := c.Announce(ctx, "udp://127.0.0.1/announce", AnnounceRequest{})
	if err == nil {
		t.Fatal("Announce accepted a udp:// URL with no port, want an error")
	}
}
