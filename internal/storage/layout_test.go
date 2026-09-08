package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveMultiFile(t *testing.T) {
	root := t.TempDir()
	l, err := NewLayout(root, "MyTorrent", true, LayoutOriginal)
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	want := filepath.Join(root, "MyTorrent")
	if l.Base() != want {
		t.Fatalf("Base() = %q, want %q", l.Base(), want)
	}

	got, err := l.Resolve([]string{"Season 1", "ep1.mkv"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != filepath.Join(want, "Season 1", "ep1.mkv") {
		t.Fatalf("Resolve returned %q", got)
	}

	if _, err := l.Resolve(nil); err == nil {
		t.Fatal("Resolve(nil) on a multi-file layout returned no error")
	}
}

func TestResolveSingleFile(t *testing.T) {
	root := t.TempDir()
	l, err := NewLayout(root, "ubuntu.iso", false, LayoutOriginal)
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	got, err := l.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve(nil): %v", err)
	}
	if got != filepath.Join(root, "ubuntu.iso") {
		t.Fatalf("Resolve(nil) = %q", got)
	}
}

// TestResolveRejectsEscapes is the last line of defence. metainfo already
// refuses these segments at parse time, but Resolve must never hand back a
// path outside Base even if something upstream is bypassed.
func TestResolveRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	l, err := NewLayout(root, "MyTorrent", true, LayoutOriginal)
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	escapes := [][]string{
		{".."},
		{"..", ".."},
		{"..", "..", "Windows", "System32", "evil.dll"},
		{"a", "..", "..", "..", "etc", "passwd"},
		{"../../../etc/passwd"},
		{`..\..\..\Windows\evil.dll`},
		{"/etc/passwd"},
		{`C:\Windows\System32\evil.dll`},
	}

	for _, parts := range escapes {
		t.Run(strings.Join(parts, "|"), func(t *testing.T) {
			got, err := l.Resolve(parts)
			if err != nil {
				return // rejected outright, which is the point
			}
			// If it resolved, it must at least still be inside Base.
			rel, relErr := filepath.Rel(l.Base(), got)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Fatalf("Resolve(%q) escaped to %q", parts, got)
			}
		})
	}
}

func TestNewLayoutRejectsEmptyName(t *testing.T) {
	if _, err := NewLayout(t.TempDir(), "", false, LayoutOriginal); err == nil {
		t.Fatal("NewLayout accepted an empty torrent name")
	}
}

// TestContentLayoutSubfolderWrapsASingleFile proves LayoutSubfolder wraps
// even a single-file torrent in <name>/, unlike the default.
func TestContentLayoutSubfolderWrapsASingleFile(t *testing.T) {
	root := t.TempDir()
	l, err := NewLayout(root, "ubuntu.iso", false, LayoutSubfolder)
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	got, err := l.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve(nil): %v", err)
	}
	want := filepath.Join(root, "ubuntu.iso", "ubuntu.iso")
	if got != want {
		t.Fatalf("Resolve(nil) = %q, want %q", got, want)
	}
}

// TestContentLayoutNoSubfolderFlattensAMultiFileTorrent proves
// LayoutNoSubfolder drops the wrapping <name>/ directory even for a
// multi-file torrent, landing files directly under the download directory.
func TestContentLayoutNoSubfolderFlattensAMultiFileTorrent(t *testing.T) {
	root := t.TempDir()
	l, err := NewLayout(root, "MyTorrent", true, LayoutNoSubfolder)
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	if l.Base() != root {
		t.Fatalf("Base() = %q, want the download directory %q unwrapped", l.Base(), root)
	}
	got, err := l.Resolve([]string{"data", "part1.bin"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := filepath.Join(root, "data", "part1.bin")
	if got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
}
