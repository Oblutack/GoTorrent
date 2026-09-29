package mse

import (
	"bufio"
	"bytes"
	"testing"
)

func TestLooksLikeHandshakeRecognizesARealClassicHandshake(t *testing.T) {
	data := append(append([]byte{19}, []byte("BitTorrent protocol")...), []byte("\x00\x00\x00\x00\x00\x00\x00\x00restofhandshake")...)
	got, err := LooksLikeHandshake(bufio.NewReader(bytes.NewReader(data)))
	if err != nil {
		t.Fatalf("LooksLikeHandshake: %v", err)
	}
	if !got {
		t.Fatal("got false for a real classic handshake prefix")
	}
}

func TestLooksLikeHandshakeRejectsMSEShapedBytes(t *testing.T) {
	// A real MSE Ya is 96 effectively-random bytes; this stands in for one
	// that happens to start with the same pstrlen byte (19) a classic
	// handshake does, to prove the discriminator checks the whole 20-byte
	// prefix rather than just that one byte.
	data := make([]byte, 96)
	data[0] = 19
	for i := 1; i < len(data); i++ {
		data[i] = byte(i * 7)
	}
	got, err := LooksLikeHandshake(bufio.NewReader(bytes.NewReader(data)))
	if err != nil {
		t.Fatalf("LooksLikeHandshake: %v", err)
	}
	if got {
		t.Fatal("got true for MSE-shaped bytes that merely share the first byte")
	}
}

func TestLooksLikeHandshakeOnATooShortRead(t *testing.T) {
	got, err := LooksLikeHandshake(bufio.NewReader(bytes.NewReader([]byte{19, 'B', 'i', 't'})))
	if err != nil {
		t.Fatalf("LooksLikeHandshake: %v", err)
	}
	if got {
		t.Fatal("got true for a stream too short to ever contain a full classic handshake prefix")
	}
}
