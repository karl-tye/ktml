package gogen_test

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

func TestGenerate_GIVENDoctypeCommentsAndFrameworkNames_WHENRendered_THENWritesThemVerbatim(t *testing.T) {
	generated := generateSource(t, `Layout [title: string, open: boolean] {
	<!-- shell {title} -->
	doctype html
	html lang="en" {
		body x-data @keydown.slash.window="if (a < b) go('x')" hx-ext:inherited="sse" :class="{ open: true }" @dragover.prevent data-open="{open}" {
			<!--
				Mobile menu & <button>
			-->
			button x-on:click.outside="open = false" x-transition.opacity.duration.100ms {
				"{title}"
			}
		}
	}
}`)
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"
)

func TestMarkup(t *testing.T) {
	var output strings.Builder
	if err := (Layout{Title: "<T>", Open: true}).Render(context.Background(), &output); err != nil { t.Fatal(err) }
	want := "<!-- shell {title} --><!doctype html><html lang=\"en\">" +
		"<body x-data @keydown.slash.window=\"if (a &lt; b) go(&#39;x&#39;)\" hx-ext:inherited=\"sse\" :class=\"{ open: true }\" @dragover.prevent data-open>" +
		"<!--\n\t\t\t\tMobile menu & <button>\n\t\t\t-->" +
		"<button x-on:click.outside=\"open = false\" x-transition.opacity.duration.100ms>&lt;T&gt;</button></body></html>"
	if got := output.String(); got != want {
		t.Fatalf("rendered\n%q\nwant\n%q", got, want)
	}
}
`)
	if strings.Count(string(generated), "rt.Raw(") > 4 {
		t.Errorf("static markup is not coalesced:\n%s", generated)
	}
}

func TestGenerate_GIVENRawTextElementContentWithoutSnapshot_WHENGenerated_THENFailsBeforeOutput(t *testing.T) {
	for _, source := range []string{
		"Panel [] {\n\tdiv { style \"a > b { }\" }\n}",
		"Panel [] {\n\tSCRIPT { b \"x\" }\n}",
	} {
		result := compiler.CompileSyntax("input.ktml", []byte(source))
		if result.HasErrors() {
			t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
		}
		generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
		if err == nil || !strings.Contains(err.Error(), "is raw text") {
			t.Fatalf("Generate(%q) = %s, %v; want a raw text error", source, generated, err)
		}
	}
}

func TestGenerate_GIVENRawTextLiterals_WHENRendered_THENWritesScriptAndStyleByteForByte(t *testing.T) {
	generated := generateSource(t, "Page [title: string] {\n\tscript ```\n\tif (a && b < c) { console.log(`${a}<b>&amp;'\"`) }\n\t```\n\tstyle {\n\t\t```a > b { content: \"&\" }```\n\t\t```\nc{}```\n\t}\n\tp \"{title}\"\n}")
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"
)

func TestRawText(t *testing.T) {
	var output strings.Builder
	if err := (Page{Title: "<x>"}).Render(context.Background(), &output); err != nil { t.Fatal(err) }
	want := "<script>\n\tif (a && b < c) { console.log(`+"`"+`${a}<b>&amp;'\"`+"`"+`) }\n\t</script>" +
		"<style>a > b { content: \"&\" }\nc{}</style><p>&lt;x&gt;</p>"
	if got := output.String(); got != want {
		t.Fatalf("rendered\n%q\nwant\n%q", got, want)
	}
}
`)
}

func TestGenerate_GIVENUnsafeRawTextWithoutSnapshot_WHENGenerated_THENFailsBeforeOutput(t *testing.T) {
	for source, want := range map[string]string{
		"Panel [] {\n\tscript ```a = '</script>'```\n}": "cannot contain </script",
		"Panel [] {\n\tstyle ```</STYLE>```\n}":         "cannot contain </STYLE",
		"Panel [] {\n\tdiv ```x```\n}":                  "only the content of a script or style",
		"Panel [] {\n\txmp ```x```\n}":                  "is raw text",
	} {
		result := compiler.CompileSyntax("input.ktml", []byte(source))
		if result.HasErrors() {
			t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
		}
		generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("Generate(%q) = %s, %v; want %s", source, generated, err, want)
		}
	}
}

func TestGenerate_GIVENStarAttributeNames_WHENRendered_THENWritesThemVerbatim(t *testing.T) {
	generated := generateSource(t, `Contractor [target: string] {
	form (hx-target-4*="#errors" hx-target-*="{target}") {}
}`)
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"
)

func TestStar(t *testing.T) {
	var output strings.Builder
	if err := (Contractor{Target: "\"x"}).Render(context.Background(), &output); err != nil { t.Fatal(err) }
	if got, want := output.String(), "<form hx-target-4*=\"#errors\" hx-target-*=\"&#34;x\"></form>"; got != want {
		t.Fatalf("rendered %s, want %s", got, want)
	}
}
`)
}
