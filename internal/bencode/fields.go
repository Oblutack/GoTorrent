package bencode

import (
	"reflect"
	"strings"
	"sync"
)

// field describes one struct field that participates in bencoding.
type field struct {
	name      string // the bencode dictionary key
	index     []int  // path for reflect.Value.FieldByIndex
	omitEmpty bool
}

// structFields is the cached field set of a struct type, both as an ordered
// slice (for Marshal, sorted by key) and as a lookup map (for Unmarshal).
type structFields struct {
	list  []field
	byKey map[string]*field
}

var fieldCache sync.Map // map[reflect.Type]*structFields

// cachedFields returns the bencode field set for a struct type, computing it
// once per type.
func cachedFields(t reflect.Type) *structFields {
	if cached, ok := fieldCache.Load(t); ok {
		return cached.(*structFields)
	}
	computed := typeFields(t)
	actual, _ := fieldCache.LoadOrStore(t, computed)
	return actual.(*structFields)
}

// typeFields walks a struct type, following embedded structs, and builds its
// field set. A field is skipped when it is unexported or tagged "-"; untagged
// fields fall back to the Go field name lowercased, which is what the majority
// of bencode dictionary keys look like.
func typeFields(t reflect.Type) *structFields {
	sf := &structFields{byKey: make(map[string]*field)}
	collectFields(t, nil, sf)

	// Sorted by key so Marshal emits canonical bencode: a dictionary's keys
	// must appear in lexicographic order.
	sortFields(sf.list)
	for i := range sf.list {
		sf.byKey[sf.list[i].name] = &sf.list[i]
	}
	return sf
}

func collectFields(t reflect.Type, index []int, sf *structFields) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)

		if f.Anonymous && f.Type.Kind() == reflect.Struct && f.Tag.Get("bencode") == "" {
			collectFields(f.Type, append(index, i), sf)
			continue
		}
		if !f.IsExported() {
			continue
		}

		tag := f.Tag.Get("bencode")
		if tag == "-" {
			continue
		}

		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = strings.ToLower(f.Name)
		}

		sf.list = append(sf.list, field{
			name:      name,
			index:     append(append([]int(nil), index...), i),
			omitEmpty: hasOption(opts, "omitempty"),
		})
	}
}

func hasOption(opts, want string) bool {
	for opts != "" {
		var opt string
		opt, opts, _ = strings.Cut(opts, ",")
		if opt == want {
			return true
		}
	}
	return false
}

// sortFields orders fields by key. It is a plain insertion sort: field sets are
// tiny and this avoids pulling in sort just for this.
func sortFields(fields []field) {
	for i := 1; i < len(fields); i++ {
		for j := i; j > 0 && fields[j].name < fields[j-1].name; j-- {
			fields[j], fields[j-1] = fields[j-1], fields[j]
		}
	}
}

// fieldByIndex resolves an index path, allocating nil pointers along the way
// when alloc is set. It returns the zero Value if a nil pointer is met and
// alloc is false.
func fieldByIndex(v reflect.Value, index []int, alloc bool) reflect.Value {
	for i, x := range index {
		if i > 0 {
			for v.Kind() == reflect.Pointer {
				if v.IsNil() {
					if !alloc {
						return reflect.Value{}
					}
					v.Set(reflect.New(v.Type().Elem()))
				}
				v = v.Elem()
			}
		}
		v = v.Field(x)
	}
	return v
}
