package gobencode

import (
	"bufio" // For potentially buffered writing
	"fmt"
	"io"
	"reflect" // To inspect the type of the data
	"sort"    // For sorting dictionary keys
	"strconv"
)

// Encode writes the Bencode encoding of data to the writer w.
// Supported Go types for data:
// int, int8, int16, int32, int64
// uint, uint8, uint16, uint32, uint64 (treated as int64, check for overflow if necessary or restrict)
// string
// []interface{} (slice of interface{})
// map[string]interface{}
//
// For slices and maps, their elements must also be of supported types.
func Encode(w io.Writer, data interface{}) error {
	// It's often good to use a buffered writer for efficiency,
	// especially if the underlying writer is not buffered (e.g., a network connection).
	// However, for simplicity in this first pass, we can write directly to w.
	// If w is already a *bufio.Writer, that's even better.
	// Let's assume w might not be buffered.
	bw, ok := w.(*bufio.Writer)
	if !ok {
		bw = bufio.NewWriter(w)
		// We must ensure the buffer is flushed at the end if we created it.
		defer bw.Flush()
	}

	return encodeRecursive(bw, data)
}

// encodeRecursive is the internal recursive encoding function.
func encodeRecursive(w *bufio.Writer, data interface{}) error {
	if data == nil {
		// How to encode nil? Bencode doesn't have a nil type.
		// Often, nil slices/maps are encoded as empty lists/dictionaries.
		// A nil string could be an empty string. A nil int is problematic.
		// For now, let's return an error or encode as an empty string (common for optional fields).
		// Or, we can decide based on context (e.g. omitempty in struct tags).
		// Let's try to encode as an empty string, as some bencode implementations might expect something.
		// Or better yet, let the caller handle nils if they mean "omit".
		// Let's return an error for a direct nil.
		return fmt.Errorf("gobencode: cannot encode nil value directly")
	}

	switch v := data.(type) {
	case string:
		return encodeString(w, v)
	case int:
		return encodeInteger(w, int64(v))
	case int8:
		return encodeInteger(w, int64(v))
	case int16:
		return encodeInteger(w, int64(v))
	case int32:
		return encodeInteger(w, int64(v))
	case int64:
		return encodeInteger(w, v)
	// For unsigned integers, Bencode spec implies integers.
	// We'll cast them to int64. Be mindful of potential overflow if uint64 > MaxInt64.
	// For .torrent files, this is usually not an issue as numbers are within int64 range.
	case uint:
		return encodeInteger(w, int64(v))
	case uint8:
		return encodeInteger(w, int64(v))
	case uint16:
		return encodeInteger(w, int64(v))
	case uint32:
		return encodeInteger(w, int64(v))
	case uint64:
        // Handle potential overflow for uint64 -> int64
        if v > uint64(9223372036854775807) { // MaxInt64
            return fmt.Errorf("gobencode: uint64 value %d overflows int64", v)
        }
		return encodeInteger(w, int64(v))
	case []interface{}:
		return encodeList(w, v)
	case map[string]interface{}:
		return encodeDictionary(w, v)
	default:
		// For other types, we could use reflect, but it gets more complex.
		// For now, let's restrict to the types above.
		// Using reflect.Value:
		val := reflect.ValueOf(data)
		switch val.Kind() {
		case reflect.Slice:
			// Need to convert val (reflect.Value) to []interface{}
			// This is tricky if it's not already []interface{}.
			// For now, we'll only support []interface{} directly via type assertion.
			return fmt.Errorf("gobencode: unsupported slice type %T, only []interface{} is supported directly", data)
		case reflect.Map:
			// Similar to slices, direct support for map[string]interface{} is easier.
			// We'd need to check if key is string.
			return fmt.Errorf("gobencode: unsupported map type %T, only map[string]interface{} is supported directly", data)
		}
		return fmt.Errorf("gobencode: unsupported type %T for encoding", data)
	}
}

func encodeInteger(w *bufio.Writer, val int64) error {
	// Write 'i', then the integer, then 'e'
	if err := w.WriteByte('i'); err != nil {
		return err
	}
	// strconv.FormatInt converts int64 to its string representation in base 10
	if _, err := w.WriteString(strconv.FormatInt(val, 10)); err != nil {
		return err
	}
	return w.WriteByte('e')
}

func encodeString(w *bufio.Writer, val string) error {
	// Write length, then ':', then the string
	// strconv.Itoa converts int to string. len(val) returns int.
	if _, err := w.WriteString(strconv.Itoa(len(val))); err != nil {
		return err
	}
	if err := w.WriteByte(':'); err != nil {
		return err
	}
	_, err := w.WriteString(val)
	return err
}

func encodeList(w *bufio.Writer, list []interface{}) error {
	if err := w.WriteByte('l'); err != nil {
		return err
	}
	for _, item := range list {
		if err := encodeRecursive(w, item); err != nil { // Recursive call for each item
			return err
		}
	}
	return w.WriteByte('e')
}

func encodeDictionary(w *bufio.Writer, dict map[string]interface{}) error {
	if err := w.WriteByte('d'); err != nil {
		return err
	}

	// Keys in a bencoded dictionary MUST be sorted lexicographically.
	keys := make([]string, 0, len(dict))
	for k := range dict {
		keys = append(keys, k)
	}
	sort.Strings(keys) // Sort the keys

	for _, k := range keys {
		// Encode the key (which must be a string)
		if err := encodeString(w, k); err != nil {
			return fmt.Errorf("gobencode: error encoding dictionary key '%s': %w", k, err)
		}
		// Encode the value
		if err := encodeRecursive(w, dict[k]); err != nil { // Recursive call for the value
			return fmt.Errorf("gobencode: error encoding dictionary value for key '%s': %w", k, err)
		}
	}

	return w.WriteByte('e')
}