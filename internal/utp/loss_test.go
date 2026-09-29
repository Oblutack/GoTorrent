package utp

import (
	"bytes"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// lossyPacketConn wraps a real net.PacketConn (a real UDP socket in every
// test below) and deterministically drops every dropEvery'th outgoing
// packet — real loss injected at the real transport layer, not a fake
// substitute for one, so what's actually being proven is that the real
// reorder buffer, SACK generation, and RTO-driven retransmission in
// conn_actor.go recover a real transfer, not that some simplified stand-in
// does.
type lossyPacketConn struct {
	net.PacketConn
	dropEvery int32
	n         atomic.Int32
	mu        sync.Mutex
	dropped   int
}

func (l *lossyPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if l.dropEvery > 0 && l.n.Add(1)%l.dropEvery == 0 {
		l.mu.Lock()
		l.dropped++
		l.mu.Unlock()
		return len(p), nil // report success to the caller, exactly like a real dropped UDP datagram does
	}
	return l.PacketConn.WriteTo(p, addr)
}

func (l *lossyPacketConn) droppedCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dropped
}

func newLossySocket(t *testing.T, dropEvery int32) (*Socket, *lossyPacketConn) {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	lossy := &lossyPacketConn{PacketConn: pc, dropEvery: dropEvery}
	s := NewSocket(lossy)
	t.Cleanup(func() { s.Close() })
	return s, lossy
}

// TestTransferSurvivesRealPacketLoss sends a large, many-packet transfer
// over a real UDP socket that genuinely drops roughly one packet in five —
// real loss, not a simulated stand-in — and confirms the RTO-driven
// retransmission path (handleRTO) recovers every dropped packet and the
// full content still arrives byte-exact.
func TestTransferSurvivesRealPacketLoss(t *testing.T) {
	dialer, dialerLossy := newLossySocket(t, 5)
	acceptor := newLoopbackSocket(t)
	client, server := dialAndAccept(t, dialer, acceptor)
	t.Cleanup(func() { client.Close() })
	t.Cleanup(func() { server.Close() })

	const size = mss*15 + 321
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i * 13)
	}

	errCh := make(chan error, 1)
	go func() { _, err := client.Write(content); errCh <- err }()

	got := make([]byte, size)
	server.SetReadDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("content mismatch after recovering from real packet loss")
	}
	if dialerLossy.droppedCount() == 0 {
		t.Fatal("the loss injection itself never fired - this test proved nothing about loss recovery")
	}
	t.Logf("recovered a transfer despite %d real dropped packets", dialerLossy.droppedCount())
}

// TestTransferSurvivesRealPacketLossBothDirections is the bidirectional
// companion to the test above: both sides' sockets drop real packets, so
// both directions' reorder buffers, SACK generation, and retransmission
// timers all have to do real work at once, the same as a genuine
// congested link would put on a real two-way transfer.
func TestTransferSurvivesRealPacketLossBothDirections(t *testing.T) {
	dialer, dialerLossy := newLossySocket(t, 6)
	acceptor, acceptorLossy := newLossySocket(t, 7)
	client, server := dialAndAccept(t, dialer, acceptor)
	t.Cleanup(func() { client.Close() })
	t.Cleanup(func() { server.Close() })

	const size = mss*10 + 55
	clientContent := make([]byte, size)
	serverContent := make([]byte, size)
	for i := range clientContent {
		clientContent[i] = byte(i * 11)
		serverContent[i] = byte(i*11 + 1)
	}

	errCh := make(chan error, 2)
	go func() { _, err := client.Write(clientContent); errCh <- err }()
	go func() { _, err := server.Write(serverContent); errCh <- err }()

	gotAtServer := make([]byte, size)
	server.SetReadDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.ReadFull(server, gotAtServer); err != nil {
		t.Fatalf("server ReadFull: %v", err)
	}
	gotAtClient := make([]byte, size)
	client.SetReadDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.ReadFull(client, gotAtClient); err != nil {
		t.Fatalf("client ReadFull: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	if !bytes.Equal(gotAtServer, clientContent) {
		t.Fatal("server's received content mismatch")
	}
	if !bytes.Equal(gotAtClient, serverContent) {
		t.Fatal("client's received content mismatch")
	}
	if dialerLossy.droppedCount() == 0 || acceptorLossy.droppedCount() == 0 {
		t.Fatal("loss injection never fired on one direction or the other - this test proved nothing")
	}
}
