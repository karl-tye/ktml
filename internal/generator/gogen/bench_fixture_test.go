package gogen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

// TestGenerate_GIVENKeyedBenchmarkModule_WHENGenerated_THENCommittedRenderersAreCurrent
// keeps the generated renderers of the separate bench/keyed module current.
// Run with -update to rewrite them.
func TestGenerate_GIVENKeyedBenchmarkModule_WHENGenerated_THENCommittedRenderersAreCurrent(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "bench", "keyed", "list"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := compiler.NewWorkspace(dir).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if err := snapshot.Err(); err != nil {
		t.Fatalf("benchmark fixture diagnostics: %v", err)
	}
	options := gogen.Options{Package: "list", Packages: func(string) (gogen.GoPackage, error) {
		return gogen.GoPackage{ImportPath: "github.com/karl-tye/ktml/bench/keyed/list", Name: "list"}, nil
	}}
	schema, _, err := gogen.GenerateSchema(snapshot, filepath.Join(dir, "list.ktmlschema"), options)
	if err != nil {
		t.Fatalf("GenerateSchema() error = %v", err)
	}
	components, err := gogen.GenerateFile(snapshot, filepath.Join(dir, "list.ktml"), options)
	if err != nil {
		t.Fatalf("GenerateFile() error = %v", err)
	}
	for name, content := range map[string][]byte{"list_ktmlschema.go": schema, "list_ktml.go": components} {
		path := filepath.Join(dir, name)
		if *update {
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		committed, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(committed, content) {
			t.Errorf("%s is stale (run go test ./internal/generator/gogen -run KeyedBenchmark -update): %v", path, err)
		}
	}
}
