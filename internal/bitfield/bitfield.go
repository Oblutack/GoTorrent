// Package bitfield implements the piece bitmap used by the BitTorrent wire
// protocol, by resume data, and by the piece picker.
//
// Bits are numbered from the most significant bit of the first byte, which is
// what BEP 3 specifies and what goes on the wire unchanged.
package bitfield

import (
	"errors"
	"fmt"
	"math/bits"
)

// ErrSpareBitsSet reports a bitfield whose padding bits are not zero. BEP 3
// requires them to be, and a peer that gets it wrong is either broken or
// trying to claim pieces that do not exist.
var ErrSpareBitsSet = errors.New("bitfield: spare bits are set")

// Bitfield is a fixed-size bitmap over a torrent's pieces.
//
// It is not safe for concurrent use; callers that share one must provide their
// own synchronisation.
type Bitfield struct {
	bits  []byte
	n     int // number of pieces, which is usually not a multiple of 8
	count int // cached popcount, so Complete and Count are O(1)
}

// ByteLen returns how many bytes a bitfield of n pieces occupies.
func ByteLen(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + 7) / 8
}

// New returns an empty bitfield for n pieces.
func New(n int) *Bitfield {
	if n < 0 {
		n = 0
	}
	return &Bitfield{bits: make([]byte, ByteLen(n)), n: n}
}

// FromBytes wraps raw wire or resume bytes, validating the width and the
// padding. The slice is copied.
func FromBytes(raw []byte, n int) (*Bitfield, error) {
	want := ByteLen(n)
	if len(raw) != want {
		return nil, fmt.Errorf("bitfield: got %d bytes for %d pieces, want %d", len(raw), n, want)
	}
	b := &Bitfield{bits: append([]byte(nil), raw...), n: n}
	if spare := n % 8; spare != 0 && want > 0 {
		mask := byte(0xFF >> spare)
		if b.bits[want-1]&mask != 0 {
			return nil, ErrSpareBitsSet
		}
	}
	b.recount()
	return b, nil
}

// Full returns a bitfield with every piece set, which is what a seeder has.
func Full(n int) *Bitfield {
	b := New(n)
	for i := 0; i < n; i++ {
		b.Set(i)
	}
	return b
}

func (b *Bitfield) recount() {
	b.count = 0
	for _, x := range b.bits {
		b.count += bits.OnesCount8(x)
	}
}

// Len is the number of pieces the bitfield covers.
func (b *Bitfield) Len() int { return b.n }

// Count is how many pieces are set.
func (b *Bitfield) Count() int { return b.count }

// Complete reports whether every piece is set.
func (b *Bitfield) Complete() bool { return b.n > 0 && b.count == b.n }

// Empty reports whether no piece is set.
func (b *Bitfield) Empty() bool { return b.count == 0 }

// Has reports whether piece i is set. Out-of-range indexes report false rather
// than panicking: the index often comes straight off the wire.
func (b *Bitfield) Has(i int) bool {
	if i < 0 || i >= b.n {
		return false
	}
	return b.bits[i/8]&(1<<(7-uint(i)%8)) != 0
}

// Set marks piece i. Out-of-range indexes are ignored.
func (b *Bitfield) Set(i int) {
	if i < 0 || i >= b.n || b.Has(i) {
		return
	}
	b.bits[i/8] |= 1 << (7 - uint(i)%8)
	b.count++
}

// Clear unmarks piece i.
func (b *Bitfield) Clear(i int) {
	if i < 0 || i >= b.n || !b.Has(i) {
		return
	}
	b.bits[i/8] &^= 1 << (7 - uint(i)%8)
	b.count--
}

// Bytes returns the wire representation. The slice is the bitfield's own
// storage, so callers that hold on to it must copy first.
func (b *Bitfield) Bytes() []byte { return b.bits }

// Clone returns an independent copy.
func (b *Bitfield) Clone() *Bitfield {
	return &Bitfield{
		bits:  append([]byte(nil), b.bits...),
		n:     b.n,
		count: b.count,
	}
}

// CopyFrom replaces the contents with raw, validating it the same way
// FromBytes does. This is the Bitfield wire message.
func (b *Bitfield) CopyFrom(raw []byte) error {
	replacement, err := FromBytes(raw, b.n)
	if err != nil {
		return err
	}
	copy(b.bits, replacement.bits)
	b.count = replacement.count
	return nil
}

// Each calls fn for every set piece, in ascending order. Returning false stops
// the iteration.
func (b *Bitfield) Each(fn func(i int) bool) {
	for byteIndex, x := range b.bits {
		for x != 0 {
			bit := bits.LeadingZeros8(x)
			i := byteIndex*8 + bit
			if i >= b.n {
				return
			}
			if !fn(i) {
				return
			}
			x &^= 1 << (7 - uint(bit))
		}
	}
}
