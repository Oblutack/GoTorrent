package bencode

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestUnmarshalInto(t *testing.T) {
	type inner struct {
		N int64  `bencode:"n"`
		S string `bencode:"s"`
	}
	type outer struct {
		Announce string     `bencode:"announce"`
		Count    int        `bencode:"count"`
		Ratio    uint16     `bencode:"ratio"`
		Private  bool       `bencode:"private"`
		Pieces   []byte     `bencode:"pieces"`
		Hash     [4]byte    `bencode:"hash"`
		Tiers    [][]string `bencode:"tiers"`
		Nested   inner      `bencode:"nested"`
		Ptr      *inner     `bencode:"ptr"`
		Extras   map[string]int64
		Untagged string
		Skipped  string `bencode:"-"`
	}

	data := []byte("d" +
		"8:announce19:http://tracker/annc" +
		"5:counti42e" +
		"6:extrasd1:ai1e1:bi2ee" +
		"4:hash4:abcd" +
		"6:nestedd1:ni7e1:s3:foxe" +
		"6:pieces6:\x00\x01\x02\x03\x04\x05" +
		"7:privatei1e" +
		"3:ptrd1:ni9e1:s3:baze" +
		"5:ratioi65535e" +
		"7:skipped4:nope" +
		"5:tiersll4:aaaael4:bbbb4:ccccee" +
		"8:untagged3:yes" +
		"7:unknownd4:deep4:teste" +
		"e")

	var got outer
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	want := outer{
		Announce: "http://tracker/annc",
		Count:    42,
		Ratio:    65535,
		Private:  true,
		Pieces:   []byte{0, 1, 2, 3, 4, 5},
		Hash:     [4]byte{'a', 'b', 'c', 'd'},
		Tiers:    [][]string{{"aaaa"}, {"bbbb", "cccc"}},
		Nested:   inner{N: 7, S: "fox"},
		Ptr:      &inner{N: 9, S: "baz"},
		Extras:   map[string]int64{"a": 1, "b": 2},
		Untagged: "yes",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Unmarshal mismatch\n got: %+v\nwant: %+v", got, want)
	}
	if got.Skipped != "" {
		t.Errorf(`field tagged "-" was populated with %q`, got.Skipped)
	}
}

func TestUnmarshalIntoAny(t *testing.T) {
	var v any
	if err := Unmarshal([]byte("d3:agei30e4:listli1ei2ee4:name5:alicee"), &v); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := map[string]any{
		"age":  int64(30),
		"list": []any{int64(1), int64(2)},
		"name": "alice",
	}
	if !reflect.DeepEqual(v, want) {
		t.Fatalf("got %#v, want %#v", v, want)
	}
}

// TestRawMessageIsVerbatim is the property the whole package is built around:
// an infohash is SHA-1 over the original bytes of the info dictionary, so
// RawMessage must reproduce them exactly, including any ordering or encoding
// quirk the original author used.
func TestRawMessageIsVerbatim(t *testing.T) {
	// Note the keys are NOT in lexicographic order. A decode/re-encode round
	// trip would silently reorder them and change the hash.
	infoDict := "d4:name4:test12:piece lengthi16384e6:pieces0:e"
	data := []byte("d8:announce4:http4:infod4:name4:test12:piece lengthi16384e6:pieces0:ee")

	var parsed struct {
		Announce string     `bencode:"announce"`
		Info     RawMessage `bencode:"info"`
	}
	if err := Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(parsed.Info) != infoDict {
		t.Fatalf("RawMessage = %q, want %q", parsed.Info, infoDict)
	}

	// And it must survive a round trip through Marshal unchanged.
	out, err := Marshal(parsed)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Contains(out, []byte(infoDict)) {
		t.Fatalf("re-marshalled output lost the raw info dict: %q", out)
	}
}

func TestMarshalIsCanonical(t *testing.T) {
	// Struct fields are declared out of order on purpose.
	v := struct {
		Zebra string `bencode:"zebra"`
		Apple string `bencode:"apple"`
		Mango string `bencode:"mango"`
	}{"z", "a", "m"}

	got, err := Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := "d5:apple1:a5:mango1:m5:zebra1:ze"
	if string(got) != want {
		t.Fatalf("Marshal = %q, want %q", got, want)
	}

	// Maps must sort too.
	m := map[string]int{"c": 3, "a": 1, "b": 2}
	got, err = Marshal(m)
	if err != nil {
		t.Fatalf("Marshal map: %v", err)
	}
	if string(got) != "d1:ai1e1:bi2e1:ci3ee" {
		t.Fatalf("Marshal map = %q", got)
	}
}

func TestMarshalOmitEmpty(t *testing.T) {
	type v struct {
		Always  string   `bencode:"always"`
		Text    string   `bencode:"text,omitempty"`
		Num     int64    `bencode:"num,omitempty"`
		List    []string `bencode:"list,omitempty"`
		Present int64    `bencode:"present,omitempty"`
	}
	got, err := Marshal(v{Always: "", Present: 5})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := "d6:always0:7:presenti5ee"
	if string(got) != want {
		t.Fatalf("Marshal = %q, want %q", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	type file struct {
		Length int64    `bencode:"length"`
		Path   []string `bencode:"path"`
	}
	type info struct {
		Files       []file `bencode:"files"`
		Name        string `bencode:"name"`
		PieceLength int64  `bencode:"piece length"`
		Pieces      []byte `bencode:"pieces"`
	}

	original := info{
		Files: []file{
			{Length: 100, Path: []string{"a", "b.txt"}},
			{Length: 200, Path: []string{"c.bin"}},
		},
		Name:        "Torrent Name",
		PieceLength: 16384,
		Pieces:      []byte{0xde, 0xad, 0xbe, 0xef},
	}

	encoded, err := Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded info
	if err := Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round trip changed the value\n got: %+v\nwant: %+v", decoded, original)
	}

	// Encoding the decoded value again must be byte-identical.
	again, err := Marshal(decoded)
	if err != nil {
		t.Fatalf("Marshal again: %v", err)
	}
	if !bytes.Equal(encoded, again) {
		t.Fatalf("re-encoding is not stable:\n%q\n%q", encoded, again)
	}
}

func TestUnmarshalSyntaxErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty input", input: ""},
		{name: "bare e", input: "e"},
		{name: "unterminated integer", input: "i42"},
		{name: "empty integer", input: "ie"},
		{name: "negative zero", input: "i-0e"},
		{name: "leading zero", input: "i03e"},
		{name: "lone minus", input: "i-e"},
		{name: "non-digit integer", input: "i4x2e"},
		{name: "string with no colon", input: "4abcd"},
		{name: "string length leading zero", input: "04:abcd"},
		{name: "string overruns input", input: "500:abc"},
		{name: "unterminated list", input: "li1e"},
		{name: "unterminated dict", input: "d3:abci1e"},
		{name: "dict key is not a string", input: "di1ei2ee"},
		{name: "dict with a dangling key", input: "d3:abce"},
		{name: "unknown token", input: "x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v any
			if err := Unmarshal([]byte(tt.input), &v); err == nil {
				t.Fatalf("Unmarshal(%q) returned no error, got %#v", tt.input, v)
			}
		})
	}
}

// TestUnmarshalRejectsDeepNesting covers the recursion bound. The decoder walks
// containers on the goroutine stack, so unbounded input is a crash, not an
// error.
func TestUnmarshalRejectsDeepNesting(t *testing.T) {
	for _, depth := range []int{maxDepth + 2, 100_000} {
		input := strings.Repeat("l", depth) + strings.Repeat("e", depth)
		var v any
		err := Unmarshal([]byte(input), &v)
		if err == nil {
			t.Fatalf("Unmarshal of %d nested lists returned no error", depth)
		}
		if !errors.Is(err, ErrTooDeep) {
			t.Fatalf("Unmarshal of %d nested lists = %v, want ErrTooDeep", depth, err)
		}
	}

	// Within the limit it must still work.
	depth := maxDepth - 1
	input := strings.Repeat("l", depth) + strings.Repeat("e", depth)
	var v any
	if err := Unmarshal([]byte(input), &v); err != nil {
		t.Fatalf("Unmarshal of %d nested lists = %v, want nil", depth, err)
	}
}

// TestUnmarshalRejectsHugeStringLength makes sure a claimed length is bounded
// by what is actually present before anything is allocated.
func TestUnmarshalRejectsHugeStringLength(t *testing.T) {
	var v any
	err := Unmarshal([]byte("2147483647:abc"), &v)
	if err == nil {
		t.Fatal("a string claiming 2 GB on a 14-byte input was accepted")
	}
	if !errors.Is(err, ErrSyntax) {
		t.Fatalf("got %v, want a syntax error", err)
	}
}

func TestUnmarshalTypeErrors(t *testing.T) {
	var target struct {
		N int64 `bencode:"n"`
	}
	err := Unmarshal([]byte("d1:n3:abce"), &target)
	var typeErr *UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("got %v, want an UnmarshalTypeError", err)
	}
	if typeErr.Field != "n" {
		t.Errorf("error names field %q, want \"n\"", typeErr.Field)
	}
}

func TestUnmarshalInvalidArgument(t *testing.T) {
	var notAPointer int
	if err := Unmarshal([]byte("i1e"), notAPointer); err == nil {
		t.Fatal("Unmarshal into a non-pointer returned no error")
	}
	var nilPointer *int
	if err := Unmarshal([]byte("i1e"), nilPointer); err == nil {
		t.Fatal("Unmarshal into a nil pointer returned no error")
	}
}

// TestUnmarshalAcceptsUnsortedKeys is a compatibility guarantee. The previous
// decoder rejected dictionaries whose keys were not in lexicographic order,
// because the infohash depended on re-encoding them. RawMessage removed that
// dependency, so there is no longer a reason to refuse these torrents.
func TestUnmarshalAcceptsUnsortedKeys(t *testing.T) {
	var v map[string]any
	if err := Unmarshal([]byte("d1:zi1e1:ai2ee"), &v); err != nil {
		t.Fatalf("Unmarshal with unsorted keys: %v", err)
	}
	if v["z"] != int64(1) || v["a"] != int64(2) {
		t.Fatalf("got %#v", v)
	}
}

func TestStringsCarryBinary(t *testing.T) {
	// 20 bytes of a SHA-1 hash, including a NUL and bytes above 0x7f.
	raw := []byte{0x00, 0xff, 0x80, 0x0a, 0x0d, 0x1b, 'a', 'b', 0x7f, 0x01,
		0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0xfe, 0xfd}

	encoded, err := Marshal(raw)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded []byte
	if err := Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !bytes.Equal(raw, decoded) {
		t.Fatalf("binary string did not survive: %x vs %x", decoded, raw)
	}
}

func TestDecodedBytesDoNotAliasInput(t *testing.T) {
	data := []byte("3:abc")
	var got []byte
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	data[2] = 'X' // scribble on the input
	if string(got) != "abc" {
		t.Fatalf("decoded value aliases the input buffer: got %q", got)
	}
}

func BenchmarkUnmarshalTorrentLike(b *testing.B) {
	type file struct {
		Length int64    `bencode:"length"`
		Path   []string `bencode:"path"`
	}
	type info struct {
		Files       []file `bencode:"files"`
		Name        string `bencode:"name"`
		PieceLength int64  `bencode:"piece length"`
		Pieces      []byte `bencode:"pieces"`
	}
	src := info{Name: "bench", PieceLength: 262144, Pieces: bytes.Repeat([]byte{1}, 20*2000)}
	for i := 0; i < 200; i++ {
		src.Files = append(src.Files, file{Length: int64(i) * 1000, Path: []string{"dir", "file"}})
	}
	data, err := Marshal(src)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		var out info
		if err := Unmarshal(data, &out); err != nil {
			b.Fatal(err)
		}
	}
}
