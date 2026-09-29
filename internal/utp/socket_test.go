package utp

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// newLoopbackSocket binds a real UDP socket on 127.0.0.1 - real sockets
// throughout, the same "real fixture over mocks" bar every Go package in
// this project already meets, and necessary here specifically: a fake
// in-memory transport would hide the very packet-loss/reordering
// scenarios this test suite exists to prove recovery from.
func newLoopbackSocket(t *testing.T) *Socket {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	s := NewSocket(pc)
	t.Cleanup(func() { s.Close() })
	return s
}

func dialAndAccept(t *testing.T, dialer, acceptor *Socket) (client net.Conn, server net.Conn) {
	t.Helper()
	acceptCh := make(chan net.Conn, 1)
	acceptErrCh := make(chan error, 1)
	go func() {
		c, err := acceptor.Accept()
		if err != nil {
			acceptErrCh <- err
			return
		}
		acceptCh <- c
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := dialer.DialContext(ctx, "udp", acceptor.LocalAddr().String())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}

	select {
	case server = <-acceptCh:
	case err := <-acceptErrCh:
		t.Fatalf("Accept: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Accept")
	}
	return client, server
}

func TestDialAndAcceptEstablishesAConnection(t *testing.T) {
	dialer := newLoopbackSocket(t)
	acceptor := newLoopbackSocket(t)
	client, server := dialAndAccept(t, dialer, acceptor)
	t.Cleanup(func() { client.Close() })
	t.Cleanup(func() { server.Close() })

	if client.RemoteAddr().String() != acceptor.LocalAddr().String() {
		t.Fatalf("client.RemoteAddr() = %s, want %s", client.RemoteAddr(), acceptor.LocalAddr())
	}
}

func TestSmallWriteReadRoundTrip(t *testing.T) {
	dialer := newLoopbackSocket(t)
	acceptor := newLoopbackSocket(t)
	client, server := dialAndAccept(t, dialer, acceptor)
	t.Cleanup(func() { client.Close() })
	t.Cleanup(func() { server.Close() })

	msg := []byte("hello over a real uTP connection")
	if _, err := client.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("got %q, want %q", got, msg)
	}
}

func TestBidirectionalTraffic(t *testing.T) {
	dialer := newLoopbackSocket(t)
	acceptor := newLoopbackSocket(t)
	client, server := dialAndAccept(t, dialer, acceptor)
	t.Cleanup(func() { client.Close() })
	t.Cleanup(func() { server.Close() })

	clientMsg := []byte("client to server")
	serverMsg := []byte("server to client, a different length")

	errCh := make(chan error, 2)
	go func() { _, err := client.Write(clientMsg); errCh <- err }()
	go func() { _, err := server.Write(serverMsg); errCh <- err }()
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	gotAtServer := make([]byte, len(clientMsg))
	if _, err := io.ReadFull(server, gotAtServer); err != nil {
		t.Fatalf("server ReadFull: %v", err)
	}
	if !bytes.Equal(gotAtServer, clientMsg) {
		t.Fatalf("server got %q, want %q", gotAtServer, clientMsg)
	}

	gotAtClient := make([]byte, len(serverMsg))
	if _, err := io.ReadFull(client, gotAtClient); err != nil {
		t.Fatalf("client ReadFull: %v", err)
	}
	if !bytes.Equal(gotAtClient, serverMsg) {
		t.Fatalf("client got %q, want %q", gotAtClient, serverMsg)
	}
}

// TestLargeTransferSpanningManyPackets proves real fragmentation and
// reassembly across many MSS-sized packets, not just a single-datagram
// exchange.
func TestLargeTransferSpanningManyPackets(t *testing.T) {
	dialer := newLoopbackSocket(t)
	acceptor := newLoopbackSocket(t)
	client, server := dialAndAccept(t, dialer, acceptor)
	t.Cleanup(func() { client.Close() })
	t.Cleanup(func() { server.Close() })

	const size = mss*20 + 777 // deliberately not a clean multiple of MSS
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i * 7)
	}

	errCh := make(chan error, 1)
	go func() { _, err := client.Write(content); errCh <- err }()

	got := make([]byte, size)
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("large transfer content mismatch")
	}
}

func TestDialContextFailsAgainstAPeerThatNeverReplies(t *testing.T) {
	orig := SynRetries
	SynRetries = 2
	t.Cleanup(func() { SynRetries = orig })

	dialer := newLoopbackSocket(t)
	// A real UDP socket nothing is listening on - packets sent there are
	// simply dropped by the OS, exactly like a genuinely unreachable µTP
	// peer would look from this side.
	deadPC, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	deadAddr := deadPC.LocalAddr().String()
	deadPC.Close() // now genuinely nothing is listening there

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = dialer.DialContext(ctx, "udp", deadAddr)
	if err == nil {
		t.Fatal("DialContext succeeded against an address nothing is listening on")
	}
}

func TestCloseSendsFINAndPeerSeesEOF(t *testing.T) {
	dialer := newLoopbackSocket(t)
	acceptor := newLoopbackSocket(t)
	client, server := dialAndAccept(t, dialer, acceptor)
	t.Cleanup(func() { server.Close() })

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	buf := make([]byte, 16)
	server.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := server.Read(buf)
	if err != io.EOF {
		t.Fatalf("server.Read after client.Close() = %v, want io.EOF", err)
	}
}

func TestCloseDeliversAlreadyBufferedDataBeforeEOF(t *testing.T) {
	dialer := newLoopbackSocket(t)
	acceptor := newLoopbackSocket(t)
	client, server := dialAndAccept(t, dialer, acceptor)
	t.Cleanup(func() { server.Close() })

	msg := []byte("last words before closing")
	if _, err := client.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Give the message a moment to actually arrive and be queued for the
	// reader before Close races it.
	time.Sleep(200 * time.Millisecond)
	client.Close()

	got := make([]byte, len(msg))
	server.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatalf("ReadFull: %v (data sent before Close must still be delivered)", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("got %q, want %q", got, msg)
	}
}

func TestReadDeadlineTimesOut(t *testing.T) {
	dialer := newLoopbackSocket(t)
	acceptor := newLoopbackSocket(t)
	client, server := dialAndAccept(t, dialer, acceptor)
	t.Cleanup(func() { client.Close() })
	t.Cleanup(func() { server.Close() })

	server.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, err := server.Read(make([]byte, 16))
	ne, ok := err.(net.Error)
	if !ok || !ne.Timeout() {
		t.Fatalf("err = %v, want a net.Error reporting Timeout()", err)
	}
}
