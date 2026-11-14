package compiler_test

import (
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestWorkspace_GIVENConditionalValuedAttributes_WHENLoaded_THENTypesBranchesWithNarrowing(t *testing.T) {
	snapshot, path := gapWorkspace(t, `Tab [current: boolean, oob: boolean, form?: string, label: string, handler: TrustedScript] {
	a (
		href="/x"
		if current { aria-current="page" class="on" } else { class="off" }
		if form? { form="{form}" } else if oob { hx-swap-oob="true" }
		if current && oob { @click="{handler}" data-label="{label}" }
		if !current { disabled }
	) "{label}"
	option (value="6" if current { selected }) "6"
}
`)
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
}

func TestWorkspace_GIVENInvalidConditionalAttributes_WHENLoaded_THENReportsCodesAtRanges(t *testing.T) {
	tests := []struct {
		name, body, code, span string
	}{
		{name: "non-boolean condition", body: `a (if label { title="x" })`, code: "non-boolean-condition", span: "label"},
		{name: "unnarrowed optional", body: `a (if on { title="{form}" })`, code: "optional-value-access", span: "form"},
		{name: "narrowing ends with the branch", body: `a (if form? { title="x" } else { title="{form}" })`, code: "optional-value-access", span: "form"},
		{name: "script attribute string", body: `a (if on { @click="go('{label}')" })`, code: "script-attribute-value", span: "{label}"},
		{name: "unconditional and conditional", body: `a (class="a" if on { class="b" })`, code: "duplicate-attribute", span: `class="b"`},
		{name: "case-insensitive", body: `a (if on { TITLE="b" } title="a")`, code: "duplicate-attribute", span: `title="a"`},
		{name: "two groups", body: `a (if on { title="a" } if off { title="b" })`, code: "duplicate-attribute", span: `title="b"`},
		{name: "same branch", body: `a (if on { title="a" title="b" })`, code: "duplicate-attribute", span: `title="b"`},
		{name: "id shorthand", body: `a (#x if on { id="y" })`, code: "duplicate-attribute", span: `id="y"`},
		{name: "nested group in one branch", body: `a (if on { title="a" if off { title="b" } })`, code: "duplicate-attribute", span: `title="b"`},
		{name: "conditional call input", body: `Badge (text="a" if on { tone="b" })`, code: "conditional-call-input", span: `if on { tone="b" }`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := "Badge [text: string, tone: string = \"x\"] { span \"{text}\" }\nPage [on: boolean, off: boolean, label: string, form?: string] {\n\t" + test.body + "\n}\n"
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

func TestWorkspace_GIVENSameNameInExclusiveBranches_WHENLoaded_THENHasNoDiagnostics(t *testing.T) {
	snapshot, path := gapWorkspace(t, `Page [on: boolean, off: boolean] {
	a (if on { title="a" } else if off { title="b" } else { title="c" })
	a (if on { if off { title="a" } else { title="b" } })
	a (class="a" class="b")
}
`)
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
}

func TestSemanticTokens_GIVENConditionalAttributes_THENClassifiesKeywordsAndPaths(t *testing.T) {
	source := "Page [on: boolean, note?: string] {\n\ta (if on { title=\"{note}\" } else if !on { data-x }) \"x\"\n}\n"
	want := "parameter*:on,type:boolean,parameter*:note,type:string,keyword:if,parameter:on,parameter:note,keyword:else,keyword:if,operator:!,parameter:on"
	var got []string
	for _, current := range compiler.SemanticTokens([]byte(source)) {
		kind := string(current.Type)
		if current.Declaration {
			kind += "*"
		}
		got = append(got, kind+":"+source[current.Start:current.End])
	}
	if joined := strings.Join(got, ","); joined != want {
		t.Fatalf("tokens = %s\nwant %s", joined, want)
	}
}
