package rt

import (
	"strconv"

	"github.com/karl-tye/ktml"
)

// smallKeys is how many keys a key set compares linearly before it allocates
// a map. A set declared as a local stays on the stack until then.
const smallKeys = 8

// StringKeys is the one uniqueness set of a string-keyed repetition
// execution. Reset sizes it for the collection; Add reports whether a key is
// new.
type StringKeys struct {
	small [smallKeys]string
	count int
	hint  int
	set   map[string]struct{}
}

// Reset prepares the set for a collection of n items.
func (keys *StringKeys) Reset(n int) {
	keys.count, keys.hint, keys.set = 0, n, nil
}

// Add records key and reports false when it was already present.
func (keys *StringKeys) Add(key string) bool {
	if keys.set != nil {
		if _, duplicate := keys.set[key]; duplicate {
			return false
		}
		keys.set[key] = struct{}{}
		return true
	}
	for index := 0; index < keys.count; index++ {
		if keys.small[index] == key {
			return false
		}
	}
	if keys.count < smallKeys {
		keys.small[keys.count] = key
		keys.count++
		return true
	}
	keys.set = make(map[string]struct{}, max(keys.hint, smallKeys+1))
	for _, existing := range keys.small {
		keys.set[existing] = struct{}{}
	}
	keys.set[key] = struct{}{}
	return true
}

// IntegerKeys is the one uniqueness set of an integer-keyed repetition
// execution.
type IntegerKeys struct {
	small [smallKeys]int64
	count int
	hint  int
	set   map[int64]struct{}
}

// Reset prepares the set for a collection of n items.
func (keys *IntegerKeys) Reset(n int) {
	keys.count, keys.hint, keys.set = 0, n, nil
}

// Add records key and reports false when it was already present.
func (keys *IntegerKeys) Add(key int64) bool {
	if keys.set != nil {
		if _, duplicate := keys.set[key]; duplicate {
			return false
		}
		keys.set[key] = struct{}{}
		return true
	}
	for index := 0; index < keys.count; index++ {
		if keys.small[index] == key {
			return false
		}
	}
	if keys.count < smallKeys {
		keys.small[keys.count] = key
		keys.count++
		return true
	}
	keys.set = make(map[int64]struct{}, max(keys.hint, smallKeys+1))
	for _, existing := range keys.small {
		keys.set[existing] = struct{}{}
	}
	keys.set[key] = struct{}{}
	return true
}

// StringKey returns the canonical encoding of a string key: its Go-quoted
// form, which never equals an integer key's encoding.
func StringKey(key string) string { return strconv.Quote(key) }

// IntegerKey returns the canonical base-10 encoding of an integer key.
func IntegerKey(key int64) string { return strconv.FormatInt(key, 10) }

// DuplicateStringKey returns the typed error for a duplicate string key found
// by a repetition's preflight, before that repetition writes.
func DuplicateStringKey(component, source, key string) error {
	return &ktml.RenderError{Kind: ktml.ErrDuplicateKey, Component: component, Input: source, Key: StringKey(key)}
}

// DuplicateIntegerKey returns the typed error for a duplicate integer key.
func DuplicateIntegerKey(component, source string, key int64) error {
	return &ktml.RenderError{Kind: ktml.ErrDuplicateKey, Component: component, Input: source, Key: IntegerKey(key)}
}
