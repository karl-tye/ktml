package alpine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/symbolindex"
	"github.com/karl-tye/ktml/internal/textposition"
)

const menuSource = `import { Panel } from "./panel.ktml"

Menu [] {
	div (x-data=` + "`{ open: false, items: [{ label: 'a' }], toggle() { this.open = !this.open } }`" + ` @keydown.escape="open = false") {
		button (type="button" @click="toggle()" :aria-expanded="open") "Menu"
		ul x-show="open" {
			template x-for="item in items" { li x-text="item.label" }
		}
		Panel { span x-show="open" }
	}
	p x-text="open"
}

Plain [] {
	div x-data=` + "`{ count: 0 }`" + ` {
		button (type="button" @click="count++") "+"
		span x-text="count"
	}
}
`

const panelSource = `Panel [children: content] {
	section x-show="visible" { children }
}
`

func call(t *testing.T, method string, params any) []byte {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	response, err := HandleLSP(method, payload)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return response
}

func openMenu(t *testing.T) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	for name, source := range map[string]string{"menu.ktml": menuSource, "panel.ktml": panelSource} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	uri := pathURI(filepath.Join(dir, "menu.ktml"))
	response := call(t, "textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 1, "text": menuSource}})
	t.Cleanup(func() { call(t, "textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}}) })
	return uri, response
}

func at(t *testing.T, marker string, shift int) map[string]any {
	t.Helper()
	offset := strings.Index(menuSource, marker)
	if offset < 0 {
		t.Fatalf("marker %q not found", marker)
	}
	line, character := textposition.Position([]byte(menuSource), offset+shift)
	return map[string]any{"line": line, "character": character}
}

type lspRange struct {
	Start, End struct{ Line, Character int }
}

func (r lspRange) spell(source string) string {
	start := textposition.Offset([]byte(source), r.Start.Line, r.Start.Character)
	end := textposition.Offset([]byte(source), r.End.Line, r.End.Character)
	return source[start:end]
}

func lineOf(r lspRange) int { return r.Start.Line + 1 }

func TestLSP_GIVENAlpineDocument_WHENOpened_THENPublishesItsDiagnostics(t *testing.T) {
	_, response := openMenu(t)
	var notification struct {
		Method string `json:"method"`
		Params struct {
			Diagnostics []struct {
				Range  lspRange `json:"range"`
				Code   string   `json:"code"`
				Source string   `json:"source"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode %s: %v", response, err)
	}
	var got []string
	for _, diagnostic := range notification.Params.Diagnostics {
		got = append(got, diagnostic.Code+":"+diagnostic.Range.spell(menuSource)+":"+diagnostic.Source)
	}
	if want := "alpine-out-of-scope:open:alpine"; notification.Method != "textDocument/publishDiagnostics" || strings.Join(got, ",") != want {
		t.Errorf("diagnostics = %v, want %s", got, want)
	}
}

func TestLSP_GIVENAlpineNames_WHENNavigated_THENDefinitionHoverAndReferencesFollowScopes(t *testing.T) {
	uri, _ := openMenu(t)
	document := map[string]any{"uri": uri}
	var definition struct {
		Result struct {
			Range lspRange `json:"range"`
		} `json:"result"`
	}
	response := call(t, "textDocument/definition", map[string]any{"textDocument": document, "position": at(t, `x-show="open"`, 8)})
	if err := json.Unmarshal(response, &definition); err != nil || definition.Result.Range.spell(menuSource) != "open" || lineOf(definition.Result.Range) != 4 {
		t.Errorf("definition = %s", response)
	}
	response = call(t, "textDocument/hover", map[string]any{"textDocument": document, "position": at(t, `x-text="item.label"`, 8)})
	if !strings.Contains(string(response), "(x-for item) item: { label: string }") {
		t.Errorf("hover = %s", response)
	}
	response = call(t, "textDocument/hover", map[string]any{"textDocument": document, "position": at(t, "x-show", 2)})
	if !strings.Contains(string(response), "display: none") {
		t.Errorf("directive hover = %s", response)
	}
	var references struct {
		Result []struct {
			Range lspRange `json:"range"`
		} `json:"result"`
	}
	response = call(t, "textDocument/references", map[string]any{"textDocument": document, "position": at(t, "open: false", 0), "context": map[string]any{"includeDeclaration": true}})
	if err := json.Unmarshal(response, &references); err != nil {
		t.Fatal(err)
	}
	var lines []int
	for _, location := range references.Result {
		if location.Range.spell(menuSource) != "open" {
			t.Errorf("reference spells %q", location.Range.spell(menuSource))
		}
		lines = append(lines, lineOf(location.Range))
	}
	sort.Ints(lines)
	// The declaration, this.open twice, the escape handler, :aria-expanded,
	// and x-show; not the out-of-scope p on line 11 nor the slot content.
	if got := stringsOf(lines); got != "4 4 4 4 5 6" {
		t.Errorf("reference lines = %v", lines)
	}
}

func stringsOf(values []int) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconv.Itoa(value)
	}
	return strings.Join(parts, " ")
}

func TestLSP_GIVENAlpinePositions_WHENCompleted_THENOffersDirectivesModifiersAndScopedNames(t *testing.T) {
	uri, _ := openMenu(t)
	tests := []struct {
		name, source, marker string
		shift                int
		want                 []string
		absent               []string
	}{
		{"names in scope", menuSource, `x-text="item.label"`, 8, []string{"item", "open", "items", "toggle", "$refs"}, []string{"$event", "count"}},
		{"fields of an item", menuSource, `x-text="item.label"`, 13, []string{"label"}, nil},
		{"this in a method", menuSource, "this.open = ", 5, []string{"open", "items", "toggle"}, nil},
		{"$event in a handler", menuSource, `@click="toggle()"`, 8, []string{"$event", "toggle"}, nil},
		{"modifiers", menuSource, "@keydown.escape", 9, []string{"prevent", "stop", "window"}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := call(t, "textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(t, test.marker, test.shift)})
			var decoded struct {
				Result struct {
					Items []struct {
						Label string `json:"label"`
					} `json:"items"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response, &decoded); err != nil {
				t.Fatal(err)
			}
			labels := make(map[string]bool)
			for _, item := range decoded.Result.Items {
				labels[item.Label] = true
			}
			for _, label := range test.want {
				if !labels[label] {
					t.Errorf("missing %s in %v", label, labels)
				}
			}
			for _, label := range test.absent {
				if labels[label] {
					t.Errorf("unexpected %s", label)
				}
			}
		})
	}
}

func TestLSP_GIVENABinding_WHENRenamed_THENEditsOnlyItsScopeOrRefusesUnsafeRenames(t *testing.T) {
	uri, _ := openMenu(t)
	response := call(t, "textDocument/rename", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(t, "count: 0", 0), "newName": "total"})
	var rename struct {
		Result struct {
			Changes map[string][]struct {
				Range   lspRange `json:"range"`
				NewText string   `json:"newText"`
			} `json:"changes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &rename); err != nil {
		t.Fatal(err)
	}
	edits := rename.Result.Changes[uri]
	var lines []int
	for _, edit := range edits {
		if edit.Range.spell(menuSource) != "count" || edit.NewText != "total" {
			t.Errorf("edit %+v", edit)
		}
		lines = append(lines, lineOf(edit.Range))
	}
	sort.Ints(lines)
	if stringsOf(lines) != "15 16 17" {
		t.Errorf("rename lines = %v (%s)", lines, response)
	}
	// open is used by slot content Panel renders: the rename is refused.
	response = call(t, "textDocument/prepareRename", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(t, "open: false", 0)})
	if !strings.Contains(string(response), `"error"`) || !strings.Contains(string(response), "content another component renders") {
		t.Errorf("prepareRename = %s", response)
	}
	response = call(t, "textDocument/rename", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(t, "count: 0", 0), "newName": "1x"})
	if !strings.Contains(string(response), `"error"`) {
		t.Errorf("invalid name accepted: %s", response)
	}
}

func TestLSP_GIVENAWorkspace_WHENSymbolsAreQueried_THENListsAlpineScopes(t *testing.T) {
	openMenu(t)
	response := call(t, "workspace/symbol", map[string]any{"query": "tog"})
	if !strings.Contains(string(response), `"name":"toggle"`) || !strings.Contains(string(response), `"containerName":"x-data in Menu"`) {
		t.Errorf("symbols = %s", response)
	}
}

func TestLSP_GIVENAnOpenDocument_WHENTheHostIndexesIt_THENContributesScopeBindingsAndTheirUses(t *testing.T) {
	uri, _ := openMenu(t)
	response := call(t, symbolindex.Method, map[string]any{"id": 1, "uris": []string{uri}})
	var answer struct {
		Result symbolindex.Result `json:"result"`
	}
	if err := json.Unmarshal(response, &answer); err != nil || len(answer.Result.Documents) != 1 {
		t.Fatalf("index = %s", response)
	}
	document := answer.Result.Documents[0]
	var open symbolindex.Symbol
	for _, symbol := range document.Symbols {
		if symbol.Name == "open" {
			open = symbol
		}
	}
	if open.Kind != symbolindex.AlpineBinding {
		t.Fatalf("symbols = %+v, want the open property", document.Symbols)
	}
	var kinds []string
	for _, reference := range document.References {
		if reference.Symbol == open.ID {
			kinds = append(kinds, string(reference.Kind))
		}
	}
	// this.open = !this.open, open = false, :aria-expanded, and x-show: the
	// x-text outside the scope is not a use.
	if got := strings.Join(kinds, ","); got != "write,read,write,read,read" {
		t.Errorf("uses of open = %s", got)
	}
}
