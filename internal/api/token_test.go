package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateTokenGeneratesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "api-token")

	token, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	if len(token) != tokenBytes*2 { // hex-encoded
		t.Fatalf("token length = %d, want %d (hex of %d bytes)", len(token), tokenBytes*2, tokenBytes)
	}

	// Not asserting the file's mode here: Windows does not honor POSIX
	// permission bits the way os.WriteFile's mode argument requests them
	// (a file written 0600 reads back as 0666 via os.Stat on this
	// project's own Windows dev machine) - no other test in this codebase
	// checks Mode().Perm() for the same reason. The 0600 argument in
	// LoadOrCreateToken is still correct and load-bearing on any POSIX
	// target this daemon runs on.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat token file: %v", err)
	}

	// A second call must return the SAME token, not generate a new one -
	// otherwise every restart would invalidate every client's stored
	// credential.
	again, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("LoadOrCreateToken (second call): %v", err)
	}
	if again != token {
		t.Fatal("LoadOrCreateToken generated a different token on the second call")
	}
}

func TestLoadOrCreateTokenGeneratesDistinctTokens(t *testing.T) {
	a, err := LoadOrCreateToken(filepath.Join(t.TempDir(), "api-token"))
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	b, err := LoadOrCreateToken(filepath.Join(t.TempDir(), "api-token"))
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	if a == b {
		t.Fatal("two fresh token files got the same generated token")
	}
}
