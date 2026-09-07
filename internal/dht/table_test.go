package dht

import (
	"net"
	"testing"
	"time"
)

func addrN(n int) *net.UDPAddr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1024 + n}
}

// distinctIDInBucket returns an ID whose common-prefix-length with self is
// fixed (by flipping one bit self does not have set, at a fixed position),
// varied by salt in a trailing byte that commonPrefixLen never reaches
// (it stops at the first differing byte) — so distinct salts produce
// distinct IDs that nonetheless land in exactly the same bucket. Bucket
// membership depends only on the first point of difference from self, not
// on what comes after it, which is what makes this construction work.
func distinctIDInBucket(self NodeID, salt byte) NodeID {
	id := self
	id[12] ^= 0x10
	id[13] = salt
	return id
}

func TestTableInsertAndFindClosest(t *testing.T) {
	self := NodeID{0}
	table := newTable(self)

	var a, b, c NodeID
	a[0] = 0x01
	b[0] = 0x02
	c[0] = 0xff // farthest from self

	table.Insert(a, addrN(1))
	table.Insert(b, addrN(2))
	table.Insert(c, addrN(3))

	if got := table.Count(); got != 3 {
		t.Fatalf("Count() = %d, want 3", got)
	}

	closest := table.FindClosest(self, 2)
	if len(closest) != 2 {
		t.Fatalf("FindClosest returned %d, want 2", len(closest))
	}
	if closest[0].id != a || closest[1].id != b {
		t.Fatalf("got order %s, %s; want a then b (closest to self first)", closest[0].id, closest[1].id)
	}
}

func TestTableInsertIgnoresSelf(t *testing.T) {
	self := NodeID{0xaa}
	table := newTable(self)
	table.Insert(self, addrN(1))
	if got := table.Count(); got != 0 {
		t.Fatalf("Count() = %d after inserting self, want 0", got)
	}
}

func TestTableRefreshesExistingContact(t *testing.T) {
	self := NodeID{0}
	table := newTable(self)
	var id NodeID
	id[0] = 1

	table.Insert(id, addrN(1))
	table.Insert(id, addrN(2)) // same id, new address - should refresh, not duplicate

	if got := table.Count(); got != 1 {
		t.Fatalf("Count() = %d after re-inserting the same id, want 1", got)
	}
	closest := table.FindClosest(id, 1)
	if closest[0].addr.Port != 1024+2 {
		t.Fatalf("refreshed contact still has the old address: %s", closest[0].addr)
	}
}

func TestBucketDropsNewContactWhenFullOfGoodOnes(t *testing.T) {
	self := NodeID{0}
	table := newTable(self)

	for i := 0; i < bucketSize; i++ {
		table.Insert(distinctIDInBucket(self, byte(i+1)), addrN(i))
	}
	if got := table.Count(); got != bucketSize {
		t.Fatalf("Count() = %d, want %d after filling one bucket", got, bucketSize)
	}

	overflow := distinctIDInBucket(self, 0xff)
	table.Insert(overflow, addrN(99))

	if got := table.Count(); got != bucketSize {
		t.Fatalf("Count() = %d after inserting into a full bucket of good contacts, want unchanged %d", got, bucketSize)
	}
}

func TestBucketEvictsBadContactForNewOne(t *testing.T) {
	self := NodeID{0}
	table := newTable(self)

	firstID := distinctIDInBucket(self, 1)
	table.Insert(firstID, addrN(1))
	for i := 0; i < badAfterFailures; i++ {
		table.MarkFailed(firstID)
	}

	for i := 1; i < bucketSize; i++ {
		table.Insert(distinctIDInBucket(self, byte(i+1)), addrN(i))
	}
	if got := table.Count(); got != bucketSize {
		t.Fatalf("Count() = %d, want %d before the evicting insert", got, bucketSize)
	}

	newID := distinctIDInBucket(self, 0xff)
	table.Insert(newID, addrN(99))

	closest := table.FindClosest(newID, 1)
	if len(closest) != 1 || closest[0].id != newID {
		t.Fatal("the new contact did not replace the bad one")
	}
	if got := table.Count(); got != bucketSize {
		t.Fatalf("Count() = %d after an evicting insert, want unchanged %d", got, bucketSize)
	}
}

func TestQuestionableExcludesGoodAndBadContacts(t *testing.T) {
	self := NodeID{0}
	table := newTable(self)

	var good, questionable, bad NodeID
	good[1] = 1
	questionable[1] = 2
	bad[1] = 3

	table.Insert(good, addrN(1))
	table.Insert(questionable, addrN(2))
	table.Insert(bad, addrN(3))
	for i := 0; i < badAfterFailures; i++ {
		table.MarkFailed(bad)
	}
	// Force "questionable" past questionableAfter without a failed query -
	// reach into the bucket directly since there is no clock to fake.
	table.mu.Lock()
	c := table.buckets[table.bucketIndex(questionable)].find(questionable)
	c.lastSeen = time.Now().Add(-2 * questionableAfter)
	table.mu.Unlock()

	q := table.Questionable()
	if len(q) != 1 || q[0].id != questionable {
		t.Fatalf("Questionable() = %+v, want just the questionable contact", q)
	}
}

func TestMarkFailedOnUnknownContactIsANoOp(t *testing.T) {
	table := newTable(NodeID{0})
	var unknown NodeID
	unknown[0] = 1
	table.MarkFailed(unknown) // must not panic
	if got := table.Count(); got != 0 {
		t.Fatalf("Count() = %d, want 0", got)
	}
}
