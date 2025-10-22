package gobencode

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestDecode(t *testing.T) {
	testCases := []struct {
		name    string
		input   string
		want    interface{}
		wantErr error
	}{

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
			wantErr: ErrMalformedData,
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
			wantErr: ErrMalformedData,
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
			input:   "10:spam",
			wantErr: ErrMalformedData,
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
			input: "li42e4:spame",
			want:  []interface{}{int64(42), "spam"},
		},
		{
			name:  "nested list",
			input: "lli1ei2ee4:spame",
			want:  []interface{}{[]interface{}{int64(1), int64(2)}, "spam"},
		},
		{
			name:    "list missing e",
			input:   "li1e",
			wantErr: ErrMalformedData,
		},

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
			name:  "nested dictionary",
			input: "d5:outerd3:key5:valueee",
			want: map[string]interface{}{
				"outer": map[string]interface{}{"key": "value"},
			},
		},
		{
			name:    "dictionary missing value",
			input:   "d3:keye",
			wantErr: ErrMalformedData,
		},
		{
			name:    "dictionary key not a string",
			input:   "di1e5:valuee",
			wantErr: ErrMalformedData,
		},
		{
			name:    "dictionary keys not sorted",
			input:   "d1:B1:b1:A1:ae",
			wantErr: ErrMalformedData,
		},
		{
			name:  "dictionary with multiple keys sorted",
			input: "d1:A1:a1:B1:be",
			want:  map[string]interface{}{"A": "a", "B": "b"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := strings.NewReader(tc.input)
			got, err := Decode(r)

			if tc.wantErr != nil {
				if err == nil {
					t.Errorf("Decode() error = nil, wantErr %v", tc.wantErr)
					return
				}

				if !errors.Is(err, tc.wantErr) && tc.wantErr == ErrMalformedData {

					t.Errorf("Decode() error = %v, wantErr to be or wrap %v", err, tc.wantErr)
				}

			} else if err != nil {
				t.Errorf("Decode() unexpected error = %v", err)
				return
			}

			if tc.wantErr == nil && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decode() got = %v (%T), want %v (%T)", got, got, tc.want, tc.want)
			}

			if err == nil {
				var remainingByte [1]byte
				n, readErr := r.Read(remainingByte[:])
				if n > 0 {
					t.Errorf("Decode() left unconsumed data: '%c'", remainingByte[0])
				} else if readErr != io.EOF && readErr != nil {

					t.Errorf("Decode() error reading for leftover data: %v", readErr)
				}
			}
		})
	}
}
