package torrent

import (
	"testing"
	"time"
)

// TestAllowedFastDownloadsAPieceWhileChoked proves BEP 6 end to end at the
// torrent level: a real Torrent connects to a fake seeder that never sends
// Unchoke at all, only AllowedFast for one specific piece — and still ends
// up with exactly that piece downloaded and verified, while the other piece
// (never granted) stays unreachable for as long as the connection remains
// choked.
func TestAllowedFastDownloadsAPieceWhileChoked(t *testing.T) {
	const pieceLength = 16384
	mi, content := buildTorrent(t, "allowedfast.bin", pieceLength, []fileSpec{{length: pieceLength * 2}})
	seeder := newFakeSeeder(t, mi, content)
	seeder.stayChoked = true
	seeder.allowedFastPieces = []int{0}

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && tr.Stats().HavePieces < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := tr.Stats().HavePieces; got != 1 {
		t.Fatalf("HavePieces = %d after granting piece 0 via AllowedFast, want exactly 1", got)
	}

	// Piece 1 was never granted and the connection never unchoked, so it
	// must still be missing — give it a real window to (wrongly) arrive
	// before asserting it didn't.
	time.Sleep(300 * time.Millisecond)
	if got := tr.Stats().HavePieces; got != 1 {
		t.Fatalf("HavePieces = %d, want it to stay at 1 (piece 1 was never allowed-fast and the peer never unchoked)", got)
	}
}
