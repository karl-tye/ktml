package compiler_test

import (
	"testing"
)

func TestWorkspace_GIVENContentInsideRawTextElements_WHENLoaded_THENEveryChildIsARawTextBodyError(t *testing.T) {
	tests := []struct {
		name, body, span string
	}{
		{name: "style text", body: "\tstyle \"a > b { color: red }\"\n", span: `"a > b { color: red }"`},
		{name: "upper-case script element", body: "\tsCRIPT { b \"x\" }\n", span: `b "x"`},
		{name: "element in script", body: "\tscript { b \"x\" }\n", span: `b "x"`},
		{name: "comment in style", body: "\tstyle { <!-- x --> }\n", span: `<!-- x -->`},
		{name: "condition in script", body: "\tscript { if on { \"x\" } }\n", span: `if on { "x" }`},
		{name: "outlet in script", body: "\tscript { children }\n", span: `children`},
		{name: "xmp text", body: "\txmp \"a < b\"\n", span: `"a < b"`},
		{name: "iframe text", body: "\tiframe \"a < b\"\n", span: `"a < b"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := "Page [on: boolean, title: string, children?: content] {\n" + test.body + "}\n"
			snapshot, path := gapWorkspace(t, component)
			diagnostics := snapshot.Diagnostics(path)
			if len(diagnostics) != 1 || diagnostics[0].Code != "raw-text-body" {
				t.Fatalf("diagnostics = %v, want one raw-text-body", codes(diagnostics))
			}
			source := "import { Payment, Money } from \"./payment.ktmlschema\"\n" + component
			if got := source[diagnostics[0].Start:diagnostics[0].End]; got != test.span {
				t.Fatalf("range = %q, want %q", got, test.span)
			}
		})
	}
}

func TestWorkspace_GIVENEmptyRawTextElementsAndEscapableText_WHENLoaded_THENHasNoDiagnostics(t *testing.T) {
	snapshot, path := gapWorkspace(t, `Page [title: string] {
	script src="/app.js" defer
	script type="module" src="/m.js" {}
	style
	textarea "a < b && {title}"
	title "{title} & co"
	noscript { p "a < b" }
}
`)
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
}

func TestWorkspace_GIVENRawTextLiteralsInScriptAndStyle_WHENLoaded_THENHasNoDiagnostics(t *testing.T) {
	snapshot, path := gapWorkspace(t, "Page [] {\n\tscript ```\n\tif (a && b < c) { console.log(`${a}</b>`, \"</scrip\") }\n\t```\n\tstyle {\n\t\t```a > b { content: \"&amp;\" }```\n\t}\n\tsCRIPT type=\"module\" ```import x from \"./x.js\"```\n}\n")
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
}

func TestWorkspace_GIVENUnsafeOrMisplacedRawText_WHENLoaded_THENReportsCodesAtRanges(t *testing.T) {
	tests := []struct {
		name, body, code, span string
	}{
		{name: "script end tag", body: "\tscript ```x = \"</script>\"```\n", code: "raw-text-end-tag", span: "```x = \"</script>\"```"},
		{name: "upper-case script end tag", body: "\tscript ```x = \"</SCRIPT \"```\n", code: "raw-text-end-tag", span: "```x = \"</SCRIPT \"```"},
		{name: "script comment opener", body: "\tscript ```x = \"<!--\"```\n", code: "raw-text-end-tag", span: "```x = \"<!--\"```"},
		{name: "style end tag", body: "\tstyle ```a{}</Style>```\n", code: "raw-text-end-tag", span: "```a{}</Style>```"},
		{name: "raw text in a div", body: "\tdiv ```x```\n", code: "raw-text-placement", span: "```x```"},
		{name: "raw text in a body", body: "\t```x```\n", code: "raw-text-placement", span: "```x```"},
		{name: "raw text in a condition", body: "\tif on {\n\t\t```x```\n\t}\n", code: "raw-text-placement", span: "```x```"},
		{name: "raw text in xmp", body: "\txmp ```x```\n", code: "raw-text-body", span: "```x```"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := "Page [on: boolean] {\n" + test.body + "}\n"
			snapshot, path := gapWorkspace(t, component)
			diagnostics := snapshot.Diagnostics(path)
			if len(diagnostics) != 1 || diagnostics[0].Code != test.code {
				t.Fatalf("diagnostics = %v, want one %s", codes(diagnostics), test.code)
			}
			source := "import { Payment, Money } from \"./payment.ktmlschema\"\n" + component
			if got := source[diagnostics[0].Start:diagnostics[0].End]; got != test.span {
				t.Fatalf("range = %q, want %q", got, test.span)
			}
		})
	}
}
