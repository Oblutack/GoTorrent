package mse

import (
	"bytes"
	"errors"
	"testing"
)

func TestReadUntilFindsAMarkerImmediately(t *testing.T) {
	marker := []byte("MARK")
	r := bytes.NewReader(append(append([]byte{}, marker...), []byte("trailing")...))
	if err := readUntil(r, marker, 100); err != nil {
		t.Fatalf("readUntil: %v", err)
	}
	// r must be positioned exactly after the marker — the rest of the
	// stream (the "trailing" bytes) must still be there, untouched.
	rest := make([]byte, len("trailing"))
	if _, err := r.Read(rest); err != nil {
		t.Fatalf("reading rest: %v", err)
	}
	if string(rest) != "trailing" {
		t.Fatalf("rest = %q, want %q (readUntil over- or under-consumed)", rest, "trailing")
	}
}

func TestReadUntilSkipsRandomPaddingBeforeTheMarker(t *testing.T) {
	marker := []byte("MARK")
	padding := []byte("some-random-padding-bytes")
	r := bytes.NewReader(append(append(append([]byte{}, padding...), marker...), []byte("after")...))
	if err := readUntil(r, marker, len(padding)+len(marker)+1); err != nil {
		t.Fatalf("readUntil: %v", err)
	}
	rest := make([]byte, len("after"))
	if _, err := r.Read(rest); err != nil {
		t.Fatalf("reading rest: %v", err)
	}
	if string(rest) != "after" {
		t.Fatalf("rest = %q, want %q", rest, "after")
	}
}

// TestReadUntilHandlesAFalseStartPrefix covers the case a naive
// implementation (e.g. one that resets its whole buffer on any mismatch
// rather than sliding it) could get wrong: padding that shares a prefix
// with the real marker before the real marker actually appears.
func TestReadUntilHandlesAFalseStartPrefix(t *testing.T) {
	marker := []byte("ABCABD")
	// "ABCABC" is a false start sharing "ABC" and then "AB" with the real
	// marker before diverging, immediately followed by the real marker.
	stream := append([]byte("ABCABC"), marker...)
	r := bytes.NewReader(append(stream, []byte("Z")...))
	if err := readUntil(r, marker, len(stream)+len(marker)); err != nil {
		t.Fatalf("readUntil: %v", err)
	}
	rest := make([]byte, 1)
	if _, err := r.Read(rest); err != nil {
		t.Fatalf("reading rest: %v", err)
	}
	if rest[0] != 'Z' {
		t.Fatalf("rest = %q, want %q", rest, "Z")
	}
}

func TestReadUntilFailsWhenMarkerNeverAppearsWithinWindow(t *testing.T) {
	marker := []byte("NEVER-HERE")
	r := bytes.NewReader(bytes.Repeat([]byte("x"), 50))
	err := readUntil(r, marker, 20)
	if !errors.Is(err, ErrSyncFailed) {
		t.Fatalf("err = %v, want ErrSyncFailed", err)
	}
}

func TestReadUntilFailsOnShortStream(t *testing.T) {
	marker := []byte("MARK")
	r := bytes.NewReader([]byte("ab")) // shorter than the marker itself
	if err := readUntil(r, marker, 100); err == nil {
		t.Fatal("readUntil succeeded against a stream shorter than the marker")
	}
}
