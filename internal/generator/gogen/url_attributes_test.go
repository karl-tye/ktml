package gogen_test

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

func TestGenerate_GIVENNonURLValueInURLAttribute_WHENGenerated_THENFails(t *testing.T) {
	for _, body := range []string{
		`a href="{label}"`,
		`form action="{label}"`,
		`img SRC="{label}"`,
		`button hx-get="{count}"`,
		`a href="{label}/x"`,
		`a href="/{label}"`,
		`a href="javascript:{label}"`,
		`a href="/x/{on}"`,
	} {
		t.Run(body, func(t *testing.T) {
			result := compiler.CompileSyntax("link.ktml", []byte("Link [label: string, count: integer, on: boolean] {\n\t"+body+"\n}\n"))
			if result.HasErrors() {
				t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
			}
			_, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
			if err == nil || !strings.Contains(err.Error(), "URL attribute") {
				t.Fatalf("Generate() error = %v, want a URL attribute error", err)
			}
		})
	}
}

func TestGenerate_GIVENJavaScriptAfterASafeURLPrefix_WHENRendered_THENStaysInThePath(t *testing.T) {
	result := compiler.CompileSyntax("link.ktml", []byte(`Link [label: string, count: integer] {
	a href="/jobs/{label}/{count}?q={label}" hx-get="https://example.com/{label}" "x"
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
	var output strings.Builder
	if err := (Link{Label: "javascript:alert(\"1\")", Count: 7}).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	want := "<a href=\"/jobs/javascript:alert(&#34;1&#34;)/7?q=javascript:alert(&#34;1&#34;)\" hx-get=\"https://example.com/javascript:alert(&#34;1&#34;)\">x</a>"
	if got := output.String(); got != want {
		t.Fatalf("rendered %q, want %q", got, want)
	}
}
`)
}
