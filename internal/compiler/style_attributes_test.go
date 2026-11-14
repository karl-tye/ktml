package compiler_test

import "testing"

func TestWorkspace_GIVENCSSValueInStyle_WHENLoaded_THENHasNoDiagnostics(t *testing.T) {
	snapshot, path := gapWorkspace(t, `Bar [width: CSSValue, extra: CSSValue, on: boolean] {
	div style="{width}"
	div (STYLE="display: block; {extra}" if on { title="x" })
	div style="color: red"
	div (if on { style="{width}" })
}
`)
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
}

func TestWorkspace_GIVENInvalidStyleValues_WHENLoaded_THENReportsCodesAtRanges(t *testing.T) {
	tests := []struct {
		name, body, code, span string
	}{
		{name: "string in style", body: `div style="width: {label}"`, code: "style-attribute-value", span: "{label}"},
		{name: "number in style", body: `div style="{ratio}"`, code: "style-attribute-value", span: "{ratio}"},
		{name: "string in conditional style", body: `div (if on { style="{label}" })`, code: "style-attribute-value", span: "{label}"},
		{name: "css in title", body: `div title="{css}"`, code: "contextual-type-mismatch", span: "{css}"},
		{name: "css in text", body: `p "{css}"`, code: "contextual-type-mismatch", span: "{css}"},
		{name: "css in a script attribute", body: `div @click="{css}"`, code: "script-attribute-value", span: "{css}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := "Page [on: boolean, label: string, ratio: number, css: CSSValue] {\n\t" + test.body + "\n}\n"
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
