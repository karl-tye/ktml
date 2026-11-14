package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const signatureField = `Field [label: string, hint?: string, size: string = "md", attrs?: Attributes, children: content, footer?: content] {
	div attrs=(...attrs) {
		label "{label}"
		children
		if footer? {
			footer
		}
	}
}
`

func TestLSP_SignatureHelp_GIVENACallBeingWritten_WHENAsked_THENShowsTheInputsWithTheArgumentBeingWrittenActive(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "field.ktml"), []byte(signatureField), 0o600); err != nil {
		t.Fatal(err)
	}
	const header = "import { Field } from \"./field.ktml\"\n\nPage [] {\n"
	tests := []struct {
		name, body string
		want       string // the active parameter's label, or "" for none
	}{
		{"after the name", "\tField |\n}", "label: string"},
		{"in a value", "\tField label=\"Na|\"\n}", "label: string"},
		{"naming an argument", "\tField label=\"x\" hi|\n}", "hint?: string"},
		{"after the required arguments", "\tField label=\"x\" |\n}", ""},
		{"in a bag", "\tField label=\"x\" attrs=(id=\"a\" |)\n}", "attrs?: Attributes"},
		{"in a group", "\tField (\n\t\tlabel=\"x\"\n\t\tsi|\n\t)\n}", `size: string = "md"`},
		{"naming a slot", "\tField label=\"x\" {\n\t\tp \"body\"\n\t\tslot |\n\t}\n}", "footer?: content"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := header + test.body
			offset := strings.Index(source, "|")
			source = strings.Replace(source, "|", "", 1)
			path := filepath.Join(dir, "page"+string(rune('a'+index))+".ktml")
			openDocument(t, path, source, 1)
			raw := callLSP(t, "textDocument/signatureHelp", map[string]any{"id": 1, "textDocument": map[string]any{"uri": "file://" + path}, "position": positionOf(t, source, source[offset:], 0)})
			var response struct {
				Result *struct {
					Signatures []struct {
						Label      string `json:"label"`
						Parameters []struct {
							Label [2]int `json:"label"`
						} `json:"parameters"`
					} `json:"signatures"`
					ActiveParameter *int `json:"activeParameter"`
				} `json:"result"`
			}
			if err := json.Unmarshal(raw, &response); err != nil || response.Result == nil || len(response.Result.Signatures) != 1 {
				t.Fatalf("signature help = %s", raw)
			}
			signature := response.Result.Signatures[0]
			if want := `Field [label: string, hint?: string, size: string = "md", attrs?: Attributes, children: content, footer?: content]`; signature.Label != want {
				t.Errorf("label = %q", signature.Label)
			}
			got := ""
			if active := response.Result.ActiveParameter; active != nil {
				span := signature.Parameters[*active].Label
				got = signature.Label[span[0]:span[1]]
			}
			if got != test.want {
				t.Errorf("active parameter = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLSP_SignatureHelp_GIVENAnElementOrText_WHENAsked_THENHasNone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.ktml")
	for _, source := range []string{"Page [] {\n\tdiv cl|\n}", "Page [] {\n\tp \"Fi|\"\n}", "Page [na|] {\n}"} {
		offset := strings.Index(source, "|")
		source = strings.Replace(source, "|", "", 1)
		openDocument(t, path, source, 1)
		raw := callLSP(t, "textDocument/signatureHelp", map[string]any{"id": 1, "textDocument": map[string]any{"uri": "file://" + path}, "position": positionOf(t, source, source[offset:], 0)})
		if !strings.Contains(string(raw), `"result":null`) {
			t.Errorf("signature help in %q = %s, want none", source, raw)
		}
	}
}
