package engine

import (
	"testing"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// TestAddWithOptionsRecordsCategoryAndTags proves AddOptions actually
// reaches the managed torrent and Summary, not just AddOptions itself.
func TestAddWithOptionsRecordsCategoryAndTags(t *testing.T) {
	e := newTestEngine(t)
	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "organized")

	got, err := e.AddWithOptions(path, "", AddOptions{Category: "movies", Tags: []string{"hd", "favorite"}})
	if err != nil {
		t.Fatalf("AddWithOptions: %v", err)
	}
	if got != hash {
		t.Fatalf("AddWithOptions returned %s, want %s", got, hash)
	}

	list := e.List()
	if len(list) != 1 {
		t.Fatalf("List() has %d entries, want 1", len(list))
	}
	s := list[0]
	if s.Category != "movies" {
		t.Fatalf("Category = %q, want %q", s.Category, "movies")
	}
	if len(s.Tags) != 2 || s.Tags[0] != "hd" || s.Tags[1] != "favorite" {
		t.Fatalf("Tags = %v, want [hd favorite]", s.Tags)
	}
}

// TestAddUsesCategoryPathWhenDownloadDirIsEmpty proves
// Defaults.CategoryPaths only kicks in when the caller didn't already ask
// for a specific directory, and is used ahead of Defaults.DownloadDir.
func TestAddUsesCategoryPathWhenDownloadDirIsEmpty(t *testing.T) {
	fallbackDir := t.TempDir()
	moviesDir := t.TempDir()

	e, err := New(t.TempDir(), Defaults{
		DownloadDir:   fallbackDir,
		ResumeDir:     t.TempDir(),
		CategoryPaths: map[string]string{"movies": moviesDir},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "categorized")
	if _, err := e.AddWithOptions(path, "", AddOptions{Category: "movies"}); err != nil {
		t.Fatalf("AddWithOptions: %v", err)
	}

	list := e.List()
	if len(list) != 1 || list[0].InfoHash != hash {
		t.Fatalf("List() = %v, want exactly the one added torrent", list)
	}
	if list[0].DownloadDir != moviesDir {
		t.Fatalf("DownloadDir = %q, want the category's path %q", list[0].DownloadDir, moviesDir)
	}
}

// TestExplicitDownloadDirOverridesCategoryPath proves a caller-specified
// downloadDir still wins over Defaults.CategoryPaths.
func TestExplicitDownloadDirOverridesCategoryPath(t *testing.T) {
	explicitDir := t.TempDir()
	e, err := New(t.TempDir(), Defaults{
		DownloadDir:   t.TempDir(),
		ResumeDir:     t.TempDir(),
		CategoryPaths: map[string]string{"movies": t.TempDir()},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Shutdown)

	torrentDir := t.TempDir()
	path, _ := writeTorrentFile(t, torrentDir, "explicit")
	if _, err := e.AddWithOptions(path, explicitDir, AddOptions{Category: "movies"}); err != nil {
		t.Fatalf("AddWithOptions: %v", err)
	}

	if got := e.List()[0].DownloadDir; got != explicitDir {
		t.Fatalf("DownloadDir = %q, want the explicit override %q", got, explicitDir)
	}
}

// TestSetCategoryAndSetTagsUpdateAndPersist proves the setters change the
// live Summary and survive a manifest reload — category/tags are meant to,
// unlike queue position (see manifestVersion's v3 comment).
func TestSetCategoryAndSetTagsUpdateAndPersist(t *testing.T) {
	stateDir := t.TempDir()
	downloadDir := t.TempDir()
	torrentDir := t.TempDir()
	path, hash := writeTorrentFile(t, torrentDir, "persisted")

	e1, err := New(stateDir, Defaults{DownloadDir: downloadDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := e1.Add(path, ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := e1.SetCategory(hash, "linux-isos"); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}
	if err := e1.SetTags(hash, []string{"verified"}); err != nil {
		t.Fatalf("SetTags: %v", err)
	}
	e1.Shutdown()

	e2, err := New(stateDir, Defaults{DownloadDir: downloadDir, ResumeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New (second engine): %v", err)
	}
	t.Cleanup(e2.Shutdown)
	if err := e2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	list := e2.List()
	if len(list) != 1 {
		t.Fatalf("List() after Load has %d entries, want 1", len(list))
	}
	if list[0].Category != "linux-isos" {
		t.Fatalf("Category after reload = %q, want %q", list[0].Category, "linux-isos")
	}
	if len(list[0].Tags) != 1 || list[0].Tags[0] != "verified" {
		t.Fatalf("Tags after reload = %v, want [verified]", list[0].Tags)
	}
}

func TestSetCategoryRejectsUnknownHash(t *testing.T) {
	e := newTestEngine(t)
	var bogus metainfo.Hash
	if err := e.SetCategory(bogus, "x"); err == nil {
		t.Fatal("SetCategory on an unmanaged hash: want an error")
	}
}
