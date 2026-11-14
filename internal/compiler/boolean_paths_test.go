package compiler_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestWorkspace_GIVENBooleanPathsAsText_WHENLoaded_THENReportsBooleanTextAtThePath(t *testing.T) {
	tests := []struct {
		name, body, span string
	}{
		{name: "exact text", body: `p "{on}"`, span: "{on}"},
		{name: "mixed text", body: `p "open: {on}"`, span: "{on}"},
		{name: "record field in text", body: `p "{user.admin}"`, span: "{user.admin}"},
		{name: "mixed attribute value", body: `p title="x {on}"`, span: "{on}"},
		{name: "mixed conditional attribute value", body: `p (if on { title="x {on}" })`, span: "{on}"},
		{name: "mixed string call argument", body: `Fee label="n {on}"`, span: "{on}"},
		{name: "mixed bag entry", body: `Field label="x" extra=(title="x {on}")`, span: "{on}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := forwardingComponents + "Fee [label: string] { span \"{label}\" }\nPage [on: boolean, user: Flags] {\n\t" + test.body + "\n}\n"
			snapshot, path := booleanWorkspace(t, component)
			diagnostics := snapshot.Diagnostics(path)
			if len(diagnostics) != 1 || diagnostics[0].Code != "boolean-text" {
				t.Fatalf("diagnostics = %v, want one boolean-text", codes(diagnostics))
			}
			source := booleanImports + component
			if got := source[diagnostics[0].Start:diagnostics[0].End]; got != test.span {
				t.Fatalf("range = %q, want %q", got, test.span)
			}
			if message := diagnostics[0].Message; !strings.Contains(message, "if ") || !strings.Contains(message, "string") {
				t.Errorf("message = %q, want a hint to use a condition or a presenter string", message)
			}
		})
	}
}

func TestWorkspace_GIVENExactBooleanAttributes_WHENLoaded_THENKeepTheirSemantics(t *testing.T) {
	snapshot, path := booleanWorkspace(t, forwardingComponents+`Toggle [open: boolean] { span }
Page [on: boolean, user: Flags] {
	button (aria-expanded="{on}" disabled="{user.admin}" x-show="{on}" @click="toggle({on})") "x"
	input (type="checkbox" checked="{on}" if on { hidden="{on}" })
	Toggle open="{on}"
	Field label="x" extra=(disabled="{on}")
	if on { p "on" } else { p "off" }
}
`)
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
}

const booleanImports = "import { Flags } from \"./flags.ktmlschema\"\n"

func booleanWorkspace(t *testing.T, component string) (*compiler.Snapshot, string) {
	t.Helper()
	dir := writeWorkspace(t, map[string]string{
		"flags.ktmlschema": "record Flags [\n\tadmin: boolean,\n]\n",
		"card.ktml":        booleanImports + component,
	})
	return loadEntry(t, dir, "card.ktml"), filepath.Join(dir, "card.ktml")
}
