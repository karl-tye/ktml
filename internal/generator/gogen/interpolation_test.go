package gogen_test

import (
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
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

	generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"
)

func TestGeneratedCard(t *testing.T) {
	var output strings.Builder
	err := (Card{Name: "<unsafe>"}).Render(context.Background(), &output)
	if err != nil { t.Fatal(err) }
	if got, want := output.String(), "<p title=\"Welcome, &lt;unsafe&gt;\">Hi &lt;&lt;unsafe&gt;&gt;</p>"; got != want {
		t.Fatalf("rendered output = %q, want %q", got, want)
	}
}
`)
}

func TestGenerate_GIVENInterpolatedComponentInput_WHENRendered_THENPassesStructuralStringWithoutPreEscaping(t *testing.T) {
	result := compiler.CompileSyntax("page.ktml", []byte(`Greeting [label: string] { span { "{label}" } }
Page [name: string] { Greeting label="Hello, {name}!" }`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	page := result.Document.Nodes[1].(*ast.ElementDecl)
	page.Body[0].(*ast.ElementNode).Attributes[0].Value = `"raw value must not be reparsed"`

	generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"
)

func TestGeneratedPage(t *testing.T) {
	var output strings.Builder
	err := (Page{Name: "<unsafe>"}).Render(context.Background(), &output)
	if err != nil { t.Fatal(err) }
	if got, want := output.String(), "<span>Hello, &lt;unsafe&gt;!</span>"; got != want {
		t.Fatalf("rendered output = %q, want %q", got, want)
	}
}
`)
}
