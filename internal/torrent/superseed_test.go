package torrent

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
)

// dialRawSuperSeedPeer dials into a real Torrent's listener as a bare-bones
// real peer (real handshake, no client machinery beyond it) so a test can
// observe and drive the exact frames Config.SuperSeeding produces on the
// actual wire, the reversed-direction counterpart to internal/peer's own
// dialTestPeer.
func dialRawSuperSeedPeer(t *testing.T, addr string, infoHash metainfo.Hash) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	var id [20]byte
	copy(id[:], "-TEST01-superseed000")
	if _, err := conn.Write(peer.NewHandshake(infoHash, id).Serialize()); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	if _, err := peer.ReadHandshake(conn); err != nil {
		t.Fatalf("read reply handshake: %v", err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn
}

// readNextHave skips any frame that is not a plain Have (Interested,
// Unchoke, the extended handshake, ...) and returns the piece index of the
// first one it finds.
func readNextHave(t *testing.T, conn net.Conn) uint32 {
	t.Helper()
	for {
		id, payload, err := readMsg(conn)
		if err != nil {
			t.Fatalf("readMsg: %v", err)
		}
		if id != peer.MsgHave {
			continue
		}
		var have peer.MsgHavePayload
		if err := have.Parse(payload); err != nil {
			t.Fatalf("parse Have: %v", err)
		}
		return have.PieceIndex
	}
}

// TestSuperSeedingRevealsOnePieceAtATime drives a real, already-complete
// (pre-seeded) super-seeding Torrent from a raw test connection playing a
// single peer, and proves the whole BEP 16 cycle end to end on the wire:
// the initial state is a single targeted Have (never HaveAll, even though
// this Torrent genuinely has every piece), each self-reported Have we send
// back earns exactly one new piece in return, and once every piece has been
// released this way the torrent graduates — sweeping every piece to the
// connection in one go instead of continuing to trickle one at a time.
func TestSuperSeedingRevealsOnePieceAtATime(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "superseed.bin", pieceLength, []fileSpec{{length: pieceLength * 4}})

	cfg := newTestConfig(t)
	cfg.SuperSeeding = true
	seeder := newPreSeededTorrent(t, "superseed.bin", mi, content, cfg)
	runInBackground(t, seeder)
	waitForState(t, seeder, StateSeeding, 5*time.Second)

	pi := listenAndRoute(t, seeder)
	conn := dialRawSuperSeedPeer(t, pi.Addr(), mi.InfoHash)

	// Piece 0 first — superSeedAssign round-robins from index 0.
	if got := readNextHave(t, conn); got != 0 {
		t.Fatalf("initial super-seed Have = %d, want 0", got)
	}

	// Report having obtained each piece in turn (0, then 1, then 2); each
	// one earns exactly the next piece in sequence, right up to piece 3.
	for reported := uint32(0); reported < 3; reported++ {
		payload := peer.MsgHavePayload{PieceIndex: reported}
		if err := writeMsg(conn, peer.MsgHave, payload.Serialize()); err != nil {
			t.Fatalf("write Have(%d): %v", reported, err)
		}
		want := reported + 1
		if got := readNextHave(t, conn); got != want {
			t.Fatalf("after reporting piece %d, next assigned Have = %d, want %d", reported, got, want)
		}
	}

	// Reporting the last piece (3, just assigned) pushes releasedN to
	// numPieces (4): graduation fires instead of a fifth single-piece
	// advance, sweeping every piece to this connection in index order.
	payload := peer.MsgHavePayload{PieceIndex: 3}
	if err := writeMsg(conn, peer.MsgHave, payload.Serialize()); err != nil {
		t.Fatalf("write Have(3): %v", err)
	}
	for want := uint32(0); want < 4; want++ {
		if got := readNextHave(t, conn); got != want {
			t.Fatalf("graduation sweep: got Have(%d), want Have(%d)", got, want)
		}
	}
}

// TestSuperSeedingLeecherDownloadsFullTorrent is the functional proof behind
// the wire-level one above: a real leecher, given nothing but a normal
// dial, still downloads the entire torrent from a super-seeding source —
// BEP 16 changes what's advertised, never what's actually served.
func TestSuperSeedingLeecherDownloadsFullTorrent(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "superseed-full.bin", pieceLength, []fileSpec{{length: pieceLength * 6}})

	seederCfg := newTestConfig(t)
	seederCfg.SuperSeeding = true
	seeder := newPreSeededTorrent(t, "superseed-full.bin", mi, content, seederCfg)
	runInBackground(t, seeder)
	waitForState(t, seeder, StateSeeding, 5*time.Second)
	pi := listenAndRoute(t, seeder)

	leecher, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New (leecher): %v", err)
	}
	runInBackground(t, leecher)
	leecher.DialPeer(pi)

	waitForState(t, leecher, StateSeeding, 30*time.Second)

	got, err := os.ReadFile(filepath.Join(leecher.cfg.DownloadDir, "superseed-full.bin"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded file differs from the source (%d bytes vs %d)", len(got), len(content))
	}
}
