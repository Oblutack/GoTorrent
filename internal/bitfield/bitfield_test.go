package bitfield

import (
	"errors"
	"reflect"
	"testing"
)

func TestSetHasClear(t *testing.T) {
	b := New(20)
	if b.Len() != 20 || b.Count() != 0 || !b.Empty() {
		t.Fatalf("New(20) = len %d count %d", b.Len(), b.Count())
	}

	for _, i := range []int{0, 7, 8, 19} {
		b.Set(i)
	}
	if b.Count() != 4 {
		t.Fatalf("Count() = %d, want 4", b.Count())
	}
	for _, i := range []int{0, 7, 8, 19} {
		if !b.Has(i) {
			t.Errorf("Has(%d) = false after Set", i)
		}
	}
	for _, i := range []int{1, 6, 9, 18} {
		if b.Has(i) {
			t.Errorf("Has(%d) = true, was never set", i)
		}
	}

	// Setting twice must not double-count.
	b.Set(0)
	if b.Count() != 4 {
		t.Fatalf("Count() = %d after a repeated Set, want 4", b.Count())
	}

	b.Clear(0)
	if b.Has(0) || b.Count() != 3 {
		t.Fatalf("after Clear(0): Has=%v Count=%d", b.Has(0), b.Count())
	}
	// Clearing twice must not under-count.
	b.Clear(0)
	if b.Count() != 3 {
		t.Fatalf("Count() = %d after a repeated Clear, want 3", b.Count())
	}
}

// TestOutOfRangeIsSafe matters because indexes arrive straight off the wire.
func TestOutOfRangeIsSafe(t *testing.T) {
	b := New(10)
	for _, i := range []int{-1, 10, 11, 1 << 30} {
		if b.Has(i) {
			t.Errorf("Has(%d) = true", i)
		}
		b.Set(i)
		b.Clear(i)
	}
	if b.Count() != 0 {
		t.Fatalf("out-of-range writes changed Count to %d", b.Count())
	}
}

func TestBitOrderMatchesTheWire(t *testing.T) {
	// BEP 3: the high bit of the first byte is piece 0.
	b := New(8)
	b.Set(0)
	if b.Bytes()[0] != 0x80 {
		t.Fatalf("piece 0 set gives byte %#x, want 0x80", b.Bytes()[0])
	}
	b.Set(7)
	if b.Bytes()[0] != 0x81 {
		t.Fatalf("pieces 0 and 7 set give %#x, want 0x81", b.Bytes()[0])
	}
}

func TestByteLen(t *testing.T) {
	for _, tt := range []struct{ n, want int }{
		{0, 0}, {1, 1}, {7, 1}, {8, 1}, {9, 2}, {16, 2}, {17, 3},
	} {
		if got := ByteLen(tt.n); got != tt.want {
			t.Errorf("ByteLen(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

func TestFromBytes(t *testing.T) {
	// 12 pieces needs 2 bytes, with the low 4 bits of byte 1 unused.
	b, err := FromBytes([]byte{0xFF, 0xF0}, 12)
	if err != nil {
		t.Fatalf("FromBytes: %v", err)
	}
	if !b.Complete() || b.Count() != 12 {
		t.Fatalf("Count = %d, Complete = %v", b.Count(), b.Complete())
	}

	// Wrong width.
	if _, err := FromBytes([]byte{0xFF}, 12); err == nil {
		t.Error("FromBytes accepted a bitfield of the wrong width")
	}
	if _, err := FromBytes([]byte{0xFF, 0xF0, 0x00}, 12); err == nil {
		t.Error("FromBytes accepted an over-wide bitfield")
	}

	// Spare bits set: a peer claiming pieces that do not exist.
	if _, err := FromBytes([]byte{0xFF, 0xF8}, 12); !errors.Is(err, ErrSpareBitsSet) {
		t.Errorf("FromBytes with spare bits = %v, want ErrSpareBitsSet", err)
	}

	// An exact multiple of 8 has no spare bits to check.
	if _, err := FromBytes([]byte{0xFF, 0xFF}, 16); err != nil {
		t.Errorf("FromBytes on a byte-aligned bitfield: %v", err)
	}
}

func TestFromBytesCopies(t *testing.T) {
	raw := []byte{0x80, 0x00}
	b, err := FromBytes(raw, 16)
	if err != nil {
		t.Fatalf("FromBytes: %v", err)
	}
	raw[0] = 0xFF
	if b.Count() != 1 {
		t.Fatal("FromBytes aliased the caller's slice")
	}
}

func TestFull(t *testing.T) {
	b := Full(13)
	if !b.Complete() || b.Count() != 13 {
		t.Fatalf("Full(13): count %d complete %v", b.Count(), b.Complete())
	}
	// Spare bits must stay clear so the result is a legal wire message.
	if _, err := FromBytes(b.Bytes(), 13); err != nil {
		t.Fatalf("Full produced an invalid bitfield: %v", err)
	}
}

func TestCopyFrom(t *testing.T) {
	b := New(12)
	if err := b.CopyFrom([]byte{0xA0, 0x00}); err != nil {
		t.Fatalf("CopyFrom: %v", err)
	}
	if !b.Has(0) || !b.Has(2) || b.Count() != 2 {
		t.Fatalf("after CopyFrom: count %d", b.Count())
	}
	if err := b.CopyFrom([]byte{0x00}); err == nil {
		t.Error("CopyFrom accepted the wrong width")
	}
	// A rejected CopyFrom must leave the bitfield untouched.
	if b.Count() != 2 {
		t.Fatalf("a failed CopyFrom mutated the bitfield: count %d", b.Count())
	}
}

func TestClone(t *testing.T) {
	b := New(10)
	b.Set(3)
	c := b.Clone()
	c.Set(4)
	if b.Has(4) {
		t.Fatal("Clone shares storage with the original")
	}
	if !c.Has(3) || c.Count() != 2 || b.Count() != 1 {
		t.Fatalf("Clone: b=%d c=%d", b.Count(), c.Count())
	}
}

func TestEach(t *testing.T) {
	b := New(20)
	want := []int{0, 5, 8, 15, 19}
	for _, i := range want {
		b.Set(i)
	}

	var got []int
	b.Each(func(i int) bool {
		got = append(got, i)
		return true
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Each visited %v, want %v", got, want)
	}

	// Returning false stops early.
	got = nil
	b.Each(func(i int) bool {
		got = append(got, i)
		return len(got) < 2
	})
	if !reflect.DeepEqual(got, []int{0, 5}) {
		t.Fatalf("Each did not stop early: %v", got)
	}

	// An empty bitfield visits nothing.
	calls := 0
	New(10).Each(func(int) bool { calls++; return true })
	if calls != 0 {
		t.Fatalf("Each on an empty bitfield made %d calls", calls)
	}
}

func TestEachStopsAtLen(t *testing.T) {
	// 5 pieces in one byte: the last 3 bits are padding and must never be
	// visited even if something set them.
	b := New(5)
	for i := 0; i < 5; i++ {
		b.Set(i)
	}
	b.bits[0] = 0xFF // corrupt the padding directly

	var got []int
	b.Each(func(i int) bool {
		got = append(got, i)
		return true
	})
	if !reflect.DeepEqual(got, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("Each visited %v, want 0..4", got)
	}
}

func TestZeroPieces(t *testing.T) {
	b := New(0)
	if b.Len() != 0 || b.Complete() || len(b.Bytes()) != 0 {
		t.Fatalf("New(0): len %d complete %v bytes %d", b.Len(), b.Complete(), len(b.Bytes()))
	}
}
