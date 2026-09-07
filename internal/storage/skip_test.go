package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWithSkipFilesRejectsWrongLength(t *testing.T) {
	mi, _ := buildTorrent(t, "multi", 16384, []fileSpec{
		{path: []string{"a.bin"}, length: 16384},
		{path: []string{"b.bin"}, length: 16384},
	})
	dir := t.TempDir()
	_, err := New(dir, mi, WithSkipFiles([]bool{true}))
	if err == nil {
		t.Fatal("New accepted a skip slice shorter than the file count, want an error")
	}
}

func TestWithSkipFilesNeverAllocatesTheSkippedFile(t *testing.T) {
	mi, _ := buildTorrent(t, "multi", 16384, []fileSpec{
		{path: []string{"a.bin"}, length: 16384},
		{path: []string{"b.bin"}, length: 16384},
		{path: []string{"c.bin"}, length: 16384},
	})
	dir := t.TempDir()
	s, err := New(dir, mi, WithSkipFiles([]bool{false, true, false}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	root := filepath.Join(dir, "multi")
	if _, err := os.Stat(filepath.Join(root, "a.bin")); err != nil {
		t.Fatalf("a.bin (not skipped) was not allocated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "c.bin")); err != nil {
		t.Fatalf("c.bin (not skipped) was not allocated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "b.bin")); !os.IsNotExist(err) {
		t.Fatalf("b.bin (skipped) exists on disk (stat err = %v), want it never created", err)
	}
}

func TestWriteAtFailsForASkippedFileUntilEnsureFileAllocated(t *testing.T) {
	mi, _ := buildTorrent(t, "multi", 16384, []fileSpec{
		{path: []string{"a.bin"}, length: 16384},
		{path: []string{"b.bin"}, length: 16384},
	})
	dir := t.TempDir()
	s, err := New(dir, mi, WithSkipFiles([]bool{false, true}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	data := bytes.Repeat([]byte{0x42}, 100)
	// File b.bin starts at offset 16384 (after a.bin).
	if _, err := s.WriteAt(data, 16384); err == nil {
		t.Fatal("WriteAt into a skipped, never-allocated file succeeded, want an error")
	}

	if err := s.EnsureFileAllocated(context.Background(), 1); err != nil {
		t.Fatalf("EnsureFileAllocated: %v", err)
	}

	if _, err := s.WriteAt(data, 16384); err != nil {
		t.Fatalf("WriteAt after EnsureFileAllocated: %v", err)
	}
	got := make([]byte, 100)
	if _, err := s.ReadAt(got, 16384); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("read back data does not match what was written")
	}
}

func TestEnsureFileAllocatedIsIdempotentAndCheapForAnUnskippedFile(t *testing.T) {
	mi, _ := buildTorrent(t, "multi", 16384, []fileSpec{
		{path: []string{"a.bin"}, length: 16384},
	})
	dir := t.TempDir()
	s, err := New(dir, mi)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	// The file was never skipped in the first place - EnsureFileAllocated
	// must be a harmless no-op, not an error or a truncation.
	if err := s.EnsureFileAllocated(context.Background(), 0); err != nil {
		t.Fatalf("EnsureFileAllocated on an already-allocated file: %v", err)
	}

	// And calling it twice on a genuinely skipped file must not error either.
	mi2, _ := buildTorrent(t, "multi2", 16384, []fileSpec{
		{path: []string{"a.bin"}, length: 16384},
		{path: []string{"b.bin"}, length: 16384},
	})
	dir2 := t.TempDir()
	s2, err := New(dir2, mi2, WithSkipFiles([]bool{false, true}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	if err := s2.Allocate(context.Background()); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if err := s2.EnsureFileAllocated(context.Background(), 1); err != nil {
		t.Fatalf("first EnsureFileAllocated: %v", err)
	}
	if err := s2.EnsureFileAllocated(context.Background(), 1); err != nil {
		t.Fatalf("second EnsureFileAllocated: %v", err)
	}
}

func TestEnsureFileAllocatedRejectsOutOfRangeIndex(t *testing.T) {
	mi, _ := buildTorrent(t, "single", 16384, []fileSpec{{length: 16384}})
	s, _ := newStorage(t, mi)
	if err := s.EnsureFileAllocated(context.Background(), 5); err == nil {
		t.Fatal("EnsureFileAllocated accepted an out-of-range index, want an error")
	}
}

func TestWantedTotalExcludesSkippedFiles(t *testing.T) {
	mi, _ := buildTorrent(t, "multi", 16384, []fileSpec{
		{path: []string{"a.bin"}, length: 16384},
		{path: []string{"b.bin"}, length: 32768},
		{path: []string{"c.bin"}, length: 16384},
	})
	dir := t.TempDir()
	s, err := New(dir, mi, WithSkipFiles([]bool{false, true, false}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if got, want := s.wantedTotal(), int64(16384+16384); got != want {
		t.Fatalf("wantedTotal() = %d, want %d (b.bin excluded)", got, want)
	}
}
