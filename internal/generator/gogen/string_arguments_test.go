package gogen_test

import (
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

func TestGenerate_GIVENIntegerAndNumberPathsInAStringArgument_WHENRendered_THENFormatsThemAsText(t *testing.T) {
	result := compiler.CompileSyntax("count.ktml", []byte(`Label [value: string] {
	span "{value}"
}

Count [n: integer, ratio: number, name: string] {
	Label value="{n} items, {ratio} of {name}"
}`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
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

func TestGeneratedCount(t *testing.T) {
	var output strings.Builder
	if err := (Count{N: -12, Ratio: 0.25, Name: "<b>"}).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "<span>-12 items, 0.25 of &lt;b&gt;</span>"; got != want {
		t.Fatalf("rendered %q, want %q", got, want)
	}
}
`)
}
