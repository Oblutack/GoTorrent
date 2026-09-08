package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/version"
)

func TestNewRefusesAnonymousModeWithoutAProxy(t *testing.T) {
	_, err := New(t.TempDir(), Defaults{
		DownloadDir:   t.TempDir(),
		ResumeDir:     t.TempDir(),
		AnonymousMode: true,
	})
	if err == nil {
		t.Fatal("New with AnonymousMode and no proxy: want an error")
	}
}

func TestNewAllowsAnonymousModeWithAProxy(t *testing.T) {
	e, err := New(t.TempDir(), Defaults{
		DownloadDir:   t.TempDir(),
		ResumeDir:     t.TempDir(),
		AnonymousMode: true,
		ProxyType:     "socks5",
		ProxyAddress:  "127.0.0.1:1", // never dialed by this test
	})
	if err != nil {
		t.Fatalf("New with AnonymousMode and a configured proxy: %v", err)
	}
	t.Cleanup(e.Shutdown)
}

func TestTorrentConfigCarriesAnonymousModeAndProxy(t *testing.T) {
	e, err := New(t.TempDir(), Defaults{
		DownloadDir:   t.TempDir(),
		ResumeDir:     t.TempDir(),
		AnonymousMode: true,
		ProxyType:     "socks5",
		ProxyAddress:  "127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	cfg := e.torrentConfig(t.TempDir())
	if !cfg.AnonymousMode {
		t.Fatal("torrentConfig().AnonymousMode = false, want true")
	}
	if cfg.ProxyDialer == nil {
		t.Fatal("torrentConfig().ProxyDialer = nil, want the configured proxy dialer")
	}
}

func TestStartLSDIsANoopUnderAnonymousMode(t *testing.T) {
	e, err := New(t.TempDir(), Defaults{
		DownloadDir:   t.TempDir(),
		ResumeDir:     t.TempDir(),
		AnonymousMode: true,
		ProxyType:     "socks5",
		ProxyAddress:  "127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	if err := e.StartLSD(context.Background(), freeTCPPort(t)); err != nil {
		t.Fatalf("StartLSD under AnonymousMode: %v", err)
	}
	e.mu.Lock()
	node := e.lsdNode
	e.mu.Unlock()
	if node != nil {
		t.Fatal("StartLSD actually started LSD under AnonymousMode")
	}
}

// TestAnonymousPeerIDHasNoFingerprint proves AnonymousMode reaches all the
// way through Add -> torrentConfig -> torrent.New to the real generated
// peer ID, not just that the Config flag itself gets set.
func TestAnonymousPeerIDHasNoFingerprint(t *testing.T) {
	e, err := New(t.TempDir(), Defaults{
		DownloadDir:   t.TempDir(),
		ResumeDir:     t.TempDir(),
		AnonymousMode: true,
		ProxyType:     "socks5",
		ProxyAddress:  "127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "anon")
	if _, err := e.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	tr, ok := e.Get(hash)
	if !ok {
		t.Fatal("Get: torrent missing right after Add")
	}

	id := tr.OurID()
	if strings.HasPrefix(string(id[:]), version.PeerIDPrefix) {
		t.Fatalf("peer ID %q under AnonymousMode still carries the client prefix", id)
	}
}
