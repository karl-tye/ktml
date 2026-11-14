package ktml_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/karl-tye/ktml"
)

func TestCSS_GIVENSafeDeclarations_WHENSerialized_THENJoinsThem(t *testing.T) {
	value, err := ktml.CSS(
		ktml.CSSDecl("width", "42.50%"),
		ktml.CSSDecl("--incentive-wheel-offset", " 12.25 "),
		ktml.CSSDecl("stroke-dashoffset", "var(--incentive-wheel-offset)"),
		ktml.CSSDecl("bottom", "calc(12.00% + 0.25rem)"),
		ktml.CSSDecl("transform", "scaleX(var(--performance-scale))"),
		ktml.CSSDecl("color", "#fff !important"),
		ktml.CSSDecl("-webkit-line-clamp", "3"),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "width: 42.50%; --incentive-wheel-offset: 12.25; stroke-dashoffset: var(--incentive-wheel-offset); bottom: calc(12.00% + 0.25rem); transform: scaleX(var(--performance-scale)); color: #fff !important; -webkit-line-clamp: 3"
	if got := value.String(); got != want {
		t.Fatalf("CSS() = %q, want %q", got, want)
	}
}

func TestCSS_GIVENUnsafeDeclarations_WHENSerialized_THENReturnsCSSErrors(t *testing.T) {
	tests := []ktml.CSSDeclaration{
		ktml.CSSDecl("width", "1px; background: red"),
		ktml.CSSDecl("background", "url(javascript:alert(1))"),
		ktml.CSSDecl("background", "URL(x)"),
		ktml.CSSDecl("width", "expression(alert(1))"),
		ktml.CSSDecl("content", `"x"`),
		ktml.CSSDecl("content", "'x'"),
		ktml.CSSDecl("width", `1px\3b`),
		ktml.CSSDecl("width", "1px</style><script>"),
		ktml.CSSDecl("width", "1px}a{b:c"),
		ktml.CSSDecl("width", "calc(1px"),
		ktml.CSSDecl("width", "1px)"),
		ktml.CSSDecl("width", " "),
		ktml.CSSDecl("width:1px;color", "red"),
		ktml.CSSDecl("", "red"),
		ktml.CSSDecl("1width", "red"),
		ktml.CSSDecl("width", "@import"),
		ktml.CSSDecl("width", "1px\nx"),
		ktml.CSSDecl("color", "red /*"),
		ktml.CSSDecl("color", "red*/"),
		ktml.CSSDecl("width", "calc(1px /**/ + 2px)"),
	}
	for _, declaration := range tests {
		value, err := ktml.CSS(declaration)
		var cssError *ktml.CSSError
		if !errors.As(err, &cssError) || value.String() != "" {
			t.Errorf("CSS(%#v) = %q, %v; want a *CSSError", declaration, value.String(), err)
		}
	}
}

func TestCSS_GIVENGridTrackFunctions_WHENSerialized_THENAcceptsThem(t *testing.T) {
	tests := []string{
		"repeat(3, 1fr)",
		"repeat(auto-fill, minmax(12rem, 1fr))",
		"repeat(auto-fit, minmax(min-content, max-content))",
		"repeat(2, 10px 1fr auto)",
		"repeat(4, fit-content(20%))",
		"REPEAT(2, MinMax(0, 1FR))",
		"minmax(100px, 1fr)",
		"minmax(auto, 50%)",
		"12rem minmax(0, 1fr) fit-content(40ch)",
		"repeat(2, 1fr) 2fr",
		"repeat( auto-fill , minmax( 8.5em , 1fr ) )",
		"fit-content(300px)",
	}
	for _, value := range tests {
		got, err := ktml.CSS(ktml.CSSDecl("grid-template-columns", value))
		if err != nil {
			t.Errorf("CSS(%q): %v", value, err)
			continue
		}
		if want := "grid-template-columns: " + value; got.String() != want {
			t.Errorf("CSS(%q) = %q, want %q", value, got.String(), want)
		}
	}
}

func TestCSS_GIVENUnsafeGridTrackFunctions_WHENSerialized_THENReturnsCSSErrors(t *testing.T) {
	tests := []string{
		"repeat(3, url(x))",
		"repeat(url(x), 1fr)",
		"minmax(url(javascript:alert(1)), 1fr)",
		"repeat(2, expression(alert(1)))",
		"minmax(expression(alert(1)), 1fr)",
		`repeat(2, "x")`,
		"repeat(2, 'x')",
		`repeat(2, 1fr\29)`,
		`minmax(1px, \31 fr)`,
		"repeat(3, 1fr",
		"repeat(3, 1fr))",
		"minmax(1px, 1fr",
		"repeat(3, minmax(1px, 1fr)",
		"repeat(3, 1fr); background: red",
		"repeat(3, 1fr)</style><script>",
		"repeat(3, 1fr /**/)",
		"repeat(0, 1fr)",
		"repeat(-1, 1fr)",
		"repeat(1.5, 1fr)",
		"repeat(auto, 1fr)",
		"repeat(3)",
		"repeat(3, )",
		"repeat(, 1fr)",
		"repeat(3, 1fr, 2fr)",
		"repeat(3, repeat(2, 1fr))",
		"minmax(\n1px, 1fr)",
		"repeat(\f3, 1fr)",
		"repeat(3, 1fr\r)",
		"fit-content(\v1px)",
		"minmax(1px)",
		"minmax(1px, 2px, 3px)",
		"minmax(1px, minmax(1px, 2px))",
		"minmax(1px, fit-content(2px))",
		"minmax(var(--x), 1fr)",
		"repeat(3, calc(1px + 2px))",
		"minmax(1foo, 1fr)",
		"minmax(1px 2px, 1fr)",
		"minmax(px, 1fr)",
		"fit-content(1fr)",
		"fit-content(auto)",
		"fit-content(1px, 2px)",
		"repeat(auto-fill, minmax(1px, 1fr)) 1px\n",
		"repeat(3,\n1fr)",
		"repeat(3, 1fr]",
		"repeat(3, 1fr{)",
		"repeat(3, 1fr@)",
	}
	for _, value := range tests {
		got, err := ktml.CSS(ktml.CSSDecl("grid-template-columns", value))
		var cssError *ktml.CSSError
		if !errors.As(err, &cssError) || got.String() != "" {
			t.Errorf("CSS(%q) = %q, %v; want a *CSSError", value, got.String(), err)
		}
	}
}

// FuzzCSS checks that no accepted value can leave its declaration, open a
// comment, or call url() or expression().
func FuzzCSS(f *testing.F) {
	for _, seed := range []string{"repeat(auto-fill, minmax(12rem, 1fr))", "minmax(0, 1fr)", "fit-content(20%)", "calc(1px + 2%)", "repeat(3, url(x))", `repeat(2, "x")`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		got, err := ktml.CSS(ktml.CSSDecl("grid-template-columns", value))
		if err != nil {
			return
		}
		serialized := strings.ToLower(got.String())
		for _, forbidden := range []string{";", "{", "}", "<", ">", `"`, "'", `\`, "/*", "*/", "url(", "expression", "\n", "\r", "\f", "@"} {
			if strings.Contains(serialized, forbidden) {
				t.Fatalf("CSS(%q) = %q contains %q", value, got.String(), forbidden)
			}
		}
	})
}
