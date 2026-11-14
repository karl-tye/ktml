package gogen_test

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

// generateSource compiles one KTML document and returns its production Go.
func generateSource(t *testing.T, source string) []byte {
	t.Helper()
	result := compiler.CompileSyntax("input.ktml", []byte(source))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	return generated
}

func TestGenerate_GIVENInterpolatedAttributeValue_WHENGenerated_THENUsesAttributeEscapers(t *testing.T) {
	generated := string(generateSource(t, `Card [name: string, count: integer, ratio: number] {
	div title="Hi {name}" data-count="n{count}" data-ratio="r{ratio}" "{name}"
}`))

	for _, want := range []string{"rt.Attr(w, props.Name)", "rt.AttrInt(w, props.Count)", "rt.AttrNumber(w, props.Ratio)", "rt.Text(w, props.Name)"} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated source lacks %s:\n%s", want, generated)
		}
	}
	for _, unwanted := range []string{"rt.TextInt", "rt.TextNumber"} {
		if strings.Contains(generated, unwanted) {
			t.Errorf("attribute values use the text escaper %s:\n%s", unwanted, generated)
		}
	}
}

// TestGenerate_GIVENAdversarialConstantAttributeValues_WHENRendered_THENEscapesThemWithoutPreEscaping
// proves that authors write constant attribute values as plain text: the
// generator escapes &, ', <, >, and " in them with the rt.Attr rules, an
// entity spelled in source stays text, and runtime values in the same
// attribute are escaped by the attribute escaper.
func TestGenerate_GIVENAdversarialConstantAttributeValues_WHENRendered_THENEscapesThemWithoutPreEscaping(t *testing.T) {
	generated := generateSource(t, `Card [name: string] {
	div (
		x-data="{ open: false, label: 'a<b>' }"
		onclick="if (a < b && c > d) go('x')"
		title="say \"hi\" & 'bye'"
		data-entity="&amp; &#39; &lt;"
		data-raw=`+"`"+`"><script>alert(1)</script>`+"`"+`
		data-mixed="pre'{name}'post & more"
		data-close="&quot;>"
	) {}
}

Wrap [name: string] {
	Field label="<i>'quoted'</i> & \"double\"" value="{name}"
}

Field [label: string, value: string] {
	input aria-label="{label}" value="{value}"
}`)
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"
)

func TestGeneratedEscaping(t *testing.T) {
	var card strings.Builder
	if err := (Card{Name: "\"><img src=x onerror=alert(1)>'"}).Render(context.Background(), &card); err != nil { t.Fatal(err) }
	wantCard := "<div" +
		" x-data=\"{ open: false, label: &#39;a&lt;b&gt;&#39; }\"" +
		" onclick=\"if (a &lt; b &amp;&amp; c &gt; d) go(&#39;x&#39;)\"" +
		" title=\"say &#34;hi&#34; &amp; &#39;bye&#39;\"" +
		" data-entity=\"&amp;amp; &amp;#39; &amp;lt;\"" +
		" data-raw=\"&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;\"" +
		" data-mixed=\"pre&#39;&#34;&gt;&lt;img src=x onerror=alert(1)&gt;&#39;&#39;post &amp; more\"" +
		" data-close=\"&amp;quot;&gt;\"" +
		"></div>"
	if got := card.String(); got != wantCard {
		t.Fatalf("Card rendered\n%s\nwant\n%s", got, wantCard)
	}

	var wrap strings.Builder
	if err := (Wrap{Name: "</input>&"}).Render(context.Background(), &wrap); err != nil { t.Fatal(err) }
	wantWrap := "<input aria-label=\"&lt;i&gt;&#39;quoted&#39;&lt;/i&gt; &amp; &#34;double&#34;\" value=\"&lt;/input&gt;&amp;\">"
	if got := wrap.String(); got != wantWrap {
		t.Fatalf("Wrap rendered\n%s\nwant\n%s", got, wantWrap)
	}
	for _, rendered := range []string{card.String(), wrap.String()} {
		if strings.Contains(rendered, "<script") || strings.Contains(rendered, "<img") || strings.Contains(rendered, "'") {
			t.Fatalf("unescaped markup in %s", rendered)
		}
	}
}
`)
}
