package htmlcheck_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/htmlcheck"
)

const helpers = `Icon [name: string] { i class="{name}" }
Field [label: string, children?: content] {
	label {
		"{label}"
		children
	}
}
Wrap [children: content] { div { children } }
`

type loaded struct {
	snapshot *compiler.Snapshot
	path     string
}

func loadSnapshot(t *testing.T, source string) loaded {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "page.ktml")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := compiler.NewWorkspace(dir).Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return loaded{snapshot, path}
}

func analyze(t *testing.T, source string) (string, []htmlcheck.Diagnostic) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "page.ktml")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := compiler.NewWorkspace(dir).Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, diagnostic := range snapshot.Diagnostics(path) {
		if diagnostic.Severity == compiler.SeverityError {
			t.Fatalf("compiler diagnostic in fixture: %v", diagnostic)
		}
	}
	return path, htmlcheck.Analyze(snapshot, path)
}

func spelled(source string, diagnostics []htmlcheck.Diagnostic) []string {
	var got []string
	for _, diagnostic := range diagnostics {
		got = append(got, diagnostic.Code+":"+source[diagnostic.Start:diagnostic.End])
	}
	sort.Strings(got)
	return got
}

func TestAnalyze_GIVENInvalidHTML_WHENAnalyzed_THENReportsCodesAtRanges(t *testing.T) {
	tests := []struct {
		name, body string
		want       []string
	}{
		{"unknown element", `dvi "x"`, []string{"html-unknown-element:dvi"}},
		{"obsolete element", `center "x"`, []string{"html-obsolete-element:center"}},
		{"unknown attribute", `div clas="x"`, []string{"html-unknown-attribute:clas"}},
		{"element attribute elsewhere", `div href="/x" "x"`, []string{"html-unknown-attribute:href"}},
		{"invalid input type", `input (type="txt" aria-label="x")`, []string{`html-invalid-attribute-value:type="txt"`}},
		{"invalid button type", `button type="sumbit" "Go"`, []string{`html-invalid-attribute-value:type="sumbit"`}},
		{"duplicate attribute", `div class="a" class="b"`, []string{"html-duplicate-attribute:class"}},
		{"duplicate id", "div #main\n\tsection id=\"main\"", []string{`html-duplicate-id:id="main"`}},
		{"li outside a list", `div { li "x" }`, []string{"html-invalid-nesting:li"}},
		{"div in ul", `ul { div "x" }`, []string{"html-invalid-nesting:div"}},
		{"span in tr", `table { tr { span "x" } }`, []string{"html-invalid-nesting:span"}},
		{"text in table", `table { "x" }`, []string{`html-invalid-nesting:"x"`}},
		{"div in p", `p { div "x" }`, []string{"html-invalid-nesting:div"}},
		{"div in p through a link", `p { a href="/x" { div "x" } }`, []string{"html-invalid-nesting:div"}},
		{"form in form", `form { div { form {} } }`, []string{"html-invalid-nesting:form"}},
		{"img without alt", `img src="/a.png"`, []string{"a11y-missing-alt:img"}},
		{"input image without alt", `input type="image" src="/a.png"`, []string{"a11y-missing-alt:input"}},
		{"unlabelled input in a div", `div { input type="text" name="q" }`, []string{"a11y-missing-label:input"}},
		{"unlabelled select in a form", `form { select name="s" { option "a" } }`, []string{"a11y-missing-label:select"}},
		{"label for another id", "div {\n\tlabel for=\"other\" \"Name\"\n\tinput id=\"name\"\n}", []string{"a11y-missing-label:input"}},
		{"empty button", `button type="button"`, []string{"a11y-missing-name:button"}},
		{"icon-only button", `button { i class="fas fa-x" }`, []string{"a11y-missing-name:button"}},
		{"svg-only link", `a href="/x" { svg { path d="M0" } }`, []string{"a11y-missing-name:a"}},
		{"hidden text only", `button { span aria-hidden="true" "x" }`, []string{"a11y-missing-name:button"}},
		{"input button without value", `input type="button"`, []string{"a11y-missing-name:input"}},
		{"unknown aria", `div aria-labeledby="x"`, []string{"a11y-unknown-aria:aria-labeledby"}},
		{"invalid aria value", `div aria-hidden="yes"`, []string{`a11y-invalid-aria-value:aria-hidden="yes"`}},
		{"invalid aria live", `div aria-live="loud"`, []string{`a11y-invalid-aria-value:aria-live="loud"`}},
		{"invalid aria integer", `div (role="heading" aria-level="two")`, []string{`a11y-invalid-aria-value:aria-level="two"`}},
		// aria-* booleans render true or false (ADR-0001), so they are fine.
		{"aria boolean", `button (aria-expanded="{on}" type="button") "Menu"`, nil},
		{"boolean in a text aria attribute", `button (aria-label="{on}" type="button") "Menu"`, []string{`a11y-invalid-aria-value:aria-label="{on}"`}},
		{"draggable boolean presence", `div draggable="{on}"`, []string{`html-boolean-presence:draggable="{on}"`}},
		{"unknown role", `div role="buton"`, []string{`a11y-invalid-role:role="buton"`}},
		{"abstract role", `div role="widget"`, []string{`a11y-invalid-role:role="widget"`}},
		{"role on a button", `button (type="button" role="heading") "x"`, []string{`a11y-role-not-allowed:role="heading"`}},
		{"role on a label", `label role="button" "x"`, []string{`a11y-role-not-allowed:role="button"`}},
		{"heading jump", "h1 \"a\"\nh3 \"b\"", []string{"a11y-heading-order:h3"}},
		{"positive tabindex", `div tabindex="2"`, []string{`a11y-positive-tabindex:tabindex="2"`}},
		{"button in a link", `a href="/x" { button type="button" "x" }`, []string{"a11y-nested-interactive:button"}},
		{"link in a button", `button type="button" { a href="/x" "x" }`, []string{"a11y-nested-interactive:a"}},
		{"input in a link", `a href="/x" { input type="checkbox" aria-label="x" }`, []string{"a11y-nested-interactive:input"}},
		{"tabindex in a button", `button type="button" { span tabindex="0" "x" }`, []string{"a11y-nested-interactive:span"}},
		{"conditional attribute", `div (if on { aria-hidden="yes" })`, []string{`a11y-invalid-aria-value:aria-hidden="yes"`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := helpers + "Page [on: boolean, name: string] {\n\t" + strings.ReplaceAll(test.body, "\n", "\n\t") + "\n}\n"
			_, diagnostics := analyze(t, source)
			got := strings.Join(spelled(source, diagnostics), "\n")
			if want := strings.Join(test.want, "\n"); got != want {
				t.Errorf("diagnostics =\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestAnalyze_GIVENWhatCannotBeKnownStatically_WHENAnalyzed_THENStaysSilent(t *testing.T) {
	tests := []struct{ name, body string }{
		{"valid document", "main {\n\th1 \"Title\"\n\tp { a href=\"/x\" \"link\" }\n\tul { li \"a\" }\n\ttable { tr { td \"x\" } }\n}"},
		{"framework attributes", `div (x-data=` + "`{ open: false }`" + ` @click="open = true" :class="open" hx-get="/x" data-id="1" x-show="open")`},
		{"event handlers", `button (type="button" onclick="go()") "Go"`},
		{"custom elements", `my-widget foo="bar" { li "x" }`},
		{"svg vocabulary", `svg viewBox="0 0 1 1" { path (stroke-linecap="round" d="M0") }`},
		{"li at the component root", `li "x"`},
		{"component in a list", `ul { Icon name="x" }`},
		{"td in a component body", `Wrap { td "x" }`},
		{"input wrapped by a label", `label { span "Name" input type="text" }`},
		{"input wrapped by a label through a div", `label { div { input type="text" } }`},
		{"input in a Field slot", `Field label="Name" { input type="text" }`},
		{"input at the root", `input type="text"`},
		{"input in a span", `span { input type="text" }`},
		{"input labelled by for", "div {\n\tlabel for=\"q\" \"Search\"\n\tinput (id=\"q\" type=\"search\")\n}"},
		{"input with a dynamic id", `div { input (id="{name}" type="text") }`},
		{"input with placeholder", `div { input (type="text" placeholder="Search") }`},
		{"hidden input", `div { input (type="hidden" name="x" value="1") }`},
		{"submit input", `div { input type="submit" }`},
		{"input with a dynamic type", `div { input type="{name}" }`},
		{"button with text", `button type="button" "Save"`},
		{"button with a path", `button type="button" "{name}"`},
		{"button with an alt image", `button { img (src="/x.png" alt="Close") }`},
		{"button with svg title", `button { svg { title "Close" } }`},
		{"button with a component", `button { Icon name="x" }`},
		{"button with a slot outlet", `Wrap { button { span "x" } }`},
		{"button with x-text", `button x-text="label"`},
		{"button with aria-label", `button aria-label="Close" { i class="fas fa-x" }`},
		{"button with a spread", `button (type="button" ...attrs)`},
		{"button with a conditional label", `button { if on { "Open" } else { i } }`},
		{"button with a sr-only label", "button {\n\ti class=\"x\"\n\tspan class=\"sr-only\" \"Close\"\n}"},
		{"img with dynamic alt", `img (src="/x.png" alt="{name}")`},
		{"img decorative", `img (src="/x.png" alt="")`},
		{"img presentation", `img (src="/x.png" role="presentation")`},
		{"img bound alt", `img (src="/x.png" :alt="label")`},
		{"img with a spread", `img (src="/x.png" ...attrs)`},
		{"aria literal true", `button (type="button" aria-expanded="true") "x"`},
		{"aria dynamic string", `div aria-label="{name}"`},
		{"aria bound", `div :aria-expanded="open"`},
		{"hidden presence", `div hidden="{on}"`},
		{"ids in exclusive branches", "if on { div #a } else { div #a }"},
		{"id in a repetition", "for item in items key item {\n\tdiv #row\n}"},
		{"ids in slots", "Wrap { div #a }\ndiv #a"},
		{"heading order across branches", "h1 \"a\"\nif on { h2 \"b\" } else { h2 \"c\" }\nh3 \"d\""},
		{"heading in a slot", "h1 \"a\"\nWrap { h4 \"b\" }"},
		{"first heading", `h3 "a"`},
		{"tabindex zero", `div tabindex="0"`},
		{"link inside a button-like div", `div role="button" { a href="/x" "x" }`},
		{"a without href in a button", `button type="button" { a "x" }`},
		{"role on a div", `div role="dialog"`},
		{"implicit role on main", `main role="main"`},
		{"role on a dynamic input type", `input (type="{name}" role="switch" aria-label="x")`},
		{"enumerated dynamic value", `input (type="{name}" aria-label="x")`},
		{"valid enumerated", `form (method="POST" enctype="multipart/form-data") { button type="submit" "Go" }`},
		{"tolerated presentational attribute", `table (border="0" cellpadding="2") { tr { td "x" } }`},
		{"rdfa", `meta (property="og:title" content="x")`},
		{"text in a td", `table { tr { td "x" } }`},
		{"slot outlets", `Wrap { children }`},
		{"li in an x-for template", "ul {\n\ttemplate x-for=\"item in items\" { li x-text=\"item\" }\n}"},
		{"tr in an x-if template", "table { tbody {\n\ttemplate x-if=\"open\" { tr { td \"x\" } }\n} }"},
		{"class-hidden file input", `div { input (type="file" class="hidden" x-ref="upload") }`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := strings.ReplaceAll(test.body, "\n", "\n\t")
			source := helpers + "Page [on: boolean, name: string, items: [string], attrs: Attributes, children?: content] {\n\t" + body + "\n}\n"
			_, diagnostics := analyze(t, source)
			if got := spelled(source, diagnostics); len(got) != 0 {
				t.Errorf("diagnostics = %v, want none", got)
			}
		})
	}
}

func TestAnalyze_GIVENControlsInCalledComponents_WHENAnalyzed_THENTheFirstIsLabelledOnlyWhenNoCallerCanWrapIt(t *testing.T) {
	tests := []struct {
		name, caller string
		want         []string
	}{
		{"called inside a label", "label { \"Amount\" Money }", nil},
		{"called inside slot content", "Field label=\"Amount\" { Money }", nil},
		{"called inside a div", "div { Money }", []string{"a11y-missing-label:input"}},
		{"called at the root of a page", "Money", []string{"a11y-missing-label:input"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := helpers + "Money [] {\n\tdiv { input type=\"number\" }\n}\nPage [] {\n\t" + test.caller + "\n}\n"
			_, diagnostics := analyze(t, source)
			if got, want := strings.Join(spelled(source, diagnostics), ","), strings.Join(test.want, ","); got != want {
				t.Errorf("diagnostics = %s, want %s", got, want)
			}
		})
	}
	source := helpers + "Pair [] {\n\tdiv {\n\t\tinput type=\"text\"\n\t\tselect { option \"a\" }\n\t}\n}\nPage [] {\n\tlabel { Pair }\n}\n"
	_, diagnostics := analyze(t, source)
	if got := strings.Join(spelled(source, diagnostics), ","); got != "a11y-missing-label:select" {
		t.Errorf("a wrapping label labels only the first control: %s", got)
	}
}

func TestAnalyze_GIVENFixableDiagnostics_WHENFixed_THENTheEditsProduceTheIntendedSource(t *testing.T) {
	tests := []struct{ name, body, code, want string }{
		{"tabindex", `div tabindex="3"`, "a11y-positive-tabindex", `div tabindex="0"`},
		{"aria typo", `div aria-labeledby="x"`, "a11y-unknown-aria", `div aria-labelledby="x"`},
		{"element typo", `spna "x"`, "html-unknown-element", `span "x"`},
		{"attribute typo", `div clas="x"`, "html-unknown-attribute", `div class="x"`},
		{"decorative image", `img src="/a.png"`, "a11y-missing-alt", `img alt="" src="/a.png"`},
		{"decorative image in a group", `img (src="/a.png")`, "a11y-missing-alt", `img (alt="" src="/a.png")`},
		{"duplicate attribute", `div class="a" class="b"`, "html-duplicate-attribute", `div class="a"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prefix := "Page [] {\n\t"
			source := prefix + test.body + "\n}\n"
			_, diagnostics := analyze(t, source)
			if len(diagnostics) != 1 || diagnostics[0].Code != test.code || len(diagnostics[0].Fixes) != 1 {
				t.Fatalf("diagnostics = %v", diagnostics)
			}
			edited := source
			edits := diagnostics[0].Fixes[0].Edits
			for index := len(edits) - 1; index >= 0; index-- {
				edit := edits[index]
				edited = edited[:edit.Start] + edit.Text + edited[edit.End:]
			}
			if got := strings.TrimSuffix(strings.TrimPrefix(edited, prefix), "\n}\n"); got != test.want {
				t.Errorf("fixed = %q, want %q", got, test.want)
			}
			_, after := analyze(t, edited)
			if len(after) != 0 {
				t.Errorf("after the fix: %v", spelled(edited, after))
			}
		})
	}
}
