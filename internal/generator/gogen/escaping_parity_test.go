package gogen_test

import (
	"fmt"
	"html/template"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
	"github.com/karl-tye/ktml/internal/plugin/gohtml"
)

func TestGenerate_GIVENAuthoredMarkupInText_WHENRendered_THENMatchesGoHTMLBackend(t *testing.T) {
	result := compiler.CompileSyntax("card.ktml", []byte(`Card [name: string] { "<b>{name}</b>" }`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}

	goHTMLSource := gohtml.Generate(result.Document, false, "card.ktml", nil)
	goHTMLTemplate, err := template.New("generated").Parse(goHTMLSource)
	if err != nil {
		t.Fatalf("parse gohtml template: %v\n%s", err, goHTMLSource)
	}
	var goHTMLOutput strings.Builder
	if err := goHTMLTemplate.ExecuteTemplate(&goHTMLOutput, "Card", struct{ Name string }{Name: "Ada"}); err != nil {
		t.Fatalf("execute gohtml template: %v", err)
	}

	generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	compileGenerated(t, generated, fmt.Sprintf(`package generated

import (
	"context"
	"strings"
	"testing"
)

func TestGeneratedCardMatchesGoHTML(t *testing.T) {
	var output strings.Builder
	if err := (Card{Name: "Ada"}).Render(context.Background(), &output); err != nil { t.Fatal(err) }
	if got, want := output.String(), %q; got != want {
		t.Fatalf("direct Go output = %%q, gohtml output = %%q", got, want)
	}
}
`, strings.TrimSpace(goHTMLOutput.String())))
}
