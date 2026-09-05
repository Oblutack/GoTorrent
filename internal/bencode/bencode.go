// Package bencode implements the bencoding format used by BitTorrent.
//
// The API deliberately mirrors encoding/json: Marshal and Unmarshal work on
// []byte, struct fields are mapped with a `bencode:"name"` tag, and RawMessage
// defers decoding of a sub-value.
//
// RawMessage is the reason this package exists in this shape. A torrent's
// infohash is the SHA-1 of the raw bytes of its info dictionary, so any
// implementation that decodes the dictionary and re-encodes it to hash it is
// betting that its encoder reproduces the original byte for byte. Capturing
// the original bytes instead removes that whole class of bug.
//
// Bencode strings are arbitrary binary data, not text: the "pieces" field is a
// concatenation of SHA-1 hashes and compact peer lists are packed addresses.
// Decoding into a Go string is supported for convenience but []byte is the
// honest type for anything that is not known to be human-readable.
package bencode

import (
	"errors"
	"fmt"
	"reflect"
)

// maxDepth bounds container nesting. The decoder recurses, so without a limit
// an input of "lllll..." exhausts the goroutine stack and takes the process
// down rather than returning an error.
const maxDepth = 64

var (
	// ErrSyntax reports malformed bencode.
	ErrSyntax = errors.New("bencode: malformed input")

	// ErrTooDeep reports nesting past maxDepth.
	ErrTooDeep = errors.New("bencode: nesting too deep")
)

// RawMessage is a bencoded value captured verbatim. Unmarshal stores the exact
// input bytes in it without decoding, and Marshal writes them back unchanged.
type RawMessage []byte

// MarshalBencode returns m as-is.
func (m RawMessage) MarshalBencode() ([]byte, error) {
	if len(m) == 0 {
		return nil, errors.New("bencode: cannot marshal an empty RawMessage")
	}
	return m, nil
}

// UnmarshalBencode stores a copy of data in m.
func (m *RawMessage) UnmarshalBencode(data []byte) error {
	if m == nil {
		return errors.New("bencode: UnmarshalBencode on a nil RawMessage")
	}
	*m = append((*m)[0:0], data...)
	return nil
}

// Marshaler is implemented by types that encode themselves.
type Marshaler interface {
	MarshalBencode() ([]byte, error)
}

// Unmarshaler is implemented by types that decode themselves from the raw
// bytes of a single bencoded value.
type Unmarshaler interface {
	UnmarshalBencode([]byte) error
}

// SyntaxError describes malformed input and where it was found.
type SyntaxError struct {
	Offset int    // byte offset in the input
	Msg    string // what went wrong
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("bencode: %s at byte %d", e.Msg, e.Offset)
}

func (e *SyntaxError) Unwrap() error { return ErrSyntax }

func syntaxErrorf(offset int, format string, args ...any) *SyntaxError {
	return &SyntaxError{Offset: offset, Msg: fmt.Sprintf(format, args...)}
}

// UnmarshalTypeError reports a bencode value that cannot be stored in the
// target Go type.
type UnmarshalTypeError struct {
	Value  string       // description of the bencode value, e.g. "string"
	Type   reflect.Type // the Go type it could not be stored in
	Struct string       // struct type name, when known
	Field  string       // field name, when known
}

func (e *UnmarshalTypeError) Error() string {
	if e.Struct != "" || e.Field != "" {
		return fmt.Sprintf("bencode: cannot unmarshal %s into %s.%s of type %s",
			e.Value, e.Struct, e.Field, e.Type)
	}
	return fmt.Sprintf("bencode: cannot unmarshal %s into a value of type %s", e.Value, e.Type)
}

// InvalidUnmarshalError reports a bad argument to Unmarshal.
type InvalidUnmarshalError struct {
	Type reflect.Type
}

func (e *InvalidUnmarshalError) Error() string {
	if e.Type == nil {
		return "bencode: Unmarshal(nil)"
	}
	if e.Type.Kind() != reflect.Pointer {
		return "bencode: Unmarshal(non-pointer " + e.Type.String() + ")"
	}
	return "bencode: Unmarshal(nil " + e.Type.String() + ")"
}

// UnsupportedTypeError reports a Go type Marshal cannot encode.
type UnsupportedTypeError struct {
	Type reflect.Type
}

func (e *UnsupportedTypeError) Error() string {
	return "bencode: unsupported type: " + e.Type.String()
}
