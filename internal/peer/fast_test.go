package peer

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bitfield"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

func mustPeerInfo(addr *net.TCPAddr) tracker.PeerInfo {
	return tracker.PeerInfo{IP: addr.IP, Port: uint16(addr.Port)}
}

func TestSendInitialStateSendsHaveAllWhenComplete(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return true }})
	go client.Run()

	if body := readFrame(t, server); MessageID(body[0]) != MsgInterested {
		t.Fatalf("expected Interested first, got %s", MessageID(body[0]))
	}
	body := readFrame(t, server)
	if MessageID(body[0]) != MsgHaveAll {
		t.Fatalf("expected HaveAll, got %s", MessageID(body[0]))
	}
	if len(body) != 1 {
		t.Fatalf("HaveAll frame carried a payload: %d bytes", len(body)-1)
	}
}

func TestSendInitialStateSendsHaveNoneWhenEmpty(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return false }})
	go client.Run()

	readFrame(t, server) // Interested
	body := readFrame(t, server)
	if MessageID(body[0]) != MsgHaveNone {
		t.Fatalf("expected HaveNone, got %s", MessageID(body[0]))
	}
}

func TestSendInitialStateSendsBitfieldWhenPartial(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(i uint32) bool { return i == 1 }})
	go client.Run()

	readFrame(t, server) // Interested
	body := readFrame(t, server)
	if MessageID(body[0]) != MsgBitfield {
		t.Fatalf("expected a plain Bitfield for a partial set, got %s", MessageID(body[0]))
	}
	bf, err := bitfield.FromBytes(body[1:], testTorrent.NumPieces)
	if err != nil {
		t.Fatalf("parsing sent bitfield: %v", err)
	}
	if !bf.Has(1) || bf.Has(0) || bf.Has(2) || bf.Has(3) {
		t.Fatalf("sent bitfield does not match HasPiece(1)=true, rest false: %v", bf.Bytes())
	}
}

func TestReceivesHaveAllSetsFullBitfield(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return false }})
	go client.Run()
	readFrame(t, server) // Interested
	readFrame(t, server) // our initial state

	writeFrame(t, server, MsgHaveAll, nil)
	waitEvent(t, client.Events) // Bitfield

	for i := uint32(0); i < uint32(testTorrent.NumPieces); i++ {
		if !client.HasPiece(i) {
			t.Fatalf("HasPiece(%d) = false after HaveAll", i)
		}
	}
}

func TestReceivesHaveNoneFiresEventButLeavesBitfieldEmpty(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return false }})
	go client.Run()
	readFrame(t, server) // Interested
	readFrame(t, server) // our initial state

	writeFrame(t, server, MsgHaveNone, nil)
	if ev := waitEvent(t, client.Events); ev.Kind != EventBitfield {
		t.Fatalf("event = %s, want Bitfield", ev.Kind)
	}

	for i := uint32(0); i < uint32(testTorrent.NumPieces); i++ {
		if client.HasPiece(i) {
			t.Fatalf("HasPiece(%d) = true after HaveNone", i)
		}
	}
}

func TestHaveAllWithNonEmptyPayloadDropsPeer(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return false }})
	runDone := make(chan struct{})
	go func() { client.Run(); close(runDone) }()
	readFrame(t, server) // Interested
	readFrame(t, server) // our initial state

	writeFrame(t, server, MsgHaveAll, []byte{0x01})

	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("connection was not dropped for a malformed HaveAll")
	}
}

// TestHaveAllBeforeMetadataAppliesOnUpgrade covers the magnet-link timing
// window: a peer's HaveAll arrives before NumPieces is known, the same
// problem pendingBitfield solves for a plain Bitfield.
func TestHaveAllBeforeMetadataAppliesOnUpgrade(t *testing.T) {
	magnetTorrent := TorrentInfo{InfoHash: testTorrent.InfoHash} // NumPieces == 0
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
		hs := make([]byte, 68)
		if _, err := io.ReadFull(conn, hs); err != nil {
			acceptCh <- accepted{err: err}
			return
		}
		var remoteID [20]byte
		copy(remoteID[:], "-TEST01-peer00000000")
		if _, err := conn.Write(NewHandshake(magnetTorrent.InfoHash, remoteID).Serialize()); err != nil {
			acceptCh <- accepted{err: err}
			return
		}
		acceptCh <- accepted{conn: conn}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	client, err := NewClient(mustPeerInfo(addr), magnetTorrent, [20]byte{}, Callbacks{}, Limits{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	got := <-acceptCh
	if got.err != nil {
		t.Fatalf("accept side: %v", got.err)
	}
	server := got.conn
	t.Cleanup(func() { server.Close() })

	go client.Run()
	readFrame(t, server) // Interested
	readFrame(t, server) // HaveNone (NumPieces==0, so that's what we send)

	writeFrame(t, server, MsgHaveAll, nil)
	waitEvent(t, client.Events) // Bitfield

	if client.HasPiece(0) {
		t.Fatal("HasPiece before UpgradeMetadata should still be false — the real width isn't known yet")
	}

	upgraded := testTorrent
	if err := client.UpgradeMetadata(upgraded); err != nil {
		t.Fatalf("UpgradeMetadata: %v", err)
	}
	for i := uint32(0); i < uint32(upgraded.NumPieces); i++ {
		if !client.HasPiece(i) {
			t.Fatalf("HasPiece(%d) = false after UpgradeMetadata applied a pending HaveAll", i)
		}
	}
}

func TestAllowedFastGrantsSpecificPiecesOnly(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return false }})
	go client.Run()
	readFrame(t, server) // Interested
	readFrame(t, server) // our initial state

	payload := MsgHavePayload{PieceIndex: 2}
	writeFrame(t, server, MsgAllowedFast, payload.Serialize())

	deadline := time.Now().Add(2 * time.Second)
	for !client.IsAllowedFast(2) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !client.IsAllowedFast(2) {
		t.Fatal("IsAllowedFast(2) = false after receiving AllowedFast for piece 2")
	}
	if client.IsAllowedFast(0) || client.IsAllowedFast(1) || client.IsAllowedFast(3) {
		t.Fatal("IsAllowedFast granted a piece that was never announced")
	}
}

func TestReceivesRejectRequestEvent(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return false }})
	go client.Run()
	readFrame(t, server) // Interested
	readFrame(t, server) // our initial state

	reject := MsgRequestPayload{Index: 2, Begin: 16384, Length: 16384}
	writeFrame(t, server, MsgRejectRequest, reject.Serialize())

	ev := waitEvent(t, client.Events)
	if ev.Kind != EventRejectRequest {
		t.Fatalf("event = %s, want RejectRequest", ev.Kind)
	}
	if ev.PieceIndex != 2 || ev.Begin != 16384 || ev.Length != 16384 {
		t.Fatalf("got %+v, want index=2 begin=16384 length=16384", ev)
	}
}

// TestServeRequestSendsRejectWhenFastPeerIsDeclined proves the send side:
// declining a request from a Fast-capable peer gets an explicit
// RejectRequest instead of silence.
func TestServeRequestSendsRejectWhenFastPeerIsDeclined(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return false }})
	go client.Run()
	readFrame(t, server) // Interested
	readFrame(t, server) // our initial state
	readFrame(t, server) // our extended handshake (dialTestPeer's fixture advertises BEP 10 too)

	req := MsgRequestPayload{Index: 0, Begin: 0, Length: 16384}
	writeFrame(t, server, MsgRequest, req.Serialize())

	body := readFrame(t, server)
	if MessageID(body[0]) != MsgRejectRequest {
		t.Fatalf("expected RejectRequest, got %s", MessageID(body[0]))
	}
	var got MsgRequestPayload
	if err := got.Parse(body[1:]); err != nil {
		t.Fatalf("parse RejectRequest payload: %v", err)
	}
	if got != req {
		t.Fatalf("got %+v, want %+v echoed back", got, req)
	}
}

// TestServeRequestStaysSilentForNonFastPeer proves a peer that never
// advertised Fast extension support gets the original BEP 3 behavior
// (silence) rather than a message it has no way to understand.
func TestServeRequestStaysSilentForNonFastPeer(t *testing.T) {
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
		hs := make([]byte, 68)
		if _, err := io.ReadFull(conn, hs); err != nil {
			acceptCh <- accepted{err: err}
			return
		}
		// A handshake advertising BEP 10 extensions but deliberately NOT
		// BEP 6 Fast support.
		reply := &Handshake{Pstrlen: protocolStringLen}
		copy(reply.Pstr[:], ProtocolString)
		reply.Reserved[extensionReservedByte] |= extensionReservedBit
		copy(reply.InfoHash[:], testTorrent.InfoHash[:])
		copy(reply.PeerID[:], "-TEST01-nofast000000")
		if _, err := conn.Write(reply.Serialize()); err != nil {
			acceptCh <- accepted{err: err}
			return
		}
		acceptCh <- accepted{conn: conn}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	client, err := NewClient(mustPeerInfo(addr), testTorrent, [20]byte{}, Callbacks{HasPiece: func(uint32) bool { return false }}, Limits{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	got := <-acceptCh
	if got.err != nil {
		t.Fatalf("accept side: %v", got.err)
	}
	server := got.conn
	t.Cleanup(func() { server.Close() })

	go client.Run()
	readFrame(t, server) // Interested
	readFrame(t, server) // our initial state (a plain Bitfield/nothing — this peer doesn't get Fast messages either way)

	req := MsgRequestPayload{Index: 0, Begin: 0, Length: 16384}
	writeFrame(t, server, MsgRequest, req.Serialize())

	server.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 1)
	if _, err := server.Read(buf); err == nil {
		t.Fatal("a non-Fast peer received something after a declined request, want silence")
	}
}
