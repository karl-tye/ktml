package gogen_test

import "testing"

func TestGenerate_GIVENCSSValueInStyle_WHENRendered_THENEscapesTheSerializedCSS(t *testing.T) {
	generated := generateWorkspaceSource(t, `Bar [width: CSSValue, extra: CSSValue, hidden: boolean] {
	div style="{width}"
	div (style="display: block; {extra}" if hidden { title="h" })
	div (if hidden { style="display: none" } else { style="{width}" })
}`)
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
)

func TestStyle(t *testing.T) {
	bar := Bar{
		Width: ktml.MustCSS(ktml.CSSDecl("width", "42.50%")),
		Extra: ktml.TrustCSS("content: \"</style><b>\"; a: 'b' & c"),
	}
	var output strings.Builder
	if err := bar.Render(context.Background(), &output); err != nil { t.Fatal(err) }
	want := "<div style=\"width: 42.50%\"></div>" +
		"<div style=\"display: block; content: &#34;&lt;/style&gt;&lt;b&gt;&#34;; a: &#39;b&#39; &amp; c\"></div>" +
		"<div style=\"width: 42.50%\"></div>"
	if got := output.String(); got != want {
		t.Fatalf("rendered\n%s\nwant\n%s", got, want)
	}
}
`)
}
