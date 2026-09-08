package torrent

import (
	"net"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
)

// readExtendedHandshakeFrom dials addr as a bare-bones real peer (real
// handshake, extension bit set) and reads back whatever the Torrent on the
// other end sends as its own extended handshake — the wire-level proof that
// Torrent.uploadOnlySafe actually reaches a real peer.Client's real
// handshake, not just that the callback is wired in the abstract (already
// covered in detail by internal/peer's own BEP 21 tests).
func readExtendedHandshakeFrom(t *testing.T, addr string, infoHash metainfo.Hash) fakeExtHandshake {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	var id [20]byte
	copy(id[:], "-TEST01-uploadonly00")
	if _, err := conn.Write(peer.NewHandshake(infoHash, id).Serialize()); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	if _, err := peer.ReadHandshake(conn); err != nil {
		t.Fatalf("read reply handshake: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		id, payload, err := readMsg(conn)
		if err != nil {
			t.Fatalf("readMsg: %v", err)
		}
		if id != peer.MsgExtended || len(payload) < 1 || payload[0] != 0 {
			continue // not the extended handshake itself; keep reading
		}
		var hs fakeExtHandshake
		if err := bencode.Unmarshal(payload[1:], &hs); err != nil {
			t.Fatalf("parse extended handshake: %v", err)
		}
		return hs
	}
}

// TestExtendedHandshakeAdvertisesUploadOnlyWhenSeeding proves a real,
// already-complete (pre-seeded) Torrent announces upload_only:1 to a real
// inbound peer, straight off the real wire.
func TestExtendedHandshakeAdvertisesUploadOnlyWhenSeeding(t *testing.T) {
	mi, content := buildTorrent(t, "seeder-uo.bin", 16384, []fileSpec{{length: 16384 * 2}})
	tr := newPreSeededTorrent(t, "seeder-uo.bin", mi, content, newTestConfig(t))
	runInBackground(t, tr)
	waitForState(t, tr, StateSeeding, 5*time.Second)

	pi := listenAndRoute(t, tr)
	hs := readExtendedHandshakeFrom(t, pi.Addr(), mi.InfoHash)

	if hs.M["upload_only"] == 0 {
		t.Fatal("real Torrent's extended handshake did not advertise the upload_only m entry")
	}
	if hs.UploadOnly != 1 {
		t.Fatalf("upload_only = %d, want 1 for an already-seeding torrent", hs.UploadOnly)
	}
}

// TestExtendedHandshakeOmitsUploadOnlyWhileDownloading is the control: a
// fresh, incomplete Torrent must not claim upload_only.
func TestExtendedHandshakeOmitsUploadOnlyWhileDownloading(t *testing.T) {
	mi, _ := buildTorrent(t, "downloader-uo.bin", 16384, []fileSpec{{length: 16384 * 2}})
	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	waitForState(t, tr, StateDownloading, 5*time.Second)

	pi := listenAndRoute(t, tr)
	hs := readExtendedHandshakeFrom(t, pi.Addr(), mi.InfoHash)

	if hs.UploadOnly != 0 {
		t.Fatalf("upload_only = %d, want 0 (omitted) for an incomplete torrent", hs.UploadOnly)
	}
}
