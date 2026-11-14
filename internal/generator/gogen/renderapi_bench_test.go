package gogen_test

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

const renderAPIModule = "github.com/karl-tye/ktml/bench/renderapi"

// renderAPITrees maps each ADR-0003 candidate to the package tree it is
// generated into inside the bench/renderapi module.
var renderAPITrees = []struct {
	api  gogen.API
	tree string
}{
	{gogen.APIStruct, "h"},
	{gogen.APIDirect, "b"},
}

// TestGenerate_GIVENRenderAPIBenchmarkModule_WHENGenerated_THENCommittedRenderersAreCurrent
// keeps the generated renderers of the separate bench/renderapi module
// current. The KTML sources under bench/renderapi/ktml are generated once per
// candidate API into the h tree and the b floor tree, with the same relative package
// structure. Run with -update to rewrite them.
func TestGenerate_GIVENRenderAPIBenchmarkModule_WHENGenerated_THENCommittedRenderersAreCurrent(t *testing.T) {
	module, err := filepath.Abs(filepath.Join("..", "..", "..", "bench", "renderapi"))
	if err != nil {
		t.Fatal(err)
	}
	sources := filepath.Join(module, "ktml")
	snapshot, err := compiler.NewWorkspace(sources).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if err := snapshot.Err(); err != nil {
		t.Fatalf("benchmark source diagnostics: %v", err)
	}
	var documents []string
	err = filepath.WalkDir(sources, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && (strings.HasSuffix(file, ".ktml") || strings.HasSuffix(file, ".ktmlschema")) {
			documents = append(documents, file)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) == 0 {
		t.Fatalf("no KTML sources under %s", sources)
	}
	for _, candidate := range renderAPITrees {
		packages := func(dir string) (gogen.GoPackage, error) {
			relative, err := filepath.Rel(sources, dir)
			if err != nil {
				return gogen.GoPackage{}, err
			}
			importPath := path.Join(renderAPIModule, candidate.tree, filepath.ToSlash(relative))
			return gogen.GoPackage{ImportPath: importPath, Name: path.Base(importPath)}, nil
		}
		for _, document := range documents {
			relative, err := filepath.Rel(sources, document)
			if err != nil {
				t.Fatal(err)
			}
			options := gogen.Options{Package: filepath.Base(filepath.Dir(document)), Packages: packages, API: candidate.api}
			var content []byte
			var name string
			if strings.HasSuffix(document, ".ktmlschema") {
				content, _, err = gogen.GenerateSchema(snapshot, document, options)
				name = strings.TrimSuffix(relative, ".ktmlschema") + "_ktmlschema.go"
			} else {
				content, err = gogen.GenerateFile(snapshot, document, options)
				name = strings.TrimSuffix(relative, ".ktml") + "_ktml.go"
			}
			if err != nil {
				t.Fatalf("generate %s for %s: %v", relative, candidate.api, err)
			}
			// The benchmark module holds copies without their sources, so
			// line directives would name files that are not there.
			content = stripDirectives(t, content)
			target := filepath.Join(module, candidate.tree, name)
			if *update {
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, content, 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			committed, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(committed, content) {
				t.Errorf("%s is stale (run go test ./internal/generator/gogen -run RenderAPIBenchmark -update): %v", target, err)
			}
		}
	}
}
