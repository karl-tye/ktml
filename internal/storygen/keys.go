package storygen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
)

// typeKey identifies a collection's element type across the workspace.
func typeKey(typ compiler.Type) string {
	switch typ.Kind {
	case compiler.RecordTypeKind:
		return "record:" + string(typ.Record)
	case compiler.StateTypeKind:
		return "state:" + string(typ.State)
	}
	return string(typ.Kind) + ":" + typ.String()
}

// keyPaths returns the key paths, below the item, that the workspace's
// repetitions use over collections of element: [term] for `for fact in
// facts key fact.term`, [meta id] for `key row.meta.id`, and [] for `key
// tag`. Every repetition counts, not only the component's own: a component
// passes its collections to the components it renders.
func (s *synthesizer) keyPaths(element compiler.Type) [][]string {
	if s.keys == nil {
		s.keys = make(map[string][][]string)
		seen := make(map[string]bool)
		for _, file := range s.snapshot.Files() {
			for _, repetition := range s.snapshot.Repetitions(file) {
				if len(repetition.Key.Parts) == 0 || repetition.Collection.Type.Element == nil {
					continue
				}
				var path []string
				for _, part := range repetition.Key.Parts[1:] {
					path = append(path, part.Name)
				}
				typ := typeKey(*repetition.Collection.Type.Element)
				id := typ + "\x00" + strings.Join(path, ".")
				if seen[id] {
					continue
				}
				seen[id] = true
				s.keys[typ] = append(s.keys[typ], path)
			}
		}
		for _, paths := range s.keys {
			sort.Slice(paths, func(i, j int) bool { return strings.Join(paths[i], ".") < strings.Join(paths[j], ".") })
		}
	}
	return s.keys[typeKey(element)]
}

// keyValue finds the value at path below an item, or nil when a field on
// the way is absent.
func keyValue(item *compiler.Value, path []string) *compiler.Value {
	value := item
	for _, name := range path {
		var next *compiler.Value
		for index := range value.Fields {
			if value.Fields[index].Name == name {
				next = &value.Fields[index].Value
				break
			}
		}
		if next == nil {
			return nil
		}
		value = next
	}
	return value
}

// uniqueKeys makes the values at a key path differ between items, as a
// keyed repetition requires: a LongText story gives every free-text field
// the same text, and a markup-shaped field is empty in every item. Values
// that already differ are kept, so stories without duplicates do not
// change. A later item's duplicate string gets its position appended, and
// a duplicate integer the next free number.
func uniqueKeys(items []compiler.Value, path []string, collection string) {
	values := make([]*compiler.Value, 0, len(items))
	for index := range items {
		if value := keyValue(&items[index], path); value != nil && value.Kind == compiler.ScalarValueKind {
			values = append(values, value)
		}
	}
	text := func(value *compiler.Value) string {
		if value.Type.IsScalar(compiler.IntegerType) {
			return fmt.Sprintf("i%d", value.Integer)
		}
		return "s" + value.String
	}
	used := make(map[string]bool)
	duplicate := false
	for _, value := range values {
		if used[text(value)] {
			duplicate = true
		}
		used[text(value)] = true
	}
	if !duplicate {
		return
	}
	name := collection
	if len(path) > 0 {
		name = path[len(path)-1]
	}
	taken := make(map[string]bool)
	for position, value := range values {
		if !taken[text(value)] {
			taken[text(value)] = true
			continue
		}
		switch {
		case value.Type.IsScalar(compiler.IntegerType):
			for taken[text(value)] || used[text(value)] {
				value.Integer++
				value.Number = float64(value.Integer)
			}
		default:
			base := value.String
			if base == "" {
				base = humanize(name)
			}
			for suffix := position + 1; ; suffix++ {
				value.String = fmt.Sprintf("%s %d", base, suffix)
				if !taken[text(value)] && !used[text(value)] {
					break
				}
			}
		}
		taken[text(value)] = true
	}
}
