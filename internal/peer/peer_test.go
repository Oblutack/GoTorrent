package peer

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

func TestMain(m *testing.M) {
	logger.Init(false)
	m.Run()
}

var testTorrent = TorrentInfo{
	NumPieces:   4,
	PieceLength: 32768, // two 16 KiB blocks per piece
	TotalLength: 32768*3 + 100,
}

// dialTestPeer stands up a loopback listener, completes a handshake on it, and
// returns the real Client together with the raw server-side connection so a
// test can drive the wire directly.
func dialTestPeer(t *testing.T, hasPiece func(uint32) bool, readBlock func(index, begin, length uint32) ([]byte, error)) (*Client, net.Conn) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	type accepted struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan accepted, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			acceptCh <- accepted{err: err}
			return
		}
		// Read their handshake, answer with ours.
		hs := make([]byte, 68)
		if _, err := io.ReadFull(conn, hs); err != nil {
			conn.Close()
			acceptCh <- accepted{err: err}
			return
		}
		var remoteID [20]byte
		copy(remoteID[:], "-TEST01-peer00000000")
		reply := NewHandshake(testTorrent.InfoHash, remoteID).Serialize()
		if _, err := conn.Write(reply); err != nil {
			conn.Close()
			acceptCh <- accepted{err: err}
			return
		}
		acceptCh <- accepted{conn: conn}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	client, err := NewClient(
		tracker.PeerInfo{IP: addr.IP, Port: uint16(addr.Port)},
		testTorrent,
		[20]byte{},
		hasPiece,
		readBlock,
		Limits{},
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	got := <-acceptCh
	if got.err != nil {
		t.Fatalf("accept side: %v", got.err)
	}
	t.Cleanup(func() { got.conn.Close() })

	return client, got.conn
}

// readFrame reads one length-prefixed wire frame and returns its payload
// including the message ID. A zero-length frame (keep-alive) returns nil.
func readFrame(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	var prefix [4]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		t.Fatalf("read length prefix: %v", err)
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 {
		return nil
	}
	if n > maxMessageLength {
		t.Fatalf("frame claims %d bytes, which is above the %d cap", n, maxMessageLength)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatalf("read frame body of %d bytes: %v", n, err)
	}
	return body
}

func writeFrame(t *testing.T, conn net.Conn, id MessageID, payload []byte) {
	t.Helper()
	msg := &Message{ID: id, Payload: payload}
	if _, err := conn.Write(msg.Serialize()); err != nil {
		t.Fatalf("write %s frame: %v", id, err)
	}
}

// TestConcurrentSendsProduceIntactFrames is the regression test for the
// interleaved-write bug: several goroutines used to call Conn.Write directly,
// so a large Piece frame could be split across syscalls and spliced with a
// Have, producing garbage length prefixes at the far end.
func TestConcurrentSendsProduceIntactFrames(t *testing.T) {
	client, server := dialTestPeer(t, func(uint32) bool { return true }, nil)

	go client.sendLoop()

	const (
		senders       = 8
		framesPerSend = 25
	)
	block := make([]byte, MaxBlockLength)
	for i := range block {
		block[i] = byte(i)
	}

	var wg sync.WaitGroup
	for g := 0; g < senders; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < framesPerSend; i++ {
				if g%2 == 0 {
					// Ignore ErrOutboundFull: dropping under backpressure is the
					// designed behaviour, corruption is not.
					_ = client.SendPiece(uint32(g%testTorrent.NumPieces), 0, block)
				} else {
					_ = client.SendHave(uint32(i % testTorrent.NumPieces))
				}
			}
		}(g)
	}

	// Drain the wire on the server side, asserting every frame is well-formed.
	done := make(chan struct{})
	var pieces, haves int
	go func() {
		defer close(done)
		server.SetReadDeadline(time.Now().Add(10 * time.Second))
		for {
			var prefix [4]byte
			if _, err := io.ReadFull(server, prefix[:]); err != nil {
				return
			}
			n := binary.BigEndian.Uint32(prefix[:])
			if n == 0 || n > maxMessageLength {
				t.Errorf("corrupt length prefix: %d", n)
				return
			}
			body := make([]byte, n)
			if _, err := io.ReadFull(server, body); err != nil {
				t.Errorf("truncated frame of %d bytes: %v", n, err)
				return
			}
			switch MessageID(body[0]) {
			case MsgPiece:
				if len(body) != 1+8+MaxBlockLength {
					t.Errorf("Piece frame is %d bytes, expected %d", len(body), 1+8+MaxBlockLength)
					return
				}
				for i, b := range body[9:] {
					if b != byte(i) {
						t.Errorf("Piece payload corrupted at offset %d", i)
						return
					}
				}
				pieces++
			case MsgHave:
				if len(body) != 1+4 {
					t.Errorf("Have frame is %d bytes, expected 5", len(body))
					return
				}
				haves++
			case MsgInterested:
				// SendInterested is not running here, but tolerate it.
			default:
				t.Errorf("unexpected message %s on the wire", MessageID(body[0]))
				return
			}
		}
	}()

	wg.Wait()
	// Let sendLoop flush, then close so the reader sees EOF.
	time.Sleep(200 * time.Millisecond)
	client.Close()
	<-done

	if pieces == 0 || haves == 0 {
		t.Fatalf("expected both Piece and Have frames to arrive, got %d/%d", pieces, haves)
	}
	t.Logf("received %d intact Piece frames and %d intact Have frames", pieces, haves)
}

func TestValidateRequest(t *testing.T) {
	c := &Client{Torrent: testTorrent}
	lastPieceLen := testTorrent.PieceLen(uint32(testTorrent.NumPieces - 1))
	if lastPieceLen != 100 {
		t.Fatalf("test fixture is wrong: last piece is %d bytes", lastPieceLen)
	}

	tests := []struct {
		name                 string
		index, begin, length uint32
		wantErr              bool
	}{
		{name: "ordinary block", length: MaxBlockLength},
		{name: "second block of a piece", begin: MaxBlockLength, length: MaxBlockLength},
		{name: "short final piece", index: 3, length: 100},
		{name: "zero length", length: 0, wantErr: true},
		{name: "oversized length", length: MaxBlockLength + 1, wantErr: true},
		{name: "absurd length", length: 1 << 30, wantErr: true},
		{name: "piece index past the end", index: 4, length: 16, wantErr: true},
		{name: "piece index way past the end", index: ^uint32(0), length: 16, wantErr: true},
		{name: "begin past the piece", begin: 32768, length: 16, wantErr: true},
		{name: "block straddles the piece end", begin: 32760, length: 16, wantErr: true},
		{name: "begin+length overflows uint32", begin: ^uint32(0), length: 16, wantErr: true},
		{name: "final piece overrun", index: 3, begin: 90, length: 16, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := c.validateRequest(tt.index, tt.begin, tt.length)
			if tt.wantErr && err == nil {
				t.Fatalf("validateRequest(%d, %d, %d) = nil, want an error", tt.index, tt.begin, tt.length)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateRequest(%d, %d, %d) = %v, want nil", tt.index, tt.begin, tt.length, err)
			}
		})
	}
}

// TestOversizedRequestDropsPeer proves the allocation primitive is closed: a
// peer asking for a 1 GiB "block" must lose its connection instead of making
// us allocate.
func TestOversizedRequestDropsPeer(t *testing.T) {
	var served int32
	readBlock := func(index, begin, length uint32) ([]byte, error) {
		served++
		return make([]byte, length), nil
	}
	client, server := dialTestPeer(t, func(uint32) bool { return true }, readBlock)
	if err := client.SendUnchoke(); err != nil {
		t.Fatalf("SendUnchoke: %v", err)
	}

	runDone := make(chan struct{})
	go func() { client.Run(); close(runDone) }()

	// Discard whatever the client sends us; we only care that it hangs up.
	go io.Copy(io.Discard, server)

	req := MsgRequestPayload{Index: 0, Begin: 0, Length: 1 << 30}
	writeFrame(t, server, MsgRequest, req.Serialize())

	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("client did not drop the peer after an oversized request")
	}
	if served != 0 {
		t.Fatalf("readBlockFromDisk was called %d times for an invalid request", served)
	}
}

// TestUploadedBytesAreCounted proves serveRequest's accounting, not just
// that it serves the block: Client.Uploaded is what internal/torrent's
// flushUploaded reads to build the aggregate upload total reported to
// trackers and stats.
func TestUploadedBytesAreCounted(t *testing.T) {
	const blockLen = 4096
	want := bytes.Repeat([]byte{0xAB}, blockLen)
	readBlock := func(index, begin, length uint32) ([]byte, error) {
		return want, nil
	}
	client, server := dialTestPeer(t, func(uint32) bool { return true }, readBlock)
	if err := client.SendUnchoke(); err != nil {
		t.Fatalf("SendUnchoke: %v", err)
	}
	go client.Run()

	if body := readFrame(t, server); MessageID(body[0]) != MsgUnchoke {
		t.Fatalf("expected Unchoke first, got %s", MessageID(body[0]))
	}
	if body := readFrame(t, server); MessageID(body[0]) != MsgInterested {
		t.Fatalf("expected Interested second, got %s", MessageID(body[0]))
	}

	if got := client.Uploaded(); got != 0 {
		t.Fatalf("Uploaded() = %d before any request was served, want 0", got)
	}

	req := MsgRequestPayload{Index: 0, Begin: 0, Length: blockLen}
	writeFrame(t, server, MsgRequest, req.Serialize())

	body := readFrame(t, server)
	if MessageID(body[0]) != MsgPiece {
		t.Fatalf("expected Piece, got %s", MessageID(body[0]))
	}
	var piece MsgPiecePayload
	if err := piece.Parse(body[1:]); err != nil {
		t.Fatalf("parse Piece: %v", err)
	}
	if !bytes.Equal(piece.Block, want) {
		t.Fatal("served block content does not match what readBlockFromDisk returned")
	}

	if got := client.Uploaded(); got != blockLen {
		t.Fatalf("Uploaded() = %d after serving one block, want %d", got, blockLen)
	}
}

func TestMalformedBitfieldDropsPeer(t *testing.T) {
	client, server := dialTestPeer(t, func(uint32) bool { return false }, nil)

	runDone := make(chan struct{})
	go func() { client.Run(); close(runDone) }()
	go io.Copy(io.Discard, server)

	// testTorrent has 4 pieces, so a legal bitfield is exactly 1 byte.
	writeFrame(t, server, MsgBitfield, make([]byte, 64))

	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("client accepted a bitfield of the wrong width")
	}
}

// TestBlockRoundTrip drives the full request/response path: the session hands
// work to WorkQueue, the client emits a Request, and the reply comes back out
// on Results.
func TestBlockRoundTrip(t *testing.T) {
	client, server := dialTestPeer(t, func(uint32) bool { return false }, nil)

	go client.Run()

	// The client sends Interested on startup; consume it.
	if body := readFrame(t, server); MessageID(body[0]) != MsgInterested {
		t.Fatalf("expected Interested first, got %s", MessageID(body[0]))
	}

	writeFrame(t, server, MsgBitfield, []byte{0xF0}) // we have all 4 pieces
	writeFrame(t, server, MsgUnchoke, nil)

	// Wait for the client to observe the unchoke before queueing work.
	deadline := time.Now().Add(2 * time.Second)
	for client.PeerChoking() {
		if time.Now().After(deadline) {
			t.Fatal("client never processed the Unchoke")
		}
		time.Sleep(5 * time.Millisecond)
	}

	client.WorkQueue <- &BlockRequest{Index: 1, Begin: MaxBlockLength, Length: MaxBlockLength}

	server.SetReadDeadline(time.Now().Add(5 * time.Second))
	body := readFrame(t, server)
	if MessageID(body[0]) != MsgRequest {
		t.Fatalf("expected Request, got %s", MessageID(body[0]))
	}
	var req MsgRequestPayload
	if err := req.Parse(body[1:]); err != nil {
		t.Fatalf("parse Request: %v", err)
	}
	if req.Index != 1 || req.Begin != MaxBlockLength || req.Length != MaxBlockLength {
		t.Fatalf("got request (%d, %d, %d), want (1, %d, %d)", req.Index, req.Begin, req.Length, MaxBlockLength, MaxBlockLength)
	}

	payload := MsgPiecePayload{Index: 1, Begin: MaxBlockLength, Block: make([]byte, MaxBlockLength)}
	payload.Block[0] = 0xAB
	writeFrame(t, server, MsgPiece, payload.Serialize())

	select {
	case got := <-client.Results:
		if got.Index != 1 || got.Begin != MaxBlockLength || len(got.Block) != MaxBlockLength || got.Block[0] != 0xAB {
			t.Fatalf("unexpected block: index=%d begin=%d len=%d", got.Index, got.Begin, len(got.Block))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("block never reached Results")
	}

	if client.LastPieceReceivedUnix() == 0 {
		t.Fatal("LastPieceReceived was not updated")
	}
}

// TestCloseUnblocksWriteLoop covers the goroutine leak: writeLoop used to range
// over WorkQueue, which is never closed, so every disconnect leaked it.
func TestCloseUnblocksWriteLoop(t *testing.T) {
	client, _ := dialTestPeer(t, func(uint32) bool { return false }, nil)

	stopped := make(chan struct{})
	go func() { client.writeLoop(); close(stopped) }()

	client.Close()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("writeLoop did not exit after Close")
	}

	// Close must also be idempotent — the session calls it from several places.
	client.Close()
	client.Close()
}

func TestSendAfterCloseFails(t *testing.T) {
	client, _ := dialTestPeer(t, func(uint32) bool { return false }, nil)
	client.Close()
	if err := client.SendHave(0); err == nil {
		t.Fatal("SendHave on a closed client returned nil")
	}
}

// TestSendNeverBlocks guards the property the session depends on: it broadcasts
// Have while holding its mutex, so a peer that has stopped reading must not be
// able to block the caller.
func TestSendNeverBlocks(t *testing.T) {
	client, _ := dialTestPeer(t, func(uint32) bool { return false }, nil)
	// No sendLoop is running, so outbound fills up and stays full.

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < outboundQueueSize*4; i++ {
			_ = client.SendHave(uint32(i % testTorrent.NumPieces))
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SendHave blocked when the outbound queue was full")
	}
}

// TestEventsFireOnStateChanges is the contract the torrent actor depends on:
// Have, Bitfield, and choke/interest changes must surface as Events, not just
// as accessor state a poller would have to notice on its own.
func TestEventsFireOnStateChanges(t *testing.T) {
	client, server := dialTestPeer(t, func(uint32) bool { return false }, nil)
	go client.Run()

	// Consume the Interested the client sends on startup.
	readFrame(t, server)

	writeFrame(t, server, MsgBitfield, []byte{0xF0})
	if ev := waitEvent(t, client.Events); ev.Kind != EventBitfield {
		t.Fatalf("first event = %s, want Bitfield", ev.Kind)
	}

	writeFrame(t, server, MsgUnchoke, nil)
	if ev := waitEvent(t, client.Events); ev.Kind != EventChokeChanged {
		t.Fatalf("event = %s, want ChokeChanged", ev.Kind)
	}
	if client.PeerChoking() {
		t.Fatal("PeerChoking() is still true after an Unchoke event fired")
	}

	writeFrame(t, server, MsgChoke, nil)
	if ev := waitEvent(t, client.Events); ev.Kind != EventChokeChanged {
		t.Fatalf("event = %s, want ChokeChanged", ev.Kind)
	}

	writeFrame(t, server, MsgInterested, nil)
	if ev := waitEvent(t, client.Events); ev.Kind != EventInterestedChanged {
		t.Fatalf("event = %s, want InterestedChanged", ev.Kind)
	}

	havePayload := MsgHavePayload{PieceIndex: 2}
	writeFrame(t, server, MsgHave, havePayload.Serialize())
	ev := waitEvent(t, client.Events)
	if ev.Kind != EventHave || ev.PieceIndex != 2 {
		t.Fatalf("event = %+v, want Have for piece 2", ev)
	}
	if !client.HasPiece(2) {
		t.Fatal("HasPiece(2) is false after the Have event fired")
	}
}

// TestEventsCloseWithResults matters for the fan-in pattern the actor uses:
// both channels must reach a terminal state together so a select over both
// cannot leak.
func TestEventsCloseWithResults(t *testing.T) {
	client, server := dialTestPeer(t, func(uint32) bool { return false }, nil)
	runDone := make(chan struct{})
	go func() { client.Run(); close(runDone) }()

	server.Close()
	<-runDone

	if _, ok := <-client.Events; ok {
		t.Fatal("Events was not closed after Run returned")
	}
	if _, ok := <-client.Results; ok {
		t.Fatal("Results was not closed after Run returned")
	}
}

func waitEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("Events closed while waiting for an event")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an event")
		return Event{}
	}
}
