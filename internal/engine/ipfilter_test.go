package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/ipfilter"
)

func TestStartIPFilterLoadsALocalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blocklist.dat")
	if err := os.WriteFile(path, []byte("1.2.4.0 - 1.2.4.255 , 100 , blocked\n"), 0o644); err != nil {
		t.Fatalf("writing blocklist: %v", err)
	}

	e, err := New(t.TempDir(), Defaults{
		DownloadDir:  t.TempDir(),
		ResumeDir:    t.TempDir(),
		IPFilterPath: path,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	if err := e.StartIPFilter(context.Background()); err != nil {
		t.Fatalf("StartIPFilter: %v", err)
	}
	if got := e.ipFilter.Count(); got != 1 {
		t.Fatalf("ipFilter.Count() = %d, want 1", got)
	}
}

func TestStartIPFilterReportsALoadFailure(t *testing.T) {
	e, err := New(t.TempDir(), Defaults{
		DownloadDir:  t.TempDir(),
		ResumeDir:    t.TempDir(),
		IPFilterPath: filepath.Join(t.TempDir(), "does-not-exist.dat"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	if err := e.StartIPFilter(context.Background()); err == nil {
		t.Fatal("StartIPFilter with a missing file: want an error")
	}
}

func TestStartIPFilterAutoUpdatesFromAURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("1.2.4.0 - 1.2.4.255 , 100 , blocked\n"))
	}))
	defer srv.Close()

	e, err := New(t.TempDir(), Defaults{
		DownloadDir: t.TempDir(),
		ResumeDir:   t.TempDir(),
		IPFilterURL: srv.URL + "/list.dat",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := e.StartIPFilter(ctx); err != nil {
		t.Fatalf("StartIPFilter: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if e.ipFilter.Count() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := e.ipFilter.Count(); got != 1 {
		t.Fatalf("ipFilter.Count() after auto-update = %d, want 1", got)
	}
}

// TestManagedTorrentsShareTheEngineIPFilter proves the fleet-wide filter
// actually reaches a torrent's own Config, not just Engine.ipFilter in
// isolation.
func TestManagedTorrentsShareTheEngineIPFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blocklist.dat")
	if err := os.WriteFile(path, []byte("1.2.4.0 - 1.2.4.255 , 100 , blocked\n"), 0o644); err != nil {
		t.Fatalf("writing blocklist: %v", err)
	}

	e, err := New(t.TempDir(), Defaults{
		DownloadDir:  t.TempDir(),
		ResumeDir:    t.TempDir(),
		IPFilterPath: path,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)
	if err := e.StartIPFilter(context.Background()); err != nil {
		t.Fatalf("StartIPFilter: %v", err)
	}

	cfg := e.torrentConfig(t.TempDir())
	if cfg.IPFilter != e.ipFilter {
		t.Fatal("torrentConfig's IPFilter is not the same instance as Engine.ipFilter")
	}
}

// TestHandleIncomingRejectsABlockedIP mirrors
// TestListenClosesConnectionForUnmanagedInfoHash's pattern, proving the IP
// filter closes an inbound connection before it even reaches the
// handshake read — a blocked peer at 127.0.0.1 can't even claim a known
// infohash to get past it.
func TestHandleIncomingRejectsABlockedIP(t *testing.T) {
	e := newTestEngine(t)
	e.ipFilter.Load([]ipfilter.Range{{Start: net.ParseIP("127.0.0.1"), End: net.ParseIP("127.0.0.1")}})

	port := freeTCPPort(t)
	e.defaults.ListenPort = port
	if err := e.Listen(context.Background()); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dialing the engine's listener: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected the connection to be closed for a blocked IP, got data instead")
	}
}

func TestDetectIPFilterFormat(t *testing.T) {
	cases := map[string]string{
		"http://example.com/list.dat": "dat",
		"http://example.com/list.p2p": "p2p",
		"http://example.com/list.P2P": "p2p",
		"/local/path/blocklist":       "dat",
	}
	for source, want := range cases {
		if got := detectIPFilterFormat(source); got != want {
			t.Errorf("detectIPFilterFormat(%q) = %q, want %q", source, got, want)
		}
	}
}
