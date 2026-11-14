package compiler_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

// gapWorkspace loads one component document beside the payment schema.
func gapWorkspace(t *testing.T, component string) (*compiler.Snapshot, string) {
	t.Helper()
	source := "import { Payment, Money } from \"./payment.ktmlschema\"\n" + component
	dir := writeWorkspace(t, map[string]string{"payment.ktmlschema": paymentSchema, "card.ktml": source})
	return loadEntry(t, dir, "card.ktml"), filepath.Join(dir, "card.ktml")
}

func TestWorkspace_GIVENSiblingComponentCalls_WHENLoaded_THENEachCallIsValidatedAndReferenced(t *testing.T) {
	snapshot, path := gapWorkspace(t, `Fee [label: string] { span "{label}" }
Note [] { p "note" }
Panel [] {
	Fee label="a"
	Note
	Fee label="b"
}
`)
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", codes(diagnostics))
	}
	fee, _ := snapshot.Resolve(path, "Fee")
	note, _ := snapshot.Resolve(path, "Note")
	if got := len(snapshot.References(fee.ID)); got != 2 {
		t.Errorf("Fee references = %d, want 2", got)
	}
	if got := len(snapshot.References(note.ID)); got != 1 {
		t.Errorf("Note references = %d, want 1", got)
	}
}

func TestWorkspace_GIVENAComponentNameAsASameLineAttribute_WHENLoaded_THENItIsAnUnknownInput(t *testing.T) {
	snapshot, path := gapWorkspace(t, `Fee [label: string] { span "{label}" }
Note [] { p "note" }
Panel [] {
	Fee label="a" Note
}
`)
	if got := codes(snapshot.Diagnostics(path)); len(got) != 1 || got[0] != "error:unknown-input" {
		t.Fatalf("diagnostics = %v, want one unknown-input", got)
	}
}

const contentComponents = `Modal [title: string, children: content, footer?: content, actions?: content] {
	section {
		h2 "{title}"
		children
		if footer? {
			footer
		}
		actions
	}
}
Wrapper [] { div { children } }
Plain [label: string] { span "{label}" }
`

func TestWorkspace_GIVENContentInputs_WHENLoaded_THENDeclaresTypedContentAndImplicitChildren(t *testing.T) {
	snapshot, path := gapWorkspace(t, contentComponents+`Page [] {
	Modal title="Pay" {
		p "Choose"
		slot footer {
			button "Pay"
		}
	}
	Wrapper {
		Plain label="x"
	}
}
`)
	if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v %#v", codes(diagnostics), diagnostics)
	}
	modal, _ := snapshot.Resolve(path, "Modal")
	var spelled []string
	for _, input := range modal.Inputs {
		spelled = append(spelled, input.Name+":"+string(input.Mode)+":"+input.Type.String())
	}
	if got := strings.Join(spelled, ","); got != "title:required:string,children:required:content,footer:optional:content,actions:optional:content" {
		t.Errorf("Modal inputs = %s", got)
	}
	wrapper, _ := snapshot.Resolve(path, "Wrapper")
	if len(wrapper.Inputs) != 1 || !wrapper.Inputs[0].Implicit || wrapper.Inputs[0].Mode != compiler.OptionalInput || !wrapper.Inputs[0].IsContent() || !wrapper.AcceptsChildren {
		t.Errorf("Wrapper inputs = %#v", wrapper.Inputs)
	}
	var uses []string
	for _, use := range snapshot.ContentUses(path) {
		uses = append(uses, string(use.Kind)+":"+use.Input)
	}
	if got := strings.Join(uses, ","); got != "outlet:children,outlet:footer,outlet:actions,outlet:children,fill:footer" {
		t.Errorf("content uses = %s", got)
	}
	if references := snapshot.ContentReferences(modal.ID, "footer"); len(references) != 2 {
		t.Errorf("footer references = %#v", references)
	}
}

func TestWorkspace_GIVENInvalidContent_WHENLoaded_THENReportsExactDiagnostics(t *testing.T) {
	tests := []struct {
		name, body, code, marker string
	}{
		{name: "unknown slot", body: `Modal title="x" { p "c"
		slot sidebar {} }`, code: "unknown-slot", marker: "sidebar"},
		{name: "slot of a value input", body: `Modal title="x" { p "c"
		slot title {} }`, code: "unknown-slot", marker: "title"},
		{name: "duplicate slot", body: "Modal title=\"x\" {\n\t\tp \"c\"\n\t\tslot footer {}\n\t\tslot footer {}\n\t}", code: "duplicate-slot", marker: "slot footer"},
		{name: "slot children", body: "Modal title=\"x\" {\n\t\tp \"c\"\n\t\tslot children {}\n\t}", code: "slot-children", marker: "slot children"},
		{name: "missing required children", body: `Modal title="x" {}`, code: "missing-content", marker: "Modal"},
		{name: "missing required children without body", body: `Modal title="x"`, code: "missing-content", marker: "Modal"},
		{name: "unexpected children", body: `Plain label="x" { p "c" }`, code: "unexpected-children", marker: `Plain label="x" { p "c" }`},
		{name: "content as attribute", body: `Modal title="x" footer="{title}" { p "c" }`, code: "content-as-attribute", marker: `footer="{title}"`},
		{name: "slot outside call", body: `div { slot footer {} }`, code: "slot-outside-call", marker: "slot footer"},
		{name: "outlet with attributes", body: `children class="x"`, code: "outlet-with-content", marker: `children class="x"`},
		{name: "interpolated content", body: `p "{children}"`, code: "content-value-access", marker: "{children}"},
		{name: "content field access", body: `p "{children.title}"`, code: "content-value-access", marker: "title"},
		{name: "content as a condition", body: `if children { p "x" }`, code: "non-boolean-condition", marker: "children"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := contentComponents + "Page [children: content] {\n\t" + tt.body + "\n}\n"
			snapshot, path := gapWorkspace(t, source)
			full := "import { Payment, Money } from \"./payment.ktmlschema\"\n" + source
			start := strings.LastIndex(full, tt.marker)
			diagnostics := snapshot.Diagnostics(path)
			if len(diagnostics) != 1 || diagnostics[0].Code != tt.code || diagnostics[0].Start != start || diagnostics[0].End != start+len(tt.marker) {
				t.Errorf("diagnostics = %v %#v, want one %s at %d-%d", codes(diagnostics), diagnostics, tt.code, start, start+len(tt.marker))
			}
		})
	}
}

func TestWorkspace_GIVENInvalidContentDeclarations_WHENLoaded_THENReportsCodes(t *testing.T) {
	tests := []struct {
		name, component, code string
	}{
		{name: "content default", component: `Card [actions: content = "x"] { actions }`, code: "content-default"},
		{name: "content collection", component: `Card [items: [content]] { p }`, code: "content-collection"},
		{name: "children as a value", component: `Card [children: string] { p "{children}" }`, code: "children-not-content"},
		{name: "content selector", component: "Card [children?: content]\nCard [children = Some] { p }\nCard [children = None] { p }", code: "invalid-selector-type"},
		{name: "undeclared children in a default body", component: "Card [on: boolean] default { children }\nCard [on = true] { p }", code: "undeclared-content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot, path := gapWorkspace(t, tt.component+"\n")
			found := false
			for _, diagnostic := range snapshot.Diagnostics(path) {
				found = found || diagnostic.Code == tt.code
			}
			if !found {
				t.Errorf("diagnostics = %v, want %s", codes(snapshot.Diagnostics(path)), tt.code)
			}
		})
	}
}

func TestWorkspace_GIVENAClauseGroupWithDeclaredContent_WHENLoaded_THENEveryClauseMayRenderIt(t *testing.T) {
	snapshot, path := gapWorkspace(t, `Panel [payment: Payment, children?: content, footer?: content]

Panel [payment = Unpaid] { div { children } }

Panel [payment = Pending] { footer }

Panel [payment = Paid] { p "paid" }

Panel [payment = Failed] {}

Page [] {
	Panel payment="{payment}" {
		p "body"
		slot footer {}
	}
}
`)
	var errors []string
	for _, diagnostic := range snapshot.Diagnostics(path) {
		if diagnostic.Code != "unknown-path-root" {
			errors = append(errors, diagnostic.Code)
		}
	}
	if len(errors) != 0 {
		t.Fatalf("diagnostics = %v", codes(snapshot.Diagnostics(path)))
	}
}

const conditionSchema = `record Address [
	city: string,
	verified: boolean,
	note?: string,
]

record User [
	name: string,
	admin: boolean,
	address?: Address,
	tags?: [string],
	flag?: boolean,
]
`

func conditionWorkspace(t *testing.T, body string) (*compiler.Snapshot, string, string) {
	t.Helper()
	source := `import { User } from "./user.ktmlschema"
Badge [label: string] { span "{label}" }
Card [user: User, note?: string, on: boolean, compact: boolean = false, footer?: content] {
` + body + `
}
`
	dir := writeWorkspace(t, map[string]string{"user.ktmlschema": conditionSchema, "card.ktml": source})
	return loadEntry(t, dir, "card.ktml"), filepath.Join(dir, "card.ktml"), source
}

func TestWorkspace_GIVENValidConditions_WHENLoaded_THENTypesAndNarrows(t *testing.T) {
	bodies := []string{
		`if on { p "on" }`,
		`if !on && compact || user.admin { p "x" } else { p "y" }`,
		`if note? { p "{note}" }`,
		`if !note? { p "none" } else { p "{note}" }`,
		`if !(note?) {} else { Badge label="{note}" }`,
		`if note? && on { p "{note}" }`,
		`if user.address? && user.address.verified { p "{user.address.city}" }`,
		`if !user.address? || user.address.verified { p "x" }`,
		`if user.address? { if user.address.note? { p "{user.address.note}" } }`,
		`if user.tags? { for tag in user.tags key tag { p "{tag}" } }`,
		`if user.flag? && user.flag { p "flag" }`,
		`if footer? { div { footer } }`,
		`if note? { p "a" } else if on { p "b" } else { p "c" }`,
		`if (note? && on) || (note? && compact) { p "{note}" }`,
		`if note? { p "{note}" } else if note? { p "never" }`,
		`if note? { if note? { p "{note}" } }`,
	}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			snapshot, path, _ := conditionWorkspace(t, "\t"+body)
			if diagnostics := snapshot.Diagnostics(path); len(diagnostics) != 0 {
				t.Errorf("diagnostics = %v %#v", codes(diagnostics), diagnostics)
			}
		})
	}
}

func TestWorkspace_GIVENInvalidConditions_WHENLoaded_THENReportsExactDiagnostics(t *testing.T) {
	tests := []struct {
		body, code, marker string
	}{
		{body: `if user.name { p }`, code: "non-boolean-condition", marker: "user.name"},
		{body: `if user { p }`, code: "non-boolean-condition", marker: "user {"},
		{body: `if user.tags { p }`, code: "non-boolean-condition", marker: "user.tags"},
		{body: `if note { p }`, code: "non-boolean-condition", marker: "note {"},
		{body: `if user.flag { p }`, code: "optional-value-access", marker: "user.flag {"},
		{body: `if on? { p }`, code: "presence-requires-optional", marker: "on?"},
		{body: `if user.name? { p }`, code: "presence-requires-optional", marker: "user.name?"},
		{body: `if missing { p }`, code: "unknown-path-root", marker: "missing"},
		{body: `if user.address.verified { p }`, code: "optional-value-access", marker: "address"},
		{body: `p "{note}"`, code: "optional-value-access", marker: "note"},
		{body: `if note? || on { p "{note}" }`, code: "optional-value-access", marker: "note}"},
		{body: `if note? { p } else { p "{note}" }`, code: "optional-value-access", marker: "note}"},
		{body: `if on { p } else if note? { p } else { p "{note}" }`, code: "optional-value-access", marker: "note}"},
		{body: `if note? { p }
	p "{note}"`, code: "optional-value-access", marker: "note}"},
		{body: `if on || note.length { p }`, code: "optional-value-access", marker: "note"},
		{body: `for tag in user.tags key tag {}`, code: "optional-collection", marker: "user.tags"},
	}
	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			snapshot, path, source := conditionWorkspace(t, "\t"+tt.body)
			start := strings.LastIndex(source, tt.marker)
			end := start + len(strings.TrimRight(strings.TrimSuffix(strings.TrimSuffix(tt.marker, "}"), " {"), " "))
			diagnostics := snapshot.Diagnostics(path)
			if len(diagnostics) != 1 || diagnostics[0].Code != tt.code || diagnostics[0].Start != start || diagnostics[0].End != end {
				t.Errorf("diagnostics = %v %#v, want one %s at %d-%d", codes(diagnostics), diagnostics, tt.code, start, end)
			}
		})
	}
}
