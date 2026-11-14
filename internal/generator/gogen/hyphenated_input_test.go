package gogen_test

import (
	"go/parser"
	"go/token"
	"html/template"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
	"github.com/karl-tye/ktml/internal/plugin/gohtml"
)

func TestGenerate_GIVENHyphenatedInput_WHENRendered_THENUsesValidGoFieldNameAcrossBackends(t *testing.T) {
	result := compiler.CompileSyntax("card.ktml", []byte(`Card [first-name: string] { "{first-name}" }`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}

	var goHTMLInput []byte
	goHTMLSource := gohtml.Generate(result.Document, false, "card.ktml", func(_ string, content []byte) {
		goHTMLInput = content
	})
	if _, err := parser.ParseFile(token.NewFileSet(), "card.ktml.go", goHTMLInput, parser.AllErrors); err != nil {
		t.Fatalf("generated gohtml input is invalid Go: %v\n%s", err, goHTMLInput)
	}
	if !strings.Contains(string(goHTMLInput), "FirstName string") {
		t.Fatalf("generated gohtml input does not contain FirstName field:\n%s", goHTMLInput)
	}
	goHTMLTemplate, err := template.New("generated").Parse(goHTMLSource)
	if err != nil {
		t.Fatalf("parse gohtml template: %v\n%s", err, goHTMLSource)
	}
	var goHTMLOutput strings.Builder
	if err := goHTMLTemplate.ExecuteTemplate(&goHTMLOutput, "Card", struct{ FirstName string }{FirstName: "Ada"}); err != nil {
		t.Fatalf("execute gohtml template: %v", err)
	}
	if got := strings.TrimSpace(goHTMLOutput.String()); got != "Ada" {
		t.Fatalf("gohtml output = %q, want %q", got, "Ada")
	}

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
	if err := (Card{FirstName: "Ada"}).Render(context.Background(), &output); err != nil { t.Fatal(err) }
	if got := output.String(); got != "Ada" { t.Fatalf("output = %q, want Ada", got) }
}
`)
}
