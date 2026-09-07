package dht

import (
	"fmt"
	"os"
	"path/filepath"
)

// saveState and loadState persist a routing table snapshot between runs, so
// a restarted node can rejoin the network in one round trip per saved
// contact instead of bootstrapping from scratch every time. The encoding is
// deliberately the same compact-node format the wire protocol already uses
// (20-byte id + 4-byte IPv4 + 2-byte port) rather than a new container
// format — it is, byte for byte, exactly what find_node already produces
// and parseCompactNodes already parses.
func saveState(path string, nodes []nodeAddr) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("dht: creating state directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encodeCompactNodes(nodes), 0o644); err != nil {
		return fmt.Errorf("dht: writing state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("dht: committing state: %w", err)
	}
	return nil
}

func loadState(path string) ([]nodeAddr, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseCompactNodes(data), nil
}
