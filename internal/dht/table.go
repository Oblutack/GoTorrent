package dht

import (
	"net"
	"sort"
	"sync"
	"time"
)

const (
	// bucketSize is Kademlia's k: the reference DHT and every implementation
	// interoperating with it uses 8.
	bucketSize = 8

	// numBuckets is one per possible common-prefix-length with our own ID
	// (0..159), plus the identical-ID case folded into the last bucket. This
	// is the "bucket per distance" layout: instead of a tree that starts as
	// one bucket covering the whole ID space and splits on demand, a table
	// here is simply 160 fixed buckets indexed by commonPrefixLen(self, id).
	// It is a standard, simpler-to-implement-correctly variant of the same
	// idea — dynamic splitting only ever matters for buckets on the path to
	// your own ID, and indexing by CPL gives exactly one (potentially
	// undersized) bucket per such prefix without any tree bookkeeping. The
	// memory cost (160 buckets × 8 contacts, mostly empty) is trivial.
	numBuckets = 160

	// questionableAfter is how long a contact can go unheard-from before
	// it stops counting as "good" for eviction purposes.
	questionableAfter = 15 * time.Minute

	// badAfterFailures is how many consecutive queries a contact can fail
	// before a full bucket is willing to evict it for a new contact.
	badAfterFailures = 3
)

// contact is one routing-table entry: a node we know about, not necessarily
// one we are actively talking to right now.
type contact struct {
	id            NodeID
	addr          *net.UDPAddr
	lastSeen      time.Time
	failedQueries int
}

func (c *contact) good() bool {
	return c.failedQueries == 0 && time.Since(c.lastSeen) < questionableAfter
}

func (c *contact) bad() bool {
	return c.failedQueries >= badAfterFailures
}

// bucket holds up to bucketSize contacts, ordered least-recently-seen first
// (index 0) to most-recently-seen last — the classic Kademlia ordering,
// which is what makes "whichever contact we have heard from least recently
// is the best candidate to evict" a reasonable heuristic.
type bucket struct {
	contacts []*contact
}

func (b *bucket) find(id NodeID) *contact {
	for _, c := range b.contacts {
		if c.id == id {
			return c
		}
	}
	return nil
}

// insert adds or refreshes a contact. If the bucket is full, a bad contact
// (never a merely questionable one) is evicted to make room; otherwise a
// full bucket simply does not grow.
//
// This is a deliberate simplification of the Kademlia reference behavior,
// not an oversight: a textbook implementation pings the least-recently-seen
// contact before deciding whether to replace it with a new one, which needs
// a round trip coordinated with the query layer above this table. Table
// stays passive here; node.go's periodic maintenance sweep is what actually
// keeps buckets populated with live contacts, by proactively pinging
// questionable ones so they either refresh (good again) or accumulate
// failures (bad, and evictable) well before a bucket-full moment forces the
// decision.
func (b *bucket) insert(id NodeID, addr *net.UDPAddr) {
	if existing := b.find(id); existing != nil {
		existing.addr = addr
		existing.lastSeen = time.Now()
		existing.failedQueries = 0
		b.moveToTail(existing)
		return
	}
	c := &contact{id: id, addr: addr, lastSeen: time.Now()}
	if len(b.contacts) < bucketSize {
		b.contacts = append(b.contacts, c)
		return
	}
	for i, existing := range b.contacts {
		if existing.bad() {
			b.contacts[i] = c
			return
		}
	}
	// Full of good/questionable contacts: the new one is dropped.
}

func (b *bucket) moveToTail(c *contact) {
	for i, existing := range b.contacts {
		if existing == c {
			b.contacts = append(b.contacts[:i], b.contacts[i+1:]...)
			b.contacts = append(b.contacts, c)
			return
		}
	}
}

// Table is a Kademlia routing table for one local node.
type Table struct {
	mu      sync.Mutex
	self    NodeID
	buckets [numBuckets]bucket
}

func newTable(self NodeID) *Table {
	return &Table{self: self}
}

func (t *Table) bucketIndex(id NodeID) int {
	idx := commonPrefixLen(t.self, id)
	if idx >= numBuckets {
		idx = numBuckets - 1
	}
	return idx
}

// Insert adds or refreshes a contact. A node inserting itself is a no-op —
// there is no bucket for distance zero to matter.
func (t *Table) Insert(id NodeID, addr *net.UDPAddr) {
	if id == t.self {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buckets[t.bucketIndex(id)].insert(id, addr)
}

// MarkFailed records a failed query against a known contact, moving it
// toward eventual eviction (see bucket.insert). A contact this table has
// never heard of is a no-op.
func (t *Table) MarkFailed(id NodeID) {
	if id == t.self {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if c := t.buckets[t.bucketIndex(id)].find(id); c != nil {
		c.failedQueries++
	}
}

// FindClosest returns up to k contacts ordered by XOR distance to target —
// the core Kademlia primitive every lookup and every find_node/get_peers
// reply is built from.
func (t *Table) FindClosest(target NodeID, k int) []nodeAddr {
	t.mu.Lock()
	defer t.mu.Unlock()

	var all []nodeAddr
	for i := range t.buckets {
		for _, c := range t.buckets[i].contacts {
			all = append(all, nodeAddr{id: c.id, addr: c.addr})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return xor(target, all[i].id).less(xor(target, all[j].id))
	})
	if len(all) > k {
		all = all[:k]
	}
	return all
}

// Questionable returns every contact that is neither good nor yet bad — the
// candidates node.go's maintenance sweep pings to resolve one way or the
// other before a bucket-full moment has to guess.
func (t *Table) Questionable() []nodeAddr {
	t.mu.Lock()
	defer t.mu.Unlock()

	var out []nodeAddr
	for i := range t.buckets {
		for _, c := range t.buckets[i].contacts {
			if !c.good() && !c.bad() {
				out = append(out, nodeAddr{id: c.id, addr: c.addr})
			}
		}
	}
	return out
}

// All returns every contact currently in the table, for persistence.
func (t *Table) All() []nodeAddr {
	t.mu.Lock()
	defer t.mu.Unlock()

	var out []nodeAddr
	for i := range t.buckets {
		for _, c := range t.buckets[i].contacts {
			out = append(out, nodeAddr{id: c.id, addr: c.addr})
		}
	}
	return out
}

// Count returns how many contacts the table currently holds.
func (t *Table) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for i := range t.buckets {
		n += len(t.buckets[i].contacts)
	}
	return n
}
