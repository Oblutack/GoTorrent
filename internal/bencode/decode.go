package bencode

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
)

// Unmarshal parses the bencoded data and stores the result in the value
// pointed to by v.
//
// Trailing bytes after the first complete value are ignored; some torrents in
// the wild carry padding.
func Unmarshal(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &InvalidUnmarshalError{Type: reflect.TypeOf(v)}
	}
	d := &decodeState{data: data}
	return d.value(rv)
}

type decodeState struct {
	data  []byte
	pos   int
	depth int
}

func (d *decodeState) peek() (byte, error) {
	if d.pos >= len(d.data) {
		return 0, syntaxErrorf(d.pos, "unexpected end of input")
	}
	return d.data[d.pos], nil
}

// skip advances past one complete value and returns its raw bytes. This is how
// RawMessage is captured and how unknown dictionary keys are discarded.
func (d *decodeState) skip() ([]byte, error) {
	start := d.pos
	if err := d.skipValue(); err != nil {
		return nil, err
	}
	return d.data[start:d.pos], nil
}

func (d *decodeState) skipValue() error {
	d.depth++
	defer func() { d.depth-- }()
	if d.depth > maxDepth {
		return fmt.Errorf("%w: exceeded %d levels at byte %d", ErrTooDeep, maxDepth, d.pos)
	}

	c, err := d.peek()
	if err != nil {
		return err
	}

	switch {
	case c == 'i':
		_, err := d.scanInt()
		return err

	case c >= '0' && c <= '9':
		_, err := d.scanString()
		return err

	case c == 'l', c == 'd':
		d.pos++
		for {
			next, err := d.peek()
			if err != nil {
				return err
			}
			if next == 'e' {
				d.pos++
				return nil
			}
			if err := d.skipValue(); err != nil {
				return err
			}
		}

	default:
		return syntaxErrorf(d.pos, "unexpected token %q", c)
	}
}

// scanInt consumes an "i<digits>e" token.
func (d *decodeState) scanInt() (int64, error) {
	start := d.pos
	d.pos++ // consume 'i'
	end := d.indexOf('e')
	if end < 0 {
		return 0, syntaxErrorf(start, "unterminated integer")
	}
	text := string(d.data[d.pos:end])
	d.pos = end + 1

	// strconv accepts "+5" and rejects nothing else we care about, but bencode
	// forbids leading zeros, "-0", and a leading plus.
	if err := checkIntegerText(text); err != nil {
		return 0, syntaxErrorf(start, "%s: %q", err, text)
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, syntaxErrorf(start, "invalid integer %q", text)
	}
	return n, nil
}

func checkIntegerText(text string) error {
	switch {
	case text == "":
		return errors.New("empty integer")
	case text == "-0":
		return errors.New("negative zero")
	case text == "0":
		return nil
	}
	body := text
	if body[0] == '-' {
		body = body[1:]
		if body == "" {
			return errors.New("lone minus sign")
		}
	}
	if body[0] == '0' {
		return errors.New("leading zero")
	}
	for _, c := range []byte(body) {
		if c < '0' || c > '9' {
			return errors.New("non-digit in integer")
		}
	}
	return nil
}

// scanString consumes a "<length>:<bytes>" token and returns a sub-slice of
// the input. The result aliases d.data, so callers that keep it must copy.
func (d *decodeState) scanString() ([]byte, error) {
	start := d.pos
	colon := d.indexOf(':')
	if colon < 0 {
		return nil, syntaxErrorf(start, "string length is not terminated by ':'")
	}
	lengthText := string(d.data[d.pos:colon])
	if lengthText == "" {
		return nil, syntaxErrorf(start, "string has no length")
	}
	if len(lengthText) > 1 && lengthText[0] == '0' {
		return nil, syntaxErrorf(start, "string length %q has a leading zero", lengthText)
	}
	n, err := strconv.Atoi(lengthText)
	if err != nil || n < 0 {
		return nil, syntaxErrorf(start, "invalid string length %q", lengthText)
	}

	// Bound the length against what is actually left, so a claimed length of
	// 2 GB on a 40-byte input fails here rather than allocating.
	if n > len(d.data)-(colon+1) {
		return nil, syntaxErrorf(start, "string of %d bytes overruns the input", n)
	}

	d.pos = colon + 1 + n
	return d.data[colon+1 : d.pos], nil
}

func (d *decodeState) indexOf(target byte) int {
	for i := d.pos; i < len(d.data); i++ {
		if d.data[i] == target {
			return i
		}
	}
	return -1
}

// value decodes one bencoded value into v.
func (d *decodeState) value(v reflect.Value) error {
	d.depth++
	defer func() { d.depth-- }()
	if d.depth > maxDepth {
		return fmt.Errorf("%w: exceeded %d levels at byte %d", ErrTooDeep, maxDepth, d.pos)
	}

	v, unmarshaler, err := indirect(v)
	if err != nil {
		return err
	}
	if unmarshaler != nil {
		raw, err := d.skip()
		if err != nil {
			return err
		}
		return unmarshaler.UnmarshalBencode(raw)
	}

	c, err := d.peek()
	if err != nil {
		return err
	}

	switch {
	case c == 'i':
		n, err := d.scanInt()
		if err != nil {
			return err
		}
		return storeInt(n, v)

	case c >= '0' && c <= '9':
		s, err := d.scanString()
		if err != nil {
			return err
		}
		return storeString(s, v)

	case c == 'l':
		return d.list(v)

	case c == 'd':
		return d.dict(v)

	default:
		return syntaxErrorf(d.pos, "unexpected token %q", c)
	}
}

// indirect walks pointers, allocating as needed, and reports an Unmarshaler if
// one is found along the way.
func indirect(v reflect.Value) (reflect.Value, Unmarshaler, error) {
	for {
		// A non-nil interface holding a pointer: follow it.
		if v.Kind() == reflect.Interface && !v.IsNil() {
			e := v.Elem()
			if e.Kind() == reflect.Pointer && !e.IsNil() {
				v = e
				continue
			}
		}
		if v.Kind() != reflect.Pointer {
			break
		}
		if v.IsNil() {
			if !v.CanSet() {
				return reflect.Value{}, nil, errors.New("bencode: cannot allocate through an unaddressable pointer")
			}
			v.Set(reflect.New(v.Type().Elem()))
		}
		if v.Type().NumMethod() > 0 && v.CanInterface() {
			if u, ok := v.Interface().(Unmarshaler); ok {
				return v, u, nil
			}
		}
		v = v.Elem()
	}

	if v.CanAddr() {
		if u, ok := v.Addr().Interface().(Unmarshaler); ok {
			return v, u, nil
		}
	}
	return v, nil, nil
}

func storeInt(n int64, v reflect.Value) error {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.OverflowInt(n) {
			return &UnmarshalTypeError{Value: "integer " + strconv.FormatInt(n, 10), Type: v.Type()}
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n < 0 || v.OverflowUint(uint64(n)) {
			return &UnmarshalTypeError{Value: "integer " + strconv.FormatInt(n, 10), Type: v.Type()}
		}
		v.SetUint(uint64(n))
	case reflect.Bool:
		v.SetBool(n != 0)
	case reflect.Interface:
		if v.NumMethod() != 0 {
			return &UnmarshalTypeError{Value: "integer", Type: v.Type()}
		}
		v.Set(reflect.ValueOf(n))
	default:
		return &UnmarshalTypeError{Value: "integer", Type: v.Type()}
	}
	return nil
}

func storeString(s []byte, v reflect.Value) error {
	switch v.Kind() {
	case reflect.String:
		v.SetString(string(s))
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.Uint8 {
			return &UnmarshalTypeError{Value: "string", Type: v.Type()}
		}
		// Copy: s aliases the caller's input buffer.
		v.SetBytes(append([]byte(nil), s...))
	case reflect.Array:
		if v.Type().Elem().Kind() != reflect.Uint8 {
			return &UnmarshalTypeError{Value: "string", Type: v.Type()}
		}
		if v.Len() != len(s) {
			return &UnmarshalTypeError{
				Value: fmt.Sprintf("string of %d bytes", len(s)),
				Type:  v.Type(),
			}
		}
		reflect.Copy(v, reflect.ValueOf(s))
	case reflect.Interface:
		if v.NumMethod() != 0 {
			return &UnmarshalTypeError{Value: "string", Type: v.Type()}
		}
		v.Set(reflect.ValueOf(string(s)))
	default:
		return &UnmarshalTypeError{Value: "string", Type: v.Type()}
	}
	return nil
}

func (d *decodeState) list(v reflect.Value) error {
	start := d.pos
	d.pos++ // consume 'l'

	switch v.Kind() {
	case reflect.Slice, reflect.Array:
	case reflect.Interface:
		if v.NumMethod() != 0 {
			return &UnmarshalTypeError{Value: "list", Type: v.Type()}
		}
		var out []any
		for {
			c, err := d.peek()
			if err != nil {
				return err
			}
			if c == 'e' {
				d.pos++
				v.Set(reflect.ValueOf(out))
				return nil
			}
			var elem any
			if err := d.value(reflect.ValueOf(&elem).Elem()); err != nil {
				return err
			}
			out = append(out, elem)
		}
	default:
		return &UnmarshalTypeError{Value: "list", Type: v.Type()}
	}

	i := 0
	for {
		c, err := d.peek()
		if err != nil {
			return err
		}
		if c == 'e' {
			d.pos++
			break
		}

		if v.Kind() == reflect.Slice {
			if i >= v.Cap() {
				grown := reflect.MakeSlice(v.Type(), v.Len(), max(4, v.Cap()*2))
				reflect.Copy(grown, v)
				v.Set(grown)
			}
			if i >= v.Len() {
				v.SetLen(i + 1)
			}
		}

		if i < v.Len() {
			if err := d.value(v.Index(i)); err != nil {
				return err
			}
		} else {
			// Array is full: discard the remaining elements rather than fail.
			if _, err := d.skip(); err != nil {
				return err
			}
		}
		i++
	}

	if v.Kind() == reflect.Slice {
		if i == 0 {
			v.Set(reflect.MakeSlice(v.Type(), 0, 0))
		} else {
			v.SetLen(i)
		}
	} else if i < v.Len() {
		// Zero out the tail of a partially filled array.
		z := reflect.Zero(v.Type().Elem())
		for ; i < v.Len(); i++ {
			v.Index(i).Set(z)
		}
	}
	_ = start
	return nil
}

func (d *decodeState) dict(v reflect.Value) error {
	d.pos++ // consume 'd'

	switch v.Kind() {
	case reflect.Struct:
		return d.dictIntoStruct(v)
	case reflect.Map:
		return d.dictIntoMap(v)
	case reflect.Interface:
		if v.NumMethod() != 0 {
			return &UnmarshalTypeError{Value: "dictionary", Type: v.Type()}
		}
		out := make(map[string]any)
		for {
			c, err := d.peek()
			if err != nil {
				return err
			}
			if c == 'e' {
				d.pos++
				v.Set(reflect.ValueOf(out))
				return nil
			}
			key, err := d.dictKey()
			if err != nil {
				return err
			}
			var elem any
			if err := d.value(reflect.ValueOf(&elem).Elem()); err != nil {
				return err
			}
			out[key] = elem
		}
	default:
		return &UnmarshalTypeError{Value: "dictionary", Type: v.Type()}
	}
}

func (d *decodeState) dictKey() (string, error) {
	c, err := d.peek()
	if err != nil {
		return "", err
	}
	if c < '0' || c > '9' {
		return "", syntaxErrorf(d.pos, "dictionary key is not a string")
	}
	key, err := d.scanString()
	if err != nil {
		return "", err
	}
	return string(key), nil
}

func (d *decodeState) dictIntoStruct(v reflect.Value) error {
	fields := cachedFields(v.Type())
	for {
		c, err := d.peek()
		if err != nil {
			return err
		}
		if c == 'e' {
			d.pos++
			return nil
		}

		key, err := d.dictKey()
		if err != nil {
			return err
		}

		f, ok := fields.byKey[key]
		if !ok {
			// Unknown keys are normal: torrents carry all sorts of extras.
			if _, err := d.skip(); err != nil {
				return err
			}
			continue
		}

		target := fieldByIndex(v, f.index, true)
		if err := d.value(target); err != nil {
			var typeErr *UnmarshalTypeError
			if errors.As(err, &typeErr) && typeErr.Struct == "" {
				typeErr.Struct = v.Type().Name()
				typeErr.Field = key
			}
			return err
		}
	}
}

func (d *decodeState) dictIntoMap(v reflect.Value) error {
	t := v.Type()
	if t.Key().Kind() != reflect.String {
		return &UnmarshalTypeError{Value: "dictionary", Type: t}
	}
	if v.IsNil() {
		v.Set(reflect.MakeMap(t))
	}

	for {
		c, err := d.peek()
		if err != nil {
			return err
		}
		if c == 'e' {
			d.pos++
			return nil
		}

		key, err := d.dictKey()
		if err != nil {
			return err
		}
		elem := reflect.New(t.Elem()).Elem()
		if err := d.value(elem); err != nil {
			return err
		}
		v.SetMapIndex(reflect.ValueOf(key).Convert(t.Key()), elem)
	}
}
