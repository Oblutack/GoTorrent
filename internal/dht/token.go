package dht

import (
	"crypto/rand"
	"crypto/sha1"
	"net"
	"sync"
	"time"
)

// tokenRotateEvery is how often the secret used to derive tokens changes.
// A token remains valid across one rotation (checked against both the
// current and the previous secret), so an issued token is good for roughly
// tokenRotateEvery to 2*tokenRotateEvery — long enough that a get_peers
// followed shortly after by announce_peer never fails, short enough that a
// token cannot be replayed indefinitely.
const tokenRotateEvery = 5 * time.Minute

// tokenIssuer implements BEP 5's announce_peer anti-spoof token: an opaque
// value handed out on get_peers and required back on announce_peer, derived
// from the requester's IP (so it cannot be replayed from a different
// address) using a secret that rotates periodically (so it eventually
// expires without this node needing to remember who it gave a token to).
type tokenIssuer struct {
	mu      sync.Mutex
	current []byte
	prev    []byte
	rotated time.Time
}

func newTokenIssuer() *tokenIssuer {
	return &tokenIssuer{
		current: randomSecret(),
		prev:    randomSecret(),
		rotated: time.Now(),
	}
}

func randomSecret() []byte {
	b := make([]byte, 20)
	_, _ = rand.Read(b) // crypto/rand.Read into a fixed buffer does not fail in practice
	return b
}

func (ti *tokenIssuer) maybeRotate() {
	if time.Since(ti.rotated) < tokenRotateEvery {
		return
	}
	ti.prev = ti.current
	ti.current = randomSecret()
	ti.rotated = time.Now()
}

func (ti *tokenIssuer) issue(addr *net.UDPAddr) string {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	ti.maybeRotate()
	return string(hashToken(ti.current, addr))
}

func (ti *tokenIssuer) valid(addr *net.UDPAddr, token string) bool {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	ti.maybeRotate()
	return token == string(hashToken(ti.current, addr)) || token == string(hashToken(ti.prev, addr))
}

func hashToken(secret []byte, addr *net.UDPAddr) []byte {
	h := sha1.New()
	h.Write(secret)
	h.Write(addr.IP)
	return h.Sum(nil)
}
