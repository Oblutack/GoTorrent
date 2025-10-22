package gobencode

import (
	"bytes"
	"testing"
)

func TestEncode(t *testing.T) {
	testCases := []struct {
		name    string
		input   interface{}
		want    string
		wantErr bool
	}{

		{"encode integer zero", int64(0), "i0e", false},
		{"encode positive integer", int64(42), "i42e", false},
		{"encode negative integer", int64(-42), "i-42e", false},
		{"encode int type", int(123), "i123e", false},

		{"encode empty string", "", "0:", false},
		{"encode simple string", "spam", "4:spam", false},
		{"encode string with spaces", "hello world", "11:hello world", false},

		{"encode empty list", []interface{}{}, "le", false},
		{"encode list of integers", []interface{}{int64(1), int64(2), int64(3)}, "li1ei2ei3ee", false},
		{"encode list of strings", []interface{}{"a", "b", "c"}, "l1:a1:b1:ce", false},
		{
			"encode list of mixed types corrected",
			[]interface{}{int64(10), "eggs", []interface{}{"nested"}},
			"li10e4:eggsl6:nestedee",
			false,
		},

		{"encode empty dictionary", map[string]interface{}{}, "de", false},
		{
			"encode simple dictionary",
			map[string]interface{}{"key": "value"},
			"d3:key5:valuee",
			false,
		},
		{
			"encode dictionary sorted keys corrected",
			map[string]interface{}{"spam": "eggs", "foo": "bar"},
			"d3:foo3:bar4:spam4:eggse",
			false,
		},
		{
			"encode dictionary with int and list",
			map[string]interface{}{
				"an_int": int64(99),
				"a_list": []interface{}{"one", int64(2)},
			},
			"d6:a_listl3:onei2ee6:an_inti99ee",
			false,
		},
		{
			"encode nested dictionary",
			map[string]interface{}{
				"outer": map[string]interface{}{
					"num_key":   int64(7),
					"inner_key": "inner_value",
				},
			},

			"d5:outerd9:inner_key11:inner_value7:num_keyi7eee",
			false,
		},

		{"encode nil directly", nil, "", true},
		{"encode unsupported type float", float64(3.14), "", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := Encode(&buf, tc.input)

			if (err != nil) != tc.wantErr {
				t.Errorf("Encode() error = %v, wantErr %v", err, tc.wantErr)
				return
			}

			if !tc.wantErr {
				got := buf.String()
				if got != tc.want {
					t.Errorf("Encode() got = %q, want %q", got, tc.want)
				}
			}
		})
	}
}
