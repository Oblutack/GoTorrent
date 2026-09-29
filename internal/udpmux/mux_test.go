package udpmux

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func newRealUDPSocket(t *testing.T) *net.UDPConn {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	t.Cleanup(func() { pc.Close() })
	return pc
}

// isBencodeDict and isFooShaped stand in for internal/engine's own real
// classifiers (DHT/KRPC's bencoded 'd'-prefix vs internal/utp's type/
// version byte) — the exact same shape of "peek the first byte(s) to
// decide" decision, kept generic here since this package has no reason
// to know about either real protocol.
func isBencodeDict(data []byte) bool { return len(data) > 0 && data[0] == 'd' }
func isFooShaped(data []byte) bool   { return len(data) > 0 && data[0] == 0xF0 }

func TestMuxRoutesToTheCorrectFacadeByRealClassification(t *testing.T) {
	serverPC := newRealUDPSocket(t)
	m := New(serverPC)
	t.Cleanup(func() { m.Close() })

	dhtFacade := m.For(isBencodeDict)
	fooFacade := m.For(isFooShaped)

	client, err := net.DialUDP("udp", nil, serverPC.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer client.Close()

	dhtDatagram := []byte("d1:ad2:id20:aaaaaaaaaaaaaaaaaaaae1:q4:ping1:t2:aa1:y1:qe")
	fooDatagram := []byte{0xF0, 0x01, 0x02, 0x03}

	if _, err := client.Write(dhtDatagram); err != nil {
		t.Fatalf("Write (dht-shaped): %v", err)
	}
	if _, err := client.Write(fooDatagram); err != nil {
		t.Fatalf("Write (foo-shaped): %v", err)
	}

	dhtFacade.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1024)
	n, addr, err := dhtFacade.ReadFrom(buf)
	if err != nil {
		t.Fatalf("dhtFacade.ReadFrom: %v", err)
	}
	if !bytes.Equal(buf[:n], dhtDatagram) {
		t.Fatalf("dhtFacade got %q, want %q", buf[:n], dhtDatagram)
	}
	if addr == nil {
		t.Fatal("dhtFacade.ReadFrom returned a nil source address")
	}

	fooFacade.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, _, err = fooFacade.ReadFrom(buf)
	if err != nil {
		t.Fatalf("fooFacade.ReadFrom: %v", err)
	}
	if !bytes.Equal(buf[:n], fooDatagram) {
		t.Fatalf("fooFacade got %x, want %x", buf[:n], fooDatagram)
	}
}

func TestMuxDropsAnUnclaimedDatagramWithoutBlockingOthers(t *testing.T) {
	serverPC := newRealUDPSocket(t)
	m := New(serverPC)
	t.Cleanup(func() { m.Close() })

	dhtFacade := m.For(isBencodeDict)

	client, err := net.DialUDP("udp", nil, serverPC.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer client.Close()

	// Nothing claims this one.
	client.Write([]byte{0xFF, 0xFF, 0xFF})
	// This one is real DHT-shaped and must still arrive, proving the
	// unclaimed datagram didn't wedge the mux's own single read loop.
	dhtDatagram := []byte("d1:eol")
	client.Write(dhtDatagram)

	dhtFacade.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1024)
	n, _, err := dhtFacade.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if !bytes.Equal(buf[:n], dhtDatagram) {
		t.Fatalf("got %q, want %q", buf[:n], dhtDatagram)
	}
}

func TestFacadeWriteToGoesThroughTheRealSharedSocket(t *testing.T) {
	serverPC := newRealUDPSocket(t)
	m := New(serverPC)
	t.Cleanup(func() { m.Close() })
	facade := m.For(isBencodeDict)

	receiverPC := newRealUDPSocket(t)

	msg := []byte("hello from a facade")
	if _, err := facade.WriteTo(msg, receiverPC.LocalAddr()); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	receiverPC.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1024)
	n, _, err := receiverPC.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if !bytes.Equal(buf[:n], msg) {
		t.Fatalf("got %q, want %q", buf[:n], msg)
	}
}

func TestFacadeLocalAddrMatchesTheRealSharedSocket(t *testing.T) {
	serverPC := newRealUDPSocket(t)
	m := New(serverPC)
	t.Cleanup(func() { m.Close() })
	facade := m.For(isBencodeDict)

	if facade.LocalAddr().String() != serverPC.LocalAddr().String() {
		t.Fatalf("facade.LocalAddr() = %s, want the real shared socket's %s", facade.LocalAddr(), serverPC.LocalAddr())
	}
	// And it must be the concrete *net.UDPAddr type, not just something
	// satisfying net.Addr - internal/dht's own Addr() type-asserts this.
	if _, ok := facade.LocalAddr().(*net.UDPAddr); !ok {
		t.Fatalf("facade.LocalAddr() has dynamic type %T, want *net.UDPAddr", facade.LocalAddr())
	}
}

func TestReadFromDeadlineTimesOut(t *testing.T) {
	serverPC := newRealUDPSocket(t)
	m := New(serverPC)
	t.Cleanup(func() { m.Close() })
	facade := m.For(isBencodeDict)

	facade.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err := facade.ReadFrom(make([]byte, 16))
	ne, ok := err.(net.Error)
	if !ok || !ne.Timeout() {
		t.Fatalf("err = %v, want a net.Error reporting Timeout()", err)
	}
}

func TestCloseUnblocksAPendingReadFrom(t *testing.T) {
	serverPC := newRealUDPSocket(t)
	m := New(serverPC)
	facade := m.For(isBencodeDict)

	done := make(chan error, 1)
	go func() {
		_, _, err := facade.ReadFrom(make([]byte, 16))
		done <- err
	}()

	time.Sleep(100 * time.Millisecond) // let ReadFrom actually block first
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-done:
		if err != ErrClosed {
			t.Fatalf("ReadFrom returned %v after Close, want ErrClosed", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ReadFrom never unblocked after Close")
	}
}
