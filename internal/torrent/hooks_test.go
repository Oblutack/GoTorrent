package torrent

import (
	"sync"
	"testing"
	"time"
)

// TestOnPeerConnectedAndDisconnectedFire drives a real connect/disconnect
// cycle between two real Torrent actors and proves both hooks fire on the
// leecher (the one hooks are installed on) with the peer's address, in the
// right order. The disconnect is triggered by stopping the *seeder*, not
// the leecher under test — Stop on the torrent being observed races its
// own actor loop exiting against processing the final eventPeerGone (the
// same "cancelling ctx can starve the last event" shape sendEvent's own
// doc comment describes), so it cannot reliably prove this hook fires
// while the actor is still alive and running normally, which is the
// actual case 4.2's event stream cares about. Pause was ruled out too:
// doPause's shutdownPeers resets t.peers directly rather than going
// through removePeer at all, by design, so it never fires this hook
// either.
func TestOnPeerConnectedAndDisconnectedFire(t *testing.T) {
	mi, content := buildTorrent(t, "peerhooks.bin", 16384, []fileSpec{{length: 16384 * 2}})
	seeder := newPreSeededTorrent(t, "peerhooks.bin", mi, content, newTestConfig(t))
	runInBackground(t, seeder)
	waitForState(t, seeder, StateSeeding, 5*time.Second)
	pi := listenAndRoute(t, seeder)

	leecher, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var mu sync.Mutex
	var connected, disconnected string
	leecher.OnPeerConnected(func(addr string) {
		mu.Lock()
		connected = addr
		mu.Unlock()
	})
	leecher.OnPeerDisconnected(func(addr string) {
		mu.Lock()
		disconnected = addr
		mu.Unlock()
	})

	runInBackground(t, leecher)
	leecher.DialPeer(pi)

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := connected
		mu.Unlock()
		if got != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("OnPeerConnected never fired")
		}
		time.Sleep(10 * time.Millisecond)
	}

	seeder.Stop() // the leecher's actor stays alive and keeps processing events

	deadline = time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := disconnected
		mu.Unlock()
		if got != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("OnPeerDisconnected never fired")
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if disconnected != connected {
		t.Fatalf("OnPeerDisconnected addr = %q, want it to match OnPeerConnected's %q", disconnected, connected)
	}
}

// TestOnPieceVerifiedFiresOnlyForRealSuccesses drives a real download and
// proves the hook fires exactly once per real piece (never for the tamper
// case an earlier fixture already exercises elsewhere), reporting the
// right index each time.
func TestOnPieceVerifiedFiresForEachRealPiece(t *testing.T) {
	mi, content := buildTorrent(t, "verifyhook.bin", 16384, []fileSpec{{length: 16384 * 3}})
	seeder := newFakeSeeder(t, mi, content)

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var mu sync.Mutex
	seen := make(map[int]int) // index -> fire count
	tr.OnPieceVerified(func(index int) {
		mu.Lock()
		seen[index]++
		mu.Unlock()
	})

	runInBackground(t, tr)
	tr.DialPeer(seeder.peerInfo())
	waitForState(t, tr, StateSeeding, 30*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != mi.NumPieces() {
		t.Fatalf("OnPieceVerified fired for %d distinct pieces, want %d: %v", len(seen), mi.NumPieces(), seen)
	}
	for i := 0; i < mi.NumPieces(); i++ {
		if seen[i] != 1 {
			t.Fatalf("piece %d fired %d times, want exactly 1", i, seen[i])
		}
	}
}
