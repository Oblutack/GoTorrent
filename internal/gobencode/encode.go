package gobencode

import (
	"bufio"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
)

func Encode(w io.Writer, data interface{}) error {

	bw, ok := w.(*bufio.Writer)
	if !ok {
		bw = bufio.NewWriter(w)

		defer bw.Flush()
	}

	return encodeRecursive(bw, data)
}

func encodeRecursive(w *bufio.Writer, data interface{}) error {
	if data == nil {

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

	case uint:
		return encodeInteger(w, int64(v))
	case uint8:
		return encodeInteger(w, int64(v))
	case uint16:
		return encodeInteger(w, int64(v))
	case uint32:
		return encodeInteger(w, int64(v))
	case uint64:

		if v > uint64(9223372036854775807) {
			return fmt.Errorf("gobencode: uint64 value %d overflows int64", v)
		}
		return encodeInteger(w, int64(v))
	case []interface{}:
		return encodeList(w, v)
	case map[string]interface{}:
		return encodeDictionary(w, v)
	default:

		val := reflect.ValueOf(data)
		switch val.Kind() {
		case reflect.Slice:

			return fmt.Errorf("gobencode: unsupported slice type %T, only []interface{} is supported directly", data)
		case reflect.Map:

			return fmt.Errorf("gobencode: unsupported map type %T, only map[string]interface{} is supported directly", data)
		}
		return fmt.Errorf("gobencode: unsupported type %T for encoding", data)
	}
}

func encodeInteger(w *bufio.Writer, val int64) error {

	if err := w.WriteByte('i'); err != nil {
		return err
	}

	if _, err := w.WriteString(strconv.FormatInt(val, 10)); err != nil {
		return err
	}
	return w.WriteByte('e')
}

func encodeString(w *bufio.Writer, val string) error {

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
		if err := encodeRecursive(w, item); err != nil {
			return err
		}
	}
	return w.WriteByte('e')
}

func encodeDictionary(w *bufio.Writer, dict map[string]interface{}) error {
	if err := w.WriteByte('d'); err != nil {
		return err
	}

	keys := make([]string, 0, len(dict))
	for k := range dict {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {

		if err := encodeString(w, k); err != nil {
			return fmt.Errorf("gobencode: error encoding dictionary key '%s': %w", k, err)
		}

		if err := encodeRecursive(w, dict[k]); err != nil {
			return fmt.Errorf("gobencode: error encoding dictionary value for key '%s': %w", k, err)
		}
	}

	return w.WriteByte('e')
}
