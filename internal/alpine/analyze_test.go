package alpine_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/alpine"
	"github.com/karl-tye/ktml/internal/compiler"
)

const alpineHelpers = "Panel [children: content] { div { children } }\n"

func analyzeAlpine(t *testing.T, source string) *alpine.Document {
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
	return alpine.Analyze(snapshot.Source(path), snapshot.Document(path), alpine.SnapshotEnv(snapshot, path))
}

func page(body string) string {
	return alpineHelpers + "Page [on: boolean, data: JSONValue] {\n\t" + strings.ReplaceAll(body, "\n", "\n\t") + "\n}\n"
}

func TestAnalyze_GIVENAlpineMistakes_WHENAnalyzed_THENReportsCodesAtRanges(t *testing.T) {
	tests := []struct {
		name, body string
		want       []string
	}{
		{"syntax error", `div (x-data=` + "`{ open: false }`" + ` @click="open = ")`, []string{`alpine-syntax:"`}},
		{"syntax error in escaped x-data", `div x-data="{ open: false, { }"`, []string{`alpine-syntax:{`}},
		{"x-for off a template", `div x-for="item in items"`, []string{"alpine-directive-placement:x-for"}},
		{"x-if off a template", `div x-if="open"`, []string{"alpine-directive-placement:x-if"}},
		{"x-if with x-for", `template (x-for="item in items" x-if="item") { li }`, []string{"alpine-directive-combination:x-if"}},
		{"two template roots", "template x-for=\"item in items\" {\n\tli\n\tli\n}", []string{"alpine-template-root:template"}},
		{"key off x-for", `div :key="id"`, []string{"alpine-directive-combination::key"}},
		{"event-less x-on", `div x-on="go()"`, []string{"alpine-directive-placement:x-on"}},
		{"directive typo", `div x-sow="open"`, []string{"alpine-unknown-directive:x-sow"}},
		{"modifier typo", `form @submit.prevnt="go()"`, []string{"alpine-unknown-modifier:@submit.prevnt"}},
		{"model modifier typo", `input (x-model.lazzy="q" aria-label="q")`, []string{"alpine-unknown-modifier:x-model.lazzy"}},
		{"$event outside a handler", `div x-show="$event"`, []string{"alpine-scope-reference:$event"}},
		{"missing this in a method", "div x-data=`{ open: false, toggle() { open = !open } }`",
			[]string{"alpine-missing-this:open", "alpine-missing-this:open"}},
		{"unknown ref", "div x-data=`{ go() { this.$refs.input.focus() } }` {\n\tinput (x-ref=\"field\" aria-label=\"x\")\n}", []string{"alpine-unknown-ref:input"}},
		{"sibling scope", "div x-data=`{ open: false }`\nbutton (type=\"button\" @click=\"open = true\") \"x\"", []string{"alpine-out-of-scope:open"}},
		{"x-for item after its loop", "ul x-data=`{ items: [] }` {\n\ttemplate x-for=\"item in items\" { li x-text=\"item\" }\n\tli x-text=\"item\"\n}", []string{"alpine-out-of-scope:item"}},
		{"outerHTML swap of x-data", "div (\n\tx-data=`{ open: false }`\n\thx-get=\"/x\"\n\thx-trigger=\"every 2s\"\n\thx-swap=\"outerHTML\"\n) {\n\tbutton (type=\"button\" @click=\"open = !open\") \"x\"\n}", []string{"alpine-swap-resets-state:x-data"}},
		{"outerHTML swap of x-model state", "form (\n\tx-data=`{ q: '' }`\n\thx-post=\"/x\"\n\thx-swap=\"outerHTML\"\n) {\n\tinput (x-model=\"q\" aria-label=\"q\")\n}", []string{"alpine-swap-resets-state:x-data"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := page(test.body)
			document := analyzeAlpine(t, source)
			var got []string
			for _, diagnostic := range document.Diagnostics {
				got = append(got, diagnostic.Code+":"+source[diagnostic.Start:diagnostic.End])
			}
			sort.Strings(got)
			if joined, want := strings.Join(got, "\n"), strings.Join(test.want, "\n"); joined != want {
				t.Errorf("diagnostics =\n%s\nwant\n%s", joined, want)
			}
		})
	}
}

func TestAnalyze_GIVENWhatCannotBeKnown_WHENAnalyzed_THENStaysSilent(t *testing.T) {
	tests := []struct{ name, body string }{
		{"outer scope names", `button (type="button" @click="open = !open" x-text="label") "x"`},
		{"globals and magics", "div x-data=`{ go() { fetch('/x'); this.$dispatch('done'); console.log(window.location) } }` x-init=\"$watch('x', v => v)\""},
		{"names in a method's own scope", "div x-data=`{ open: false, toggle(open) { let x = open; this.open = x } }`"},
		{"arrow parameters", "div x-data=`{ items: [], go() { this.items.forEach(item => item.done = true) } }`"},
		{"x-for bindings", "ul x-data=`{ items: [] }` {\n\ttemplate (x-for=\"(item, index) in items\" :key=\"item.id\") {\n\t\tli x-text=\"index + item.name\"\n\t}\n}"},
		{"opaque x-data", `div (x-data="dropdown()" @click="open = true")`},
		{"x-data from Go", `div (x-data="{data}" x-show="open")`},
		{"x-data boolean from KTML", "div (x-data=\"\\u007b open: {on} \\u007d\" x-show=\"open\")"},
		{"ref through an unknown subtree", "div x-data=`{ go() { this.$refs.slot.focus() } }` {\n\tPanel { span x-ref=\"x\" }\n}"},
		{"ref found", "div x-data=`{ go() { this.$refs.field.focus() } }` {\n\tinput (x-ref=\"field\" aria-label=\"x\")\n}"},
		{"ref at the root", `button (type="button" @click="$refs.menu.focus()") "x"`},
		{"dynamic ref", "div x-data=`{ go() { this.$refs.a.focus() } }` {\n\tdiv x-ref=\"{data}\"\n}"},
		{"handler $event", `input (@input="q = $event.target.value" aria-label="x")`},
		{"key events take any modifier", `input (@keydown.ctrl.shift.k="go()" aria-label="x")`},
		{"debounce value", `input (@input.debounce.500ms="go()" aria-label="x")`},
		{"transition modifiers", `div x-transition.opacity.duration.100ms`},
		{"transition classes", `div x-transition:enter="transition ease-out duration-200"`},
		{"slot content scopes", "div x-data=`{ open: false }` {\n\tPanel { button (type=\"button\" @click=\"open = !open\") \"x\" }\n}"},
		{"names in slot content", "Panel { button (type=\"button\" @click=\"menuOpen = true\") \"x\" }"},
		{"same name in sibling scopes", "div x-data=`{ open: false }` { span x-show=\"open\" }\ndiv x-data=`{ open: true }` { span x-show=\"open\" }"},
		{"swap of state nothing writes", "div (\n\tx-data=`{ open: false }`\n\thx-get=\"/x\"\n\thx-trigger=\"every 2s\"\n\thx-swap=\"outerHTML\"\n) { span x-show=\"open\" }"},
		{"swap with a target", "div (\n\tx-data=`{ open: false }`\n\thx-get=\"/x\"\n\thx-target=\"#list\"\n\thx-swap=\"outerHTML\"\n)"},
		{"swap with morph", "div (\n\tx-data=`{ open: false }`\n\thx-get=\"/x\"\n\thx-swap=\"outerHTML\"\n\thx-ext=\"alpine-morph\"\n)"},
		{"template content count unknown", "template x-if=\"open\" {\n\tif on { p } else { span }\n}"},
		{"htmx event", `div @htmx:after-swap.window="refresh()"`},
		{"non-Alpine attributes", "div (class=\"x\" hx-on::after-request=\"go()\" data-x=`{ broken`)"},
		{"x-for from Go", "ul x-data=`{ docs: [] }` {\n\ttemplate (x-for=\"{data}\" :key=\"doc.id\") { li x-text=\"doc.name\" }\n\ttemplate x-for=\"doc in docs\" { li x-text=\"doc\" }\n}"},
		{"valueless x-data", "div x-data {\n\tbutton (type=\"button\" @click=\"$refs.box.focus()\") \"x\"\n\tdiv x-ref=\"box\"\n}"},
		{"statement forms", "div x-init=`if (ready) { start() } else { wait() }; let n = 1; for (const x of xs) n++`"},
		{"escaped x-data", "div x-data=\"\\u007b\\n\\topen: false,\\n\\ttoggle() \\u007b this.open = !this.open \\u007d\\n\\u007d\" { span x-show=\"open\" }"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := page(test.body)
			document := analyzeAlpine(t, source)
			var got []string
			for _, diagnostic := range document.Diagnostics {
				got = append(got, diagnostic.Code+":"+source[diagnostic.Start:diagnostic.End]+" "+diagnostic.Message)
			}
			if len(got) != 0 {
				t.Errorf("diagnostics = %v", got)
			}
		})
	}
}

func TestAnalyze_GIVENScopes_WHENResolved_THENReferencesBindTheirDeclarationsWithLiteralTypes(t *testing.T) {
	source := page("div x-data=`{ open: false, count: 0, name: 'x', items: [{ id: 1 }], toggle() { this.open = !this.open } }` {\n" +
		"\tbutton (type=\"button\" @click=\"toggle()\" x-text=\"open ? name : count\") \"x\"\n" +
		"\ttemplate x-for=\"item in items\" { li x-text=\"item.id\" }\n" +
		"\tdiv x-data=\"\\u007b open: {on} \\u007d\" { span x-show=\"open\" }\n" +
		"}")
	document := analyzeAlpine(t, source)
	types := make(map[string]string)
	for _, binding := range document.Bindings {
		types[binding.Name+"@"+source[binding.Start:binding.End]] = binding.Type.String()
	}
	for name, want := range map[string]string{"open@open": "boolean", "count@count": "number", "name@name": "string", "items@items": "{ id: number }[]", "toggle@toggle": "() => …", "item@item": "{ id: number }"} {
		if got := types[name]; got != want {
			t.Errorf("type of %s = %q, want %q (all %v)", name, got, want, types)
		}
	}
	resolved := make(map[int]*alpine.Binding)
	for _, reference := range document.References {
		resolved[reference.Start] = reference.Binding
	}
	outerOpen := strings.Index(source, "open: false")
	innerOpen := strings.Index(source, "open: {on}")
	check := func(marker string, shift, declaration int) {
		t.Helper()
		offset := strings.Index(source, marker) + shift
		binding := resolved[offset]
		if binding == nil || binding.Start != declaration {
			t.Errorf("reference %s at %d binds %v, want the declaration at %d", marker, offset, binding, declaration)
		}
	}
	check("this.open = ", 5, outerOpen)
	check("!this.open", 6, outerOpen)
	check("open ? name", 0, outerOpen)
	check("x-show=\"open\"", 8, innerOpen)
	check("item.id\"", 0, strings.Index(source, "item in items"))
	check("toggle()\" x-text", 0, strings.Index(source, "toggle() {"))
	for _, binding := range document.Bindings {
		if binding.Name == "open" && binding.Start == innerOpen && binding.Type.String() != "boolean" {
			t.Errorf("open from a KTML boolean = %s", binding.Type)
		}
	}
}
