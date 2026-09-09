package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// tokenBytes is the size of a generated bearer token before hex-encoding
// (32 bytes = 256 bits, the same order of magnitude every other random
// identifier in this codebase uses - e.g. dht's announce_peer token,
// tracker's generated peer ID).
const tokenBytes = 32

// LoadOrCreateToken returns the bearer token gottrentd's control API
// requires on every request (see RequireBearerToken). A token file already
// at path is read and trusted as-is; otherwise a fresh one is generated
// with crypto/rand and written to path with 0600 permissions - owner-only,
// since anyone who can read this file can fully control the daemon through
// its API. The parent directory is created if it does not exist yet.
func LoadOrCreateToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return string(data), nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("api: reading token file %s: %w", path, err)
	}

	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("api: generating token: %w", err)
	}
	token := hex.EncodeToString(raw)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("api: creating token directory: %w", err)
	}
	// 0600 before write: WriteFile with a mode argument only applies it at
	// create time, but making that explicit here (rather than relying on
	// WriteFile's own default) is what a reader auditing this file for the
	// "who can see the token" property should be able to see at a glance.
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("api: writing token file %s: %w", path, err)
	}
	return token, nil
}
