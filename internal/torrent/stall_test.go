package torrent

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDownloadSurvivesSilentlyDroppedRequests is the regression test for a
// permanent stall: tick budgets each peer a pipeline of requests and used to
// count them with a bare integer that only went down when a block arrived or
// a Reject did. A peer that simply never answers a request (it chokes us, its
// outbound queue is full, it is rate-limited) therefore leaked one slot per
// dropped request, and once every slot of every peer was leaked tick asked for
// nothing more, forever, while the picker kept the blocks pending and the
// peers sat connected and unchoked.
//
// The seeder here drops three full initial pipelines' worth of requests per
// connection without a word. With the leak, the very first four requests
// consume the whole pipeline (minPipeline) and the download never progresses;
// with in-flight tracking, each dropped request frees its slot when it times
// out and the download completes.
func TestDownloadSurvivesSilentlyDroppedRequests(t *testing.T) {
	old := requestTimeout
	requestTimeout = 300 * time.Millisecond
	t.Cleanup(func() { requestTimeout = old })

	const pieceLength = 16384
	mi, content := buildTorrent(t, "drops.bin", pieceLength, []fileSpec{{length: pieceLength * 40}})

	tr, err := New(mi, newTestConfig(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = runInBackground(t, tr)

	seeder := newFakeSeeder(t, mi, content)
	seeder.dropFirstRequests = 3 * minPipeline
	tr.DialPeer(seeder.peerInfo())

	waitForState(t, tr, StateSeeding, 20*time.Second)

	got, err := os.ReadFile(filepath.Join(tr.cfg.DownloadDir, "drops.bin"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("downloaded file differs from the source")
	}
}

func TestPeerConnInflightAccounting(t *testing.T) {
	var pc peerConn
	now := time.Now()
	a, b := blockRef{index: 1, begin: 0}, blockRef{index: 1, begin: 16384}

	if pc.outstanding() != 0 {
		t.Fatalf("fresh peerConn has %d outstanding, want 0", pc.outstanding())
	}
	pc.noteRequested(a, now)
	pc.noteRequested(b, now.Add(time.Second))
	if pc.outstanding() != 2 {
		t.Fatalf("outstanding = %d after two requests, want 2", pc.outstanding())
	}

	pc.noteAnswered(a)
	pc.noteAnswered(a) // a repeat, or a block we never asked this peer for, must be harmless
	if pc.outstanding() != 1 {
		t.Fatalf("outstanding = %d after answering a, want 1", pc.outstanding())
	}

	pc.expireInflight(now.Add(2*time.Second), 5*time.Second)
	if pc.outstanding() != 1 {
		t.Fatalf("outstanding = %d before b's timeout, want 1", pc.outstanding())
	}
	pc.expireInflight(now.Add(10*time.Second), 5*time.Second)
	if pc.outstanding() != 0 {
		t.Fatalf("outstanding = %d after b's timeout, want 0", pc.outstanding())
	}
}
