package compiler_test

import "testing"

const forwardingComponents = `Field [label: string, attrs?: Attributes, extra: Attributes] {
	label {
		"{label}"
		input (type="text" if attrs? { ...attrs } else { ...extra })
	}
}
`

func TestWorkspace_GIVENSpreadsAndBagLiterals_WHENLoaded_THENHasNoDiagnostics(t *testing.T) {
	snapshot, path := gapWorkspace(t, forwardingComponents+`Form [busy: boolean, mine: Attributes, handler: TrustedScript, data: JSONValue, href: URL, note?: string] {
	Field label="a" extra="{mine}"
	Field (
		label="b"
		attrs=(required hx-post="/x" @click="{handler}" :disabled="{busy}" x-data="{data}" hx-get="{href}" @keyup="go()" style="display: none" if busy { disabled } else { data-idle } ...mine)
		extra=()
	)
	if note? {
		Field label="c" extra=(title="{note}")
	}
}
`)
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
}

func TestWorkspace_GIVENInvalidSpreadsAndBags_WHENLoaded_THENReportsCodesAtRanges(t *testing.T) {
	tests := []struct {
		name, body, code, span string
	}{
		{name: "unnarrowed optional spread", body: `div ...maybe`, code: "optional-value-access", span: "maybe"},
		{name: "spread of a string", body: `div ...label`, code: "spread-type-mismatch", span: "label"},
		{name: "two spreads", body: `div (...mine if on { ...mine })`, code: "multiple-spreads", span: "...mine"},
		{name: "spread on a call", body: `Field label="x" extra="{mine}" ...mine`, code: "spread-call-input", span: "...mine"},
		{name: "bag on an element", body: `div data=(a)`, code: "attribute-bag-placement", span: "(a)"},
		{name: "bag for a string input", body: `Field label=(a) extra="{mine}"`, code: "attribute-bag-placement", span: "(a)"},
		{name: "string in a bag script attribute", body: `Field label="x" extra=(@click="{label}")`, code: "script-attribute-value", span: "{label}"},
		{name: "mixed script value in a bag", body: `Field label="x" extra=(@click="go({on})")`, code: "attribute-bag-value", span: `@click="go({on})"`},
		{name: "duplicate in a bag", body: `Field label="x" extra=(title="a" TITLE="b")`, code: "duplicate-attribute", span: `TITLE="b"`},
		{name: "attributes interpolated", body: `div title="{mine}"`, code: "contextual-type-mismatch", span: "{mine}"},
		{name: "attributes as text", body: `p "{mine}"`, code: "contextual-type-mismatch", span: "{mine}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := forwardingComponents + "Page [on: boolean, label: string, mine: Attributes, maybe?: Attributes] {\n\t" + test.body + "\n}\n"
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
