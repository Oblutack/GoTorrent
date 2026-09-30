package peer

import (
	"net"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/mse"
	"github.com/Oblutack/GoTorrent/internal/utp"
)

func TestMsgHashRequestPayloadRoundTrip(t *testing.T) {
	p := MsgHashRequestPayload{BaseLayer: 1, Index: 2, Length: 4, ProofLayers: 3}
	for i := range p.PiecesRoot {
		p.PiecesRoot[i] = byte(i)
	}
	raw := p.Serialize()
	if len(raw) != hashPayloadFixedLen {
		t.Fatalf("Serialize length = %d, want %d", len(raw), hashPayloadFixedLen)
	}
	var got MsgHashRequestPayload
	if err := got.Parse(raw); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got != p {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, p)
	}
}

func TestMsgHashRequestPayloadRejectsWrongLength(t *testing.T) {
	var p MsgHashRequestPayload
	if err := p.Parse(make([]byte, hashPayloadFixedLen-1)); err == nil {
		t.Fatal("Parse accepted a too-short payload")
	}
	if err := p.Parse(make([]byte, hashPayloadFixedLen+1)); err == nil {
		t.Fatal("Parse accepted a too-long payload")
	}
}

func TestMsgHashesPayloadRoundTrip(t *testing.T) {
	p := MsgHashesPayload{BaseLayer: 1, Index: 2, Length: 4, ProofLayers: 0}
	for i := range p.PiecesRoot {
		p.PiecesRoot[i] = byte(i)
	}
	for i := 0; i < 4; i++ {
		var h [32]byte
		h[0] = byte(i + 1)
		p.Hashes = append(p.Hashes, h)
	}
	raw := p.Serialize()
	wantLen := hashPayloadFixedLen + 32*4
	if len(raw) != wantLen {
		t.Fatalf("Serialize length = %d, want %d", len(raw), wantLen)
	}
	var got MsgHashesPayload
	if err := got.Parse(raw); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.PiecesRoot != p.PiecesRoot || got.BaseLayer != p.BaseLayer || got.Index != p.Index ||
		got.Length != p.Length || got.ProofLayers != p.ProofLayers || len(got.Hashes) != len(p.Hashes) {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, p)
	}
	for i := range got.Hashes {
		if got.Hashes[i] != p.Hashes[i] {
			t.Fatalf("hash %d mismatch: got %x, want %x", i, got.Hashes[i], p.Hashes[i])
		}
	}
}

func TestMsgHashesPayloadRejectsMisalignedHashList(t *testing.T) {
	raw := make([]byte, hashPayloadFixedLen+10) // not a multiple of 32
	var p MsgHashesPayload
	if err := p.Parse(raw); err == nil {
		t.Fatal("Parse accepted a hash list not a multiple of 32 bytes")
	}
}

func TestMsgHashesPayloadRejectsTooShort(t *testing.T) {
	var p MsgHashesPayload
	if err := p.Parse(make([]byte, hashPayloadFixedLen-1)); err == nil {
		t.Fatal("Parse accepted a payload shorter than the fixed fields alone")
	}
}

func TestHandshakeAdvertisesV2HashSupport(t *testing.T) {
	hs := NewHandshake([20]byte{1}, [20]byte{2})
	if !hs.SupportsV2Hashes() {
		t.Fatal("NewHandshake did not advertise BEP 52 hash-exchange support")
	}
	// No collision with the Fast extension's own bit in the same byte.
	if !hs.SupportsFast() {
		t.Fatal("advertising the v2 bit must not clobber the Fast extension's own bit in the same byte")
	}
}

// connectedClientPair builds two real *Client instances, one accepting and
// one dialing, over a real TCP loopback connection — the same "real
// fixtures over mocks" bar this project's own peer-package tests already
// hold elsewhere (fast_test.go's own hand-rolled-server style tests
// notwithstanding — this one specifically needs two genuine Client
// instances to prove the send/serve/reply round trip end to end, not
// just this package's own wire parsing).
func connectedClientPair(t *testing.T, serverCallbacks, clientCallbacks Callbacks) (server, client *Client) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	type acceptResult struct {
		c   *Client
		err error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			acceptCh <- acceptResult{err: err}
			return
		}
		hs, err := ReadHandshake(conn)
		if err != nil {
			acceptCh <- acceptResult{err: err}
			return
		}
		c, err := AcceptClient(conn, hs, testTorrent, [20]byte{9}, serverCallbacks, Limits{})
		acceptCh <- acceptResult{c: c, err: err}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	client, err = NewClient(mustPeerInfo(addr), testTorrent, [20]byte{8}, clientCallbacks, Limits{}, nil, mse.PolicyDisabled, utp.PolicyDisabled, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	res := <-acceptCh
	if res.err != nil {
		t.Fatalf("accept side: %v", res.err)
	}
	server = res.c
	t.Cleanup(func() { server.Close() })

	go server.Run()
	go client.Run()
	return server, client
}

func drainEventsAndResults(c *Client) {
	go func() {
		for range c.Events {
		}
	}()
	go func() {
		for range c.Results {
		}
	}()
	go func() {
		for range c.MetadataPieces {
		}
	}()
	go func() {
		for range c.PEXUpdates {
		}
	}()
	go func() {
		for range c.HolepunchMessages {
		}
	}()
}

func TestSendHashRequestGetsARealHashesReply(t *testing.T) {
	wantHashes := [][32]byte{{1}, {2}}
	serverCallbacks := Callbacks{
		ServeHashes: func(req MsgHashRequestPayload) ([][32]byte, bool) {
			return wantHashes, true
		},
	}
	server, client := connectedClientPair(t, serverCallbacks, Callbacks{})
	drainEventsAndResults(server)
	drainEventsAndResults(client)

	var root [32]byte
	root[0] = 0xAB
	req := MsgHashRequestPayload{PiecesRoot: root, BaseLayer: 0, Index: 0, Length: 2, ProofLayers: 0}
	if err := client.SendHashRequest(req); err != nil {
		t.Fatalf("SendHashRequest: %v", err)
	}

	select {
	case msg := <-client.HashMessages:
		if msg.Kind != HashMessageHashes {
			t.Fatalf("Kind = %v, want HashMessageHashes", msg.Kind)
		}
		if msg.PiecesRoot != root {
			t.Fatalf("PiecesRoot = %x, want %x", msg.PiecesRoot, root)
		}
		if len(msg.Hashes) != 2 || msg.Hashes[0] != wantHashes[0] || msg.Hashes[1] != wantHashes[1] {
			t.Fatalf("Hashes = %v, want %v", msg.Hashes, wantHashes)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a hashes reply")
	}
}

func TestSendHashRequestGetsARealRejectWhenNotServable(t *testing.T) {
	// A nil ServeHashes (the zero-value Callbacks) must reject, never stay
	// silent - BEP 52 requires an answer either way.
	server, client := connectedClientPair(t, Callbacks{}, Callbacks{})
	drainEventsAndResults(server)
	drainEventsAndResults(client)

	req := MsgHashRequestPayload{Index: 0, Length: 2}
	if err := client.SendHashRequest(req); err != nil {
		t.Fatalf("SendHashRequest: %v", err)
	}

	select {
	case msg := <-client.HashMessages:
		if msg.Kind != HashMessageReject {
			t.Fatalf("Kind = %v, want HashMessageReject", msg.Kind)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a hash reject")
	}
}
