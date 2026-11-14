package compiler_test

import "testing"

func TestWorkspace_GIVENScriptBodiesWithDataPaths_WHENLoaded_THENHasNoDiagnostics(t *testing.T) {
	snapshot, path := gapWorkspace(t, `Chart [id: JSONValue, data: JSONValue, boot: TrustedScript, debug: boolean] {
	script type="application/json" id="chart-data" "{data}"
	script "createChart({id}, {data});"
	script "window.debug = {debug}; {boot}"
	script "const list = [{id}, 2]; if (a) go({data});"
	sCRIPT "if (a < b) { go() }"
}
`)
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
}

func TestWorkspace_GIVENUnsafeScriptBodies_WHENLoaded_THENReportsCodesAtRanges(t *testing.T) {
	tests := []struct {
		name, body, code, span string
	}{
		{name: "string path", body: `script "go({label})"`, code: "script-body-value", span: "{label}"},
		{name: "number path", body: `script "go({ratio})"`, code: "script-body-value", span: "{ratio}"},
		{name: "url path", body: `script "go({href})"`, code: "script-body-value", span: "{href}"},
		{name: "inside a string", body: `script "go('{data}')"`, code: "script-interpolation-position", span: "{data}"},
		{name: "inside a template", body: "script \"go(`${ a } {data}`)\"", code: "script-interpolation-position", span: "{data}"},
		{name: "inside a comment", body: `script "go() // {data}"`, code: "script-interpolation-position", span: "{data}"},
		{name: "inside a regex", body: `script "x = /a{data}/"`, code: "script-interpolation-position", span: "{data}"},
		{name: "after an uncertain slash", body: `script "if (a) /x/.test(b); go({data})"`, code: "script-interpolation-position", span: "{data}"},
		{name: "end tag in static text", body: `script "go('</script>', {data})"`, code: "raw-text-end-tag", span: `go('</script>', `},
		{name: "comment opener in static text", body: `script "x = '<!--'"`, code: "raw-text-end-tag", span: `x = '<!--'`},
		{name: "trusted script after <", body: `script "a <{boot}"`, code: "script-body-adjacency", span: "{boot}"},
		{name: "trusted script before /", body: `script "{boot}/x"`, code: "script-body-adjacency", span: "{boot}"},
		{name: "trusted script before -", body: `script "{boot}-1"`, code: "script-body-adjacency", span: "{boot}"},
		{name: "text in style", body: `style "a { }"`, code: "raw-text-body", span: `"a { }"`},
		{name: "attribute string position", body: `button @click="go('{data}')"`, code: "script-interpolation-position", span: "{data}"},
		{name: "attribute template position", body: "button x-init=\"go(`${ a } {data}`)\"", code: "script-interpolation-position", span: "{data}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := "Page [label: string, ratio: number, href: URL, data: JSONValue, boot: TrustedScript] {\n\t" + test.body + "\n}\n"
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
