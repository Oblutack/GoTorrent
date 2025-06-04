package gobencode // Must be the same package name for testing unexported functions if needed,
// or gobencode_test if testing only exported functions (Decode).
// Let's use 'package gobencode' for now as our parse* functions are not exported.

import (
	"errors"
	"io"
	"reflect" // For DeepEqual
	"strings"
	"testing"
)

func TestDecode(t *testing.T) {
	testCases := []struct {
		name    string      // Name of the test case
		input   string      // Bencoded input string
		want    interface{} // Expected Go value
		wantErr error       // Expected error (nil if no error)
	}{
		// --- Integer Test Cases ---
		{
			name:  "integer zero",
			input: "i0e",
			want:  int64(0),
		},
		{
			name:  "positive integer",
			input: "i42e",
			want:  int64(42),
		},
		{
			name:  "negative integer",
			input: "i-42e",
			want:  int64(-42),
		},
		{
			name:    "invalid integer - empty",
			input:   "ie",
			wantErr: ErrMalformedData, // Or a more specific error if you wrap it
		},
		{
			name:    "invalid integer - leading zero positive",
			input:   "i042e",
			wantErr: ErrMalformedData,
		},
		{
			name:    "invalid integer - leading zero negative",
			input:   "i-042e",
			wantErr: ErrMalformedData,
		},
		{
			name:    "invalid integer - missing e",
			input:   "i42",
			wantErr: ErrMalformedData, // Will likely be an EOF error wrapped by ErrMalformedData
		},
		{
			name:    "invalid integer - non-digit",
			input:   "i42xe",
			wantErr: ErrMalformedData,
		},
        {
            name:    "invalid integer - just hyphen",
            input:   "i-e",
            wantErr: ErrMalformedData,
        },


		// --- String Test Cases ---
		{
			name:  "empty string",
			input: "0:",
			want:  "",
		},
		{
			name:  "simple string",
			input: "4:spam",
			want:  "spam",
		},
		{
			name:    "string missing colon",
			input:   "4spam",
			wantErr: ErrMalformedData,
		},
		{
			name:    "string length too short",
			input:   "10:spam", // claims 10 bytes, provides 4
			wantErr: ErrMalformedData, // Will be EOF wrapped
		},
		{
			name:    "string length not a number",
			input:   "spam:spam",
			wantErr: ErrMalformedData,
		},
		{
			name:    "string length has leading zero",
			input:   "04:spam",
			wantErr: ErrMalformedData,
		},
		// TODO: Add more test cases for strings

		// --- List Test Cases ---
		{
			name:  "empty list",
			input: "le",
			want:  make([]interface{}, 0),
		},
		{
			name:  "list of integers",
			input: "li1ei2ei3ee",
			want:  []interface{}{int64(1), int64(2), int64(3)},
		},
		{
			name:  "list of mixed types",
			input: "li42e4:spame", // [42, "spam"]
			want:  []interface{}{int64(42), "spam"},
		},
		{
			name:  "nested list",
			input: "lli1ei2ee4:spame", // [[1, 2], "spam"]
			want:  []interface{}{[]interface{}{int64(1), int64(2)}, "spam"},
		},
		{
			name:    "list missing e",
			input:   "li1e",
			wantErr: ErrMalformedData, // EOF wrapped
		},
		// TODO: Add more test cases for lists


		// --- Dictionary Test Cases ---
		{
			name:  "empty dictionary",
			input: "de",
			want:  map[string]interface{}{},
		},
		{
			name:  "simple dictionary",
			input: "d3:key5:valuee",
			want:  map[string]interface{}{"key": "value"},
		},
		{
			name:  "dictionary with integer value",
			input: "d3:numi42ee",
			want:  map[string]interface{}{"num": int64(42)},
		},
		{
			name:  "dictionary with list value",
			input: "d4:listli1ei2eee",
			want:  map[string]interface{}{"list": []interface{}{int64(1), int64(2)}},
		},
		{
			name: "nested dictionary",
			input: "d5:outerd3:key5:valueee", // {"outer": {"key": "value"}}
			want: map[string]interface{}{
				"outer": map[string]interface{}{"key": "value"},
			},
		},
		{
			name:    "dictionary missing value",
			input:   "d3:keye",
			wantErr: ErrMalformedData, // EOF or other error wrapped
		},
		{
			name:    "dictionary key not a string",
			input:   "di1e5:valuee",
			wantErr: ErrMalformedData,
		},
		{
			name:    "dictionary keys not sorted",
			input:   "d1:B1:b1:A1:ae", // B comes after A
			wantErr: ErrMalformedData,
		},
        {
            name: "dictionary with multiple keys sorted",
            input: "d1:A1:a1:B1:be",
            want: map[string]interface{}{"A": "a", "B": "b"},
        },
		// TODO: Add more test cases for dictionaries
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := strings.NewReader(tc.input)
			got, err := Decode(r) // Call our main Decode function

			// Check error
			if tc.wantErr != nil {
				if err == nil {
					t.Errorf("Decode() error = nil, wantErr %v", tc.wantErr)
					return
				}
				// We might want to check if the error *is* or *wraps* tc.wantErr
				// For simplicity now, we'll just check if an error was expected and one occurred.
				// A more robust check: if !errors.Is(err, tc.wantErr) { ... }
                // Or check the error message string if specific.
                // For now, let's assume any error is fine if one was expected, or check if it IS ErrMalformedData
				if !errors.Is(err, tc.wantErr) && tc.wantErr == ErrMalformedData {
					// If we expect ErrMalformedData, let's be a bit more specific
					// This helps catch cases where we get an unexpected I/O error instead of a parsing error
					t.Errorf("Decode() error = %v, wantErr to be or wrap %v", err, tc.wantErr)
				}
                // If tc.wantErr is not ErrMalformedData, it might be a generic io.EOF for example.
                // For now, just checking if err != nil is enough if tc.wantErr is not nil.
			} else if err != nil {
				t.Errorf("Decode() unexpected error = %v", err)
				return
			}

			// Check value if no error was expected
			if tc.wantErr == nil && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decode() got = %v (%T), want %v (%T)", got, got, tc.want, tc.want)
			}

            // Check if all input was consumed for successful decodes
            // If there's leftover data, it might indicate a parsing bug.
            if err == nil {
                var remainingByte [1]byte
                n, readErr := r.Read(remainingByte[:])
                if n > 0 {
                    t.Errorf("Decode() left unconsumed data: '%c'", remainingByte[0])
                } else if readErr != io.EOF && readErr != nil {
                    // This case is less likely if Decode itself didn't error,
                    // but good to be aware of.
                    t.Errorf("Decode() error reading for leftover data: %v", readErr)
                }
            }
		})
	}
}