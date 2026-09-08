package peer

import (
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
)

// TestExtendedHandshakeAdvertisesUploadOnlySupport proves the "m" dict
// carries upload_only alongside ut_metadata/ut_pex.
func TestExtendedHandshakeAdvertisesUploadOnlySupport(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return false }})
	go client.Run()

	readFrame(t, server) // Interested
	readInitialStateFrame(t, server)
	body := readFrame(t, server)
	if MessageID(body[0]) != MsgExtended {
		t.Fatalf("expected our extended handshake, got %s", MessageID(body[0]))
	}
	var hs extHandshakeWire
	if err := bencode.Unmarshal(body[2:], &hs); err != nil {
		t.Fatalf("parse extended handshake: %v", err)
	}
	if hs.M["upload_only"] != localUploadOnlyID {
		t.Fatalf("m[upload_only] = %d, want %d", hs.M["upload_only"], localUploadOnlyID)
	}
}

// TestExtendedHandshakeAnnouncesUploadOnlyInitialState proves the
// Callbacks.UploadOnly hook actually reaches the "upload_only" top-level
// key when true.
func TestExtendedHandshakeAnnouncesUploadOnlyInitialState(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{
		HasPiece:   func(uint32) bool { return false },
		UploadOnly: func() bool { return true },
	})
	go client.Run()

	readFrame(t, server) // Interested
	readInitialStateFrame(t, server)
	body := readFrame(t, server)
	var hs extHandshakeWire
	if err := bencode.Unmarshal(body[2:], &hs); err != nil {
		t.Fatalf("parse extended handshake: %v", err)
	}
	if hs.UploadOnly != 1 {
		t.Fatalf("upload_only = %d, want 1", hs.UploadOnly)
	}
}

// TestExtendedHandshakeUploadOnlyKeySetsPeerState proves a peer's own
// handshake carrying upload_only:1 is reflected in PeerUploadOnly()
// immediately, before any live update message.
func TestExtendedHandshakeUploadOnlyKeySetsPeerState(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return false }})
	go client.Run()

	readFrame(t, server) // Interested
	readInitialStateFrame(t, server)
	readFrame(t, server) // our extended handshake

	if client.PeerUploadOnly() {
		t.Fatal("PeerUploadOnly() = true before any handshake from the peer")
	}

	hsPayload := append([]byte{0}, mustMarshal(t, extHandshakeWire{
		M:          map[string]int{"upload_only": 7},
		UploadOnly: 1,
	})...)
	writeFrame(t, server, MsgExtended, hsPayload)
	waitEvent(t, client.Events) // ExtendedHandshake

	if !client.PeerUploadOnly() {
		t.Fatal("PeerUploadOnly() = false after a handshake announcing upload_only:1")
	}
	if !client.SupportsUploadOnly() {
		t.Fatal("SupportsUploadOnly() = false after a handshake advertising the m entry")
	}
}

// TestSendUploadOnlyAddressesThePeersOwnID mirrors
// TestSendPEXAddressesThePeersOwnID: the live update goes to whatever id
// the peer chose for itself, not our own, and the payload is the raw
// single byte BEP 21 specifies (not bencoded).
func TestSendUploadOnlyAddressesThePeersOwnID(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return false }})
	go client.Run()

	readFrame(t, server) // Interested
	readInitialStateFrame(t, server)
	readFrame(t, server) // our extended handshake

	const peerUploadOnlyID = 9
	hsPayload := append([]byte{0}, mustMarshal(t, extHandshakeWire{
		M: map[string]int{"upload_only": peerUploadOnlyID},
	})...)
	writeFrame(t, server, MsgExtended, hsPayload)
	waitEvent(t, client.Events) // ExtendedHandshake

	if !client.SupportsUploadOnly() {
		t.Fatal("SupportsUploadOnly() = false after a handshake advertising it")
	}

	if err := client.SendUploadOnly(true); err != nil {
		t.Fatalf("SendUploadOnly: %v", err)
	}

	body := readFrame(t, server)
	if MessageID(body[0]) != MsgExtended {
		t.Fatalf("expected Extended, got %s", MessageID(body[0]))
	}
	if int(body[1]) != peerUploadOnlyID {
		t.Fatalf("extended-message-id = %d, want %d (the peer's own advertised id)", body[1], peerUploadOnlyID)
	}
	if len(body) != 3 || body[2] != 1 {
		t.Fatalf("payload = %v, want a single raw byte [1] (BEP 21 is not bencoded)", body[2:])
	}
}

// TestSendUploadOnlyToAPeerWithoutSupportIsANoOp mirrors
// TestSendPEXToAPeerWithoutSupportIsANoOp.
func TestSendUploadOnlyToAPeerWithoutSupportIsANoOp(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return false }})
	go client.Run()

	readFrame(t, server) // Interested
	readInitialStateFrame(t, server)
	readFrame(t, server) // our extended handshake

	if err := client.SendUploadOnly(true); err != nil {
		t.Fatalf("SendUploadOnly: %v", err)
	}

	server.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 1)
	if _, err := server.Read(buf); err == nil {
		t.Fatal("SendUploadOnly sent something to a peer that never advertised support")
	}
}

// TestReceivesUploadOnlyLiveUpdateFromPeer drives the server side: a peer
// sends a live upload_only message (not via the handshake) addressed to
// our advertised id, and PeerUploadOnly reflects it.
func TestReceivesUploadOnlyLiveUpdateFromPeer(t *testing.T) {
	client, server := dialTestPeer(t, Callbacks{HasPiece: func(uint32) bool { return false }})
	go client.Run()

	readFrame(t, server) // Interested
	readInitialStateFrame(t, server)
	readFrame(t, server) // our extended handshake

	writeFrame(t, server, MsgExtended, []byte{localUploadOnlyID, 1})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if client.PeerUploadOnly() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !client.PeerUploadOnly() {
		t.Fatal("PeerUploadOnly() never became true after a live upload_only=1 message")
	}

	writeFrame(t, server, MsgExtended, []byte{localUploadOnlyID, 0})
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !client.PeerUploadOnly() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if client.PeerUploadOnly() {
		t.Fatal("PeerUploadOnly() never went back to false after a live upload_only=0 message")
	}
}
