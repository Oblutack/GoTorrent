package torrent

import (
	"testing"
	"time"
)

// TestHaveBitfieldReflectsRealProgress drives a real download and checks
// HaveBitfield() goes from empty to fully complete, matching the piece
// count a real download actually verifies against.
func TestHaveBitfieldReflectsRealProgress(t *testing.T) {
	mi, content := buildTorrent(t, "havebits.bin", 16384, []fileSpec{{length: 16384 * 4}})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runInBackground(t, tr)

	if bf := tr.HaveBitfield(); bf == nil || bf.Count() != 0 {
		t.Fatalf("HaveBitfield() before any data = %v, want an empty, non-nil bitfield", bf)
	}

	seeder := newFakeSeeder(t, mi, content)
	tr.DialPeer(seeder.peerInfo())
	waitForState(t, tr, StateSeeding, 30*time.Second)

	bf := tr.HaveBitfield()
	if bf == nil {
		t.Fatal("HaveBitfield() is nil after completion")
	}
	if bf.Len() != mi.NumPieces() {
		t.Fatalf("HaveBitfield().Len() = %d, want %d", bf.Len(), mi.NumPieces())
	}
	if !bf.Complete() {
		t.Fatalf("HaveBitfield().Complete() = false after reaching Seeding, count=%d/%d", bf.Count(), bf.Len())
	}
}
