package gohtml

import (
	"html/template"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

func TestGenerate_GIVENCanonicalQuotedSegments_WHENRendered_THENUsesStructureAndPreservesEscaping(t *testing.T) {
	result := compiler.CompileSyntax("card.ktml", []byte(`Card [name: string] { p title="Welcome, {name}" { "Hi <{name}>" } }`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	declaration := result.Document.Nodes[0].(*ast.ElementDecl)
	element := declaration.Body[0].(*ast.ElementNode)
	element.Attributes[0].Value = `"raw attribute must not be reparsed"`
	element.Children[0].(*ast.TextNode).Value = `"raw text must not be reparsed"`

	generated := Generate(result.Document, false, "card.ktml", nil)
	parsed, err := template.New("generated").Parse(generated)
	if err != nil {
		t.Fatalf("parse generated template: %v\n%s", err, generated)
	}
	var output strings.Builder
	if err := parsed.ExecuteTemplate(&output, "Card", struct{ Name string }{Name: "<unsafe>"}); err != nil {
		t.Fatalf("execute generated template: %v", err)
	}
	if got := output.String(); !strings.Contains(got, `title="Welcome, &lt;unsafe&gt;"`) || !strings.Contains(got, `Hi &lt;&lt;unsafe&gt;&gt;`) {
		t.Errorf("rendered output = %q, want structurally interpolated escaped values", got)
	}
}
