package gohtml

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
)

func TestGenerateChecked_GIVENInvalidImportedSource_WHENGenerated_THENFailsBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "components.ktml"), []byte(`Card { div`), 0o600); err != nil {
		t.Fatalf("write imported source: %v", err)
	}
	doc := &ast.Document{Nodes: []ast.Node{
		&ast.ImportStmt{Items: []string{"Card"}, Path: `"components.ktml"`},
		&ast.ElementDecl{Name: "Page", Body: []ast.Node{&ast.ElementNode{Name: "Card"}}},
	}}
	wrote := false

	_, err := GenerateChecked(doc, false, filepath.Join(dir, "page.ktml"), func(string, []byte) {
		wrote = true
	})
	if err == nil {
		t.Fatal("GenerateChecked() error = nil, want invalid import error")
	}
	if wrote {
		t.Error("GenerateChecked() wrote output for invalid imported source")
	}
}
