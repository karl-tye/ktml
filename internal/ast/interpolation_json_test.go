package ast_test

import (
	"encoding/json"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

func TestDocumentJSON_GIVENCanonicalInterpolation_WHENRoundTrippedForPlugin_THENPreservesStructureAndLegacyRawValue(t *testing.T) {
	result := compiler.CompileSyntax("card.ktml", []byte(`Card [name: string] { "Hi {name}" }`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	encoded, err := json.Marshal(result.Document)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var document ast.Document
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	text := document.Nodes[0].(*ast.ElementDecl).Body[0].(*ast.TextNode)
	if text.Value != `"Hi {name}"` {
		t.Errorf("legacy raw value = %q", text.Value)
	}
	if text.Quoted == nil || len(text.Quoted.Segments) != 2 || text.Quoted.Segments[1].Path.String() != "name" {
		t.Errorf("canonical quoted structure = %#v", text.Quoted)
	}
	if text.Quoted.Segments[1].Path.P != 26 || text.Quoted.Segments[1].Path.E != 32 {
		t.Errorf("round-tripped path range = %#v", text.Quoted.Segments[1].Path)
	}
}
