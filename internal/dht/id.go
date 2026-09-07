// Package dht implements the BitTorrent mainline DHT (BEP 5): a Kademlia
// distributed hash table used to find peers for a torrent without any
// tracker at all, keyed by infohash instead of the usual Kademlia "value"
// concept.
//
// A DHT node's own identity and a torrent's infohash live in the same
// 160-bit ID space and are compared the same way, so NodeID serves both.
package dht

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
)

// NodeID is a 160-bit Kademlia identifier.
type NodeID [20]byte

func (id NodeID) String() string { return hex.EncodeToString(id[:]) }

// RandomNodeID returns a cryptographically random identifier, suitable for
// this node's own ID or as a lookup target.
func RandomNodeID() (NodeID, error) {
	var id NodeID
	if _, err := rand.Read(id[:]); err != nil {
		return id, fmt.Errorf("dht: generating a random node id: %w", err)
	}
	return id, nil
}

// xor returns the Kademlia XOR distance between two IDs.
func xor(a, b NodeID) NodeID {
	var out NodeID
	for i := range out {
		out[i] = a[i] ^ b[i]
	}
	return out
}

// less compares two distances as big-endian 160-bit integers, which is
// exactly what ordering by XOR distance needs since both operands are
// already big-endian byte arrays: comparing byte by byte from index 0 is
// the same as comparing the integers they represent.
func (a NodeID) less(b NodeID) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// commonPrefixLen returns how many leading bits a and b share. This is the
// bucket index a node with ID b belongs in within a routing table whose
// owner has ID a — see table.go's package comment for why a table indexed
// this way needs no dynamic splitting.
func commonPrefixLen(a, b NodeID) int {
	for i := range a {
		x := a[i] ^ b[i]
		if x == 0 {
			continue
		}
		for bit := 7; bit >= 0; bit-- {
			if x&(1<<uint(bit)) != 0 {
				return i*8 + (7 - bit)
			}
		}
	}
	return len(a) * 8 // identical IDs
}

// nodeAddr pairs a NodeID with the network address to reach it at — the unit
// exchanged in compact node lists (find_node/get_peers replies) and returned
// by routing-table queries.
type nodeAddr struct {
	id   NodeID
	addr *net.UDPAddr
}
