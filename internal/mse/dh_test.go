package mse

import (
	"bytes"
	"testing"
)

func TestDHRoundTripProducesTheSameSharedSecret(t *testing.T) {
	a, err := generateKeypair()
	if err != nil {
		t.Fatalf("generateKeypair (a): %v", err)
	}
	b, err := generateKeypair()
	if err != nil {
		t.Fatalf("generateKeypair (b): %v", err)
	}

	sA := sharedSecret(a.priv, b.pub)
	sB := sharedSecret(b.priv, a.pub)
	if !bytes.Equal(sA, sB) {
		t.Fatalf("shared secrets differ:\n  a computed %x\n  b computed %x", sA, sB)
	}
	if len(sA) != dhByteLen {
		t.Fatalf("len(S) = %d, want %d", len(sA), dhByteLen)
	}
}

func TestGenerateKeypairProducesDistinctKeys(t *testing.T) {
	a, err := generateKeypair()
	if err != nil {
		t.Fatalf("generateKeypair: %v", err)
	}
	b, err := generateKeypair()
	if err != nil {
		t.Fatalf("generateKeypair: %v", err)
	}
	if a.priv.Cmp(b.priv) == 0 {
		t.Fatal("two independently generated private keys were identical")
	}
}

func TestEncodeFixedPadsToDHByteLen(t *testing.T) {
	// A small value must still encode to the full fixed width, left-padded
	// with zero bytes — the exact detail that would silently break real
	// interop if this package used a variable-length encoding instead.
	small := dhG // value 2
	got := encodeFixed(small)
	if len(got) != dhByteLen {
		t.Fatalf("len = %d, want %d", len(got), dhByteLen)
	}
	for i := 0; i < dhByteLen-1; i++ {
		if got[i] != 0 {
			t.Fatalf("byte %d = %#x, want 0 (leading zero padding)", i, got[i])
		}
	}
	if got[dhByteLen-1] != 2 {
		t.Fatalf("last byte = %#x, want 2", got[dhByteLen-1])
	}
}
