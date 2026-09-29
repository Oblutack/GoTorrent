package mse

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// initiateResult/receiveResult carry a handshake side's outcome back from
// the goroutine it ran in, since InitiateHandshake and ReceiveHandshake
// must run concurrently against opposite ends of one connection — each
// side's very first read blocks until the other side has written.
type initiateResult struct {
	conn   net.Conn
	method CryptoMethod
	err    error
}

type receiveResult struct {
	conn    net.Conn
	method  CryptoMethod
	matched []byte
	err     error
}

// realLoopbackPair dials a real TCP loopback connection rather than using
// net.Pipe: net.Pipe is fully synchronous with zero internal buffering,
// so the DH exchange's normal "write my public value, then read the
// peer's" shape on both ends simultaneously deadlocks on it (both sides
// block in Write, waiting for a Read that can never happen before it) —
// a real socket's kernel send buffer easily absorbs a handshake-sized
// write without blocking, exactly like production traffic.
func realLoopbackPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	type acceptResult struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		c, err := ln.Accept()
		acceptCh <- acceptResult{c, err}
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	res := <-acceptCh
	if res.err != nil {
		t.Fatalf("accept: %v", res.err)
	}
	return client, res.conn
}

func runHandshake(t *testing.T, skey []byte, provide CryptoMethod, ia []byte, skeys [][]byte, selector CryptoSelector) (initiateResult, receiveResult) {
	t.Helper()
	aConn, bConn := realLoopbackPair(t)

	initCh := make(chan initiateResult, 1)
	recvCh := make(chan receiveResult, 1)

	go func() {
		conn, method, err := InitiateHandshake(aConn, skey, provide, ia)
		if err != nil {
			// A real caller closes on failure (internal/peer already
			// does this on every other handshake error path) — matching
			// that here is what lets the peer's own blocked read fail
			// promptly instead of waiting out its full internal
			// handshakeTimeout.
			aConn.Close()
		}
		initCh <- initiateResult{conn: conn, method: method, err: err}
	}()
	go func() {
		br := bufio.NewReader(bConn)
		conn, method, matched, err := ReceiveHandshake(bConn, br, skeys, selector)
		if err != nil {
			bConn.Close()
		}
		recvCh <- receiveResult{conn: conn, method: method, matched: matched, err: err}
	}()

	var ir initiateResult
	var rr receiveResult
	for i := 0; i < 2; i++ {
		select {
		case ir = <-initCh:
		case rr = <-recvCh:
		case <-time.After(15 * time.Second):
			t.Fatal("timed out waiting for both sides of the handshake to finish")
		}
	}
	return ir, rr
}

func TestHandshakeRoundTripSelectsRC4WhenBothOfferIt(t *testing.T) {
	skey := testSKEY()
	ir, rr := runHandshake(t, skey, CryptoPlaintext|CryptoRC4, nil, [][]byte{skey}, DefaultSelector)
	if ir.err != nil {
		t.Fatalf("initiator: %v", ir.err)
	}
	if rr.err != nil {
		t.Fatalf("receiver: %v", rr.err)
	}
	if ir.method != CryptoRC4 || rr.method != CryptoRC4 {
		t.Fatalf("methods = initiator %v, receiver %v, want both RC4 (DefaultSelector prefers it)", ir.method, rr.method)
	}
	if !bytes.Equal(rr.matched, skey) {
		t.Fatalf("matched SKEY = %x, want %x", rr.matched, skey)
	}
}

func TestHandshakeRoundTripSelectsPlaintextWhenOnlyThatsOffered(t *testing.T) {
	skey := testSKEY()
	ir, rr := runHandshake(t, skey, CryptoPlaintext, nil, [][]byte{skey}, DefaultSelector)
	if ir.err != nil {
		t.Fatalf("initiator: %v", ir.err)
	}
	if rr.err != nil {
		t.Fatalf("receiver: %v", rr.err)
	}
	if ir.method != CryptoPlaintext || rr.method != CryptoPlaintext {
		t.Fatalf("methods = initiator %v, receiver %v, want both Plaintext", ir.method, rr.method)
	}
}

// plaintextOnlySelector stands in for a real peer that never supports
// RC4 at all, regardless of what's offered — the shape "Required" policy
// (crypto_provide = RC4 only, enforced by the caller, not this package)
// needs a genuinely plaintext-only peer to fail against.
func plaintextOnlySelector(offered CryptoMethod) (CryptoMethod, error) {
	if offered&CryptoPlaintext == 0 {
		return 0, errors.New("this fake peer only ever speaks plaintext")
	}
	return CryptoPlaintext, nil
}

func TestHandshakeFailsWhenInitiatorOffersOnlyRC4ButPeerIsPlaintextOnly(t *testing.T) {
	skey := testSKEY()
	ir, rr := runHandshake(t, skey, CryptoRC4, nil, [][]byte{skey}, plaintextOnlySelector)
	if ir.err == nil {
		t.Fatal("initiator succeeded, want a failure (peer cannot select any method we offered)")
	}
	if rr.err == nil {
		t.Fatal("receiver succeeded, want a failure (its own selector rejected the only offered method)")
	}
}

func TestHandshakeIdentifiesTheCorrectSKEYAmongMultipleCandidates(t *testing.T) {
	real := testSKEY()
	decoy1 := bytes.Repeat([]byte{0x01}, 20)
	decoy2 := bytes.Repeat([]byte{0x02}, 20)
	candidates := [][]byte{decoy1, real, decoy2}

	ir, rr := runHandshake(t, real, CryptoPlaintext|CryptoRC4, nil, candidates, DefaultSelector)
	if ir.err != nil {
		t.Fatalf("initiator: %v", ir.err)
	}
	if rr.err != nil {
		t.Fatalf("receiver: %v", rr.err)
	}
	if !bytes.Equal(rr.matched, real) {
		t.Fatalf("matched = %x, want the real SKEY %x, not a decoy", rr.matched, real)
	}
}

func TestHandshakeFailsWhenNoKnownSKEYMatches(t *testing.T) {
	real := testSKEY()
	unrelated := bytes.Repeat([]byte{0x99}, 20)

	_, rr := runHandshake(t, real, CryptoPlaintext|CryptoRC4, nil, [][]byte{unrelated}, DefaultSelector)
	if rr.err == nil {
		t.Fatal("receiver succeeded, want a failure (no candidate SKEY matches)")
	}
}

func TestHandshakePostNegotiationTrafficRoundTripsExactly(t *testing.T) {
	skey := testSKEY()
	ir, rr := runHandshake(t, skey, CryptoRC4, nil, [][]byte{skey}, DefaultSelector)
	if ir.err != nil || rr.err != nil {
		t.Fatalf("handshake failed: initiator=%v receiver=%v", ir.err, rr.err)
	}

	// A real classic BitTorrent handshake is 68 bytes; simulate one plus
	// a bit more, in both directions, through the wrapped conns - proving
	// the RC4 keystream continuation past the packet 3/4 boundary is
	// correct, not just that the negotiation fields themselves decoded
	// right.
	aToB := bytes.Repeat([]byte("A-to-B-payload-"), 8)
	bToA := bytes.Repeat([]byte("B-to-A-payload-"), 8)

	errCh := make(chan error, 2)
	go func() {
		_, err := ir.conn.Write(aToB)
		errCh <- err
	}()
	go func() {
		_, err := rr.conn.Write(bToA)
		errCh <- err
	}()
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	gotAtB := make([]byte, len(aToB))
	if _, err := io.ReadFull(rr.conn, gotAtB); err != nil {
		t.Fatalf("reading at B: %v", err)
	}
	if !bytes.Equal(gotAtB, aToB) {
		t.Fatalf("B received %q, want %q", gotAtB, aToB)
	}

	gotAtA := make([]byte, len(bToA))
	if _, err := io.ReadFull(ir.conn, gotAtA); err != nil {
		t.Fatalf("reading at A: %v", err)
	}
	if !bytes.Equal(gotAtA, bToA) {
		t.Fatalf("A received %q, want %q", gotAtA, bToA)
	}
}

// TestHandshakeReceiverSeesABundledIA exercises the interop-critical path
// this package's own production initiator never triggers (see doc.go):
// a real peer bundling its classic handshake as MSE's optional IA field
// to save a round trip. The receiver's wrapped conn must transparently
// yield those bytes first, ahead of anything read live off the wire
// afterward.
func TestHandshakeReceiverSeesABundledIA(t *testing.T) {
	skey := testSKEY()
	bundled := bytes.Repeat([]byte("classic-handshake-bytes"), 3)

	ir, rr := runHandshake(t, skey, CryptoRC4, bundled, [][]byte{skey}, DefaultSelector)
	if ir.err != nil || rr.err != nil {
		t.Fatalf("handshake failed: initiator=%v receiver=%v", ir.err, rr.err)
	}

	gotIA := make([]byte, len(bundled))
	if _, err := io.ReadFull(rr.conn, gotIA); err != nil {
		t.Fatalf("reading bundled IA at receiver: %v", err)
	}
	if !bytes.Equal(gotIA, bundled) {
		t.Fatalf("receiver's first read = %q, want the bundled IA %q", gotIA, bundled)
	}

	// And traffic sent afterward must still arrive correctly, proving the
	// io.MultiReader handoff back to the live connection works too.
	more := []byte("more-live-traffic")
	if _, err := ir.conn.Write(more); err != nil {
		t.Fatalf("writing more traffic: %v", err)
	}
	gotMore := make([]byte, len(more))
	if _, err := io.ReadFull(rr.conn, gotMore); err != nil {
		t.Fatalf("reading more traffic: %v", err)
	}
	if !bytes.Equal(gotMore, more) {
		t.Fatalf("got %q after the bundled IA, want %q", gotMore, more)
	}
}

func TestInitiateHandshakeRejectsAWrongLengthSKEY(t *testing.T) {
	// No real peer needed on the other end: the length check happens
	// before InitiateHandshake ever touches the connection.
	aConn, bConn := realLoopbackPair(t)
	defer bConn.Close()
	_, _, err := InitiateHandshake(aConn, []byte("too-short"), CryptoRC4, nil)
	if err == nil {
		t.Fatal("InitiateHandshake accepted a non-20-byte SKEY")
	}
}
