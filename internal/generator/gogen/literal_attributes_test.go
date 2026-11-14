package gogen_test

import (
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

func TestGenerate_GIVENLiteralBooleanSpellings_WHENRendered_THENOnlyValuelessAndTypedBooleanAttributesAreBare(t *testing.T) {
	result := compiler.CompileSyntax("link.ktml", []byte(`Link [open: boolean] {
	a hx-push-url="true" aria-hidden="false" data-on=true data-off=false hidden data-open="{open}"
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

func TestGeneratedLink(t *testing.T) {
	for _, tt := range []struct{ open bool; want string }{
		{true, "<a hx-push-url=\"true\" aria-hidden=\"false\" data-on=\"true\" data-off=\"false\" hidden data-open></a>"},
		{false, "<a hx-push-url=\"true\" aria-hidden=\"false\" data-on=\"true\" data-off=\"false\" hidden></a>"},
	} {
		var output strings.Builder
		if err := (Link{Open: tt.open}).Render(context.Background(), &output); err != nil { t.Fatal(err) }
		if got := output.String(); got != tt.want {
			t.Fatalf("open=%v rendered %q, want %q", tt.open, got, tt.want)
		}
	}
}
`)
}
