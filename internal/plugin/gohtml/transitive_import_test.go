package gohtml

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestGenerateChecked_GIVENInvalidTransitiveImport_WHENGenerated_THENFailsBeforeEveryOutput(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"leaf.ktml":   `Leaf [label?: string] { span }`,
		"middle.ktml": "import { Leaf } from \"leaf\"\nMiddle { Leaf }",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	rootPath := filepath.Join(dir, "page.ktml")
	result := compiler.CompileSyntax(rootPath, []byte("import { Middle } from \"middle\"\nPage [title: string] { Middle }"))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	wrote := false

	output, err := GenerateChecked(result.Document, false, rootPath, func(string, []byte) { wrote = true })
	if err == nil {
		t.Fatal("GenerateChecked() error = nil, want transitive import schema error")
	}
	if output != "" || wrote {
		t.Fatalf("GenerateChecked() produced output = %q, wrote = %t", output, wrote)
	}
}
