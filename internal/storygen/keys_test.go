package storygen

import (
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

// TestUniqueKeys_GIVENDuplicateIntegersAndStrings_WHENMadeUnique_THENEveryItemDiffers
// covers the key kinds a repetition accepts, string and integer, at a
// nested path, and keeps values that already differ.
func TestUniqueKeys_GIVENDuplicateIntegersAndStrings_WHENMadeUnique_THENEveryItemDiffers(t *testing.T) {
	integer := func(n int64) compiler.Value {
		return compiler.Value{Kind: compiler.ScalarValueKind, Type: compiler.ScalarTypeOf(compiler.IntegerType), Integer: n, Number: float64(n)}
	}
	text := func(s string) compiler.Value {
		return compiler.Value{Kind: compiler.ScalarValueKind, Type: compiler.ScalarTypeOf(compiler.StringType), String: s}
	}
	item := func(id compiler.Value) compiler.Value {
		return compiler.Value{Kind: compiler.RecordValueKind, Fields: []compiler.FieldValue{{Name: "meta", Value: compiler.Value{Kind: compiler.RecordValueKind, Fields: []compiler.FieldValue{{Name: "id", Value: id}}}}}}
	}
	integers := []compiler.Value{item(integer(4)), item(integer(4)), item(integer(5))}
	uniqueKeys(integers, []string{"meta", "id"}, "rows")
	seen := make(map[int64]bool)
	for index := range integers {
		id := keyValue(&integers[index], []string{"meta", "id"}).Integer
		if seen[id] {
			t.Errorf("duplicate integer key %d", id)
		}
		seen[id] = true
	}
	if !seen[4] || !seen[5] {
		t.Errorf("keys that already differed changed: %v", seen)
	}
	strings := []compiler.Value{text("Same"), text("Same"), text("Same 2")}
	uniqueKeys(strings, nil, "tags")
	got := map[string]bool{}
	for _, value := range strings {
		if got[value.String] {
			t.Errorf("duplicate string key %q", value.String)
		}
		got[value.String] = true
	}
}
