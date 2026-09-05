package bencode

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strconv"
)

// Marshal returns the bencoding of v.
//
// The output is canonical: dictionary keys are always emitted in
// lexicographic byte order, which is what the spec requires and what every
// other client assumes when hashing.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := marshalValue(&buf, reflect.ValueOf(v)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// MarshalTo writes the bencoding of v into buf.
func MarshalTo(buf *bytes.Buffer, v any) error {
	return marshalValue(buf, reflect.ValueOf(v))
}

func marshalValue(buf *bytes.Buffer, v reflect.Value) error {
	if !v.IsValid() {
		return &UnsupportedTypeError{Type: nil}
	}

	// Unwrap interfaces and pointers, checking for a custom Marshaler at each
	// level so RawMessage works whether it is held by value or by pointer.
	for {
		if v.Type().NumMethod() > 0 && v.CanInterface() {
			if m, ok := v.Interface().(Marshaler); ok {
				raw, err := m.MarshalBencode()
				if err != nil {
					return err
				}
				buf.Write(raw)
				return nil
			}
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return fmt.Errorf("bencode: cannot marshal nil %s", v.Type())
			}
			v = v.Elem()
			continue
		}
		break
	}

	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		buf.WriteByte('i')
		buf.WriteString(strconv.FormatInt(v.Int(), 10))
		buf.WriteByte('e')
		return nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		buf.WriteByte('i')
		buf.WriteString(strconv.FormatUint(v.Uint(), 10))
		buf.WriteByte('e')
		return nil

	case reflect.Bool:
		// Bencode has no boolean; BitTorrent spells it i1e / i0e (the "private"
		// flag, for instance).
		n := 0
		if v.Bool() {
			n = 1
		}
		buf.WriteString("i" + strconv.Itoa(n) + "e")
		return nil

	case reflect.String:
		writeByteString(buf, []byte(v.String()))
		return nil

	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			writeByteString(buf, v.Bytes())
			return nil
		}
		return marshalList(buf, v)

	case reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			b := make([]byte, v.Len())
			reflect.Copy(reflect.ValueOf(b), v)
			writeByteString(buf, b)
			return nil
		}
		return marshalList(buf, v)

	case reflect.Map:
		return marshalMap(buf, v)

	case reflect.Struct:
		return marshalStruct(buf, v)

	default:
		return &UnsupportedTypeError{Type: v.Type()}
	}
}

func writeByteString(buf *bytes.Buffer, b []byte) {
	buf.WriteString(strconv.Itoa(len(b)))
	buf.WriteByte(':')
	buf.Write(b)
}

func marshalList(buf *bytes.Buffer, v reflect.Value) error {
	buf.WriteByte('l')
	for i := 0; i < v.Len(); i++ {
		if err := marshalValue(buf, v.Index(i)); err != nil {
			return err
		}
	}
	buf.WriteByte('e')
	return nil
}

func marshalMap(buf *bytes.Buffer, v reflect.Value) error {
	if v.Type().Key().Kind() != reflect.String {
		return &UnsupportedTypeError{Type: v.Type()}
	}

	keys := make([]string, 0, v.Len())
	for _, k := range v.MapKeys() {
		keys = append(keys, k.String())
	}
	sort.Strings(keys)

	buf.WriteByte('d')
	for _, k := range keys {
		writeByteString(buf, []byte(k))
		if err := marshalValue(buf, v.MapIndex(reflect.ValueOf(k).Convert(v.Type().Key()))); err != nil {
			return err
		}
	}
	buf.WriteByte('e')
	return nil
}

func marshalStruct(buf *bytes.Buffer, v reflect.Value) error {
	fields := cachedFields(v.Type()) // already sorted by key

	buf.WriteByte('d')
	for i := range fields.list {
		f := &fields.list[i]
		fv := fieldByIndex(v, f.index, false)
		if !fv.IsValid() {
			continue
		}
		if f.omitEmpty && isEmptyValue(fv) {
			continue
		}
		// A nil pointer or interface has nothing to write; skip it rather than
		// failing the whole encode.
		if (fv.Kind() == reflect.Pointer || fv.Kind() == reflect.Interface) && fv.IsNil() {
			continue
		}
		writeByteString(buf, []byte(f.name))
		if err := marshalValue(buf, fv); err != nil {
			return err
		}
	}
	buf.WriteByte('e')
	return nil
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return false
}
