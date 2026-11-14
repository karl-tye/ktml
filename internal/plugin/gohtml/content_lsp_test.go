package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const lspContentSchema = `record Address [
	city: string,
	note?: string,
]

record User [
	name: string,
	address?: Address,
]
`

const lspModal = `Modal [title: string, children: content, footer?: content] {
	section {
		h2 "{title}"
		children
		if footer? {
			footer
		}
	}
}
`

const lspPage = `import { Modal } from "./modal.ktml"
import { User } from "./user.ktmlschema"

Page [user: User, note?: string] {
	Modal title="Pay" {
		p "body"
		slot footer {
			button "Pay"
		}
	}
	if note? && user.address? {
		p "{note} {user.address.city}"
	}
}
`

func contentLSPWorkspace(t *testing.T) (modalPath, pagePath string, sources map[string]string) {
	t.Helper()
	dir := t.TempDir()
	modalPath = filepath.Join(dir, "modal.ktml")
	pagePath = filepath.Join(dir, "page.ktml")
	sources = map[string]string{filepath.Join(dir, "user.ktmlschema"): lspContentSchema, modalPath: lspModal, pagePath: lspPage}
	for path, source := range sources {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return modalPath, pagePath, sources
}

// fileOffsets renders "file|name|context" markers as sorted "file:offset".
func fileOffsets(t *testing.T, sources map[string]string, markers ...string) string {
	t.Helper()
	var spelled []string
	for _, marker := range markers {
		parts := strings.SplitN(marker, "|", 3)
		var source string
		for path, text := range sources {
			if filepath.Base(path) == parts[0] {
				source = text
			}
		}
		start := strings.Index(source, parts[2])
		if start < 0 {
			t.Fatalf("marker %q not found", parts[2])
		}
		spelled = append(spelled, parts[0]+":"+strconv.Itoa(start+strings.Index(parts[2], parts[1])))
	}
	sort.Strings(spelled)
	return strings.Join(spelled, ",")
}

func TestLSP_LanguageGapDiagnostics_GIVENInvalidContentAndConditions_THENPublishesCodesAtPreciseRanges(t *testing.T) {
	modalPath, pagePath, _ := contentLSPWorkspace(t)
	_ = modalPath
	source := `import { Modal } from "./modal.ktml"
import { User } from "./user.ktmlschema"

Page [user: User, note?: string, on: boolean] {
	Modal title="x" {
		slot sidebar {}
	}
	Modal title="y" footer="{note}" { p }
	if user.name { p }
	if on? { p }
	p "{note}"
}
`
	response := openDocument(t, pagePath, source, 1)
	var notification struct {
		Params struct {
			Diagnostics []struct {
				Range    lspRange `json:"range"`
				Severity int      `json:"severity"`
				Code     string   `json:"code"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode notification %s: %v", response, err)
	}
	var got []string
	for _, diagnostic := range notification.Params.Diagnostics {
		got = append(got, strconv.Itoa(diagnostic.Severity)+":"+diagnostic.Code+":"+diagnostic.Range.spell(t, source))
	}
	want := strings.Join([]string{
		"1:missing-content:Modal",
		"1:unknown-slot:sidebar",
		"1:content-as-attribute:footer=\"{note}\"",
		"1:non-boolean-condition:user.name",
		"1:presence-requires-optional:on?",
		"1:optional-value-access:note",
	}, "\n")
	if joined := strings.Join(got, "\n"); joined != want {
		t.Errorf("diagnostics =\n%s\nwant\n%s", joined, want)
	}

	syntax := "Page [on: boolean] {\n\tif on == true { p }\n}\n"
	response = openDocument(t, pagePath, syntax, 2)
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode notification %s: %v", response, err)
	}
	got = nil
	for _, diagnostic := range notification.Params.Diagnostics {
		got = append(got, diagnostic.Code+":"+diagnostic.Range.spell(t, syntax))
	}
	if joined := strings.Join(got, ","); joined != "condition-comparison:==" {
		t.Errorf("syntax diagnostics = %s", joined)
	}
}

func TestLSP_ContentCompletion_GIVENSlotFillsAndConditions_THENOffersSlotsAndPaths(t *testing.T) {
	_, pagePath, _ := contentLSPWorkspace(t)
	const header = "import { Modal } from \"./modal.ktml\"\nimport { User } from \"./user.ktmlschema\"\n\nPage [user: User, note?: string] {\n"
	tests := []struct {
		name, source, marker, want string
	}{
		{name: "slot names", source: header + "\tModal title=\"x\" {\n\t\tslot ", marker: "slot ", want: "footer:10"},
		{name: "condition roots", source: header + "\tif ", marker: "if ", want: "user:6,note:6"},
		{name: "narrowed condition fields", source: header + "\tif user.address? && user.address.", marker: "&& user.address.", want: "city:5,note:5"},
		{name: "content type", source: "Card [actions?: ", marker: "actions?: ", want: "string:25,boolean:25,number:25,integer:25,content:25"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openDocument(t, pagePath, tt.source, 1)
			response := callLSP(t, "textDocument/completion", map[string]any{
				"id": 1, "textDocument": map[string]any{"uri": "file://" + pagePath},
				"position": positionOf(t, tt.source, tt.marker, len(tt.marker)),
			})
			var decoded struct {
				Result struct {
					Items []struct {
						Label string `json:"label"`
						Kind  int    `json:"kind"`
					} `json:"items"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response, &decoded); err != nil {
				t.Fatalf("decode completion %s: %v", response, err)
			}
			var got []string
			for _, item := range decoded.Result.Items {
				got = append(got, item.Label+":"+strconv.Itoa(item.Kind))
			}
			if joined := strings.Join(got, ","); joined != tt.want {
				t.Errorf("completion = %s, want %s", joined, tt.want)
			}
		})
	}
}

func TestLSP_ContentHoverAndDefinition_GIVENOutletsFillsAndNarrowedPaths_THENDescribesAndLocatesThem(t *testing.T) {
	modalPath, pagePath, sources := contentLSPWorkspace(t)
	const footer = "slot footer?: content (Modal)"
	tests := []struct {
		name, path, source, marker, hover, definition string
	}{
		{name: "fill name", path: pagePath, source: lspPage, marker: "footer {", hover: footer, definition: "modal.ktml|footer|footer?: content"},
		{name: "outlet", path: modalPath, source: lspModal, marker: "footer\n\t\t}", hover: footer, definition: "modal.ktml|footer|footer?: content"},
		{name: "presence root", path: modalPath, source: lspModal, marker: "footer? {", hover: footer, definition: "modal.ktml|footer|footer?: content"},
		{name: "children outlet", path: modalPath, source: lspModal, marker: "children\n", hover: "children: content (children of Modal)", definition: "modal.ktml|children|children: content"},
		{name: "narrowed path", path: pagePath, source: lspPage, marker: "note} ", hover: "input note?: string\nnarrowed to string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{
				"id": 1, "textDocument": map[string]any{"uri": "file://" + tt.path},
				"position": positionOf(t, tt.source, tt.marker, 1),
			}
			var hover struct {
				Result struct {
					Contents struct {
						Value string `json:"value"`
					} `json:"contents"`
				} `json:"result"`
			}
			response := callLSP(t, "textDocument/hover", params)
			if err := json.Unmarshal(response, &hover); err != nil {
				t.Fatalf("decode hover %s: %v", response, err)
			}
			if want := "```ktml\n" + tt.hover + "\n```"; hover.Result.Contents.Value != want {
				t.Errorf("hover = %q, want %q", hover.Result.Contents.Value, want)
			}
			if tt.definition == "" {
				return
			}
			var definition struct {
				Result *lspLocation `json:"result"`
			}
			response = callLSP(t, "textDocument/definition", params)
			if err := json.Unmarshal(response, &definition); err != nil || definition.Result == nil {
				t.Fatalf("definition = %s (%v)", response, err)
			}
			if got, want := definition.Result.at(t, sources), fileOffsets(t, sources, tt.definition); got != want {
				t.Errorf("definition = %s, want %s", got, want)
			}
		})
	}
}

func TestLSP_ContentReferencesAndRename_GIVENANamedSlot_THENEditsDeclarationOutletsAndFillsAcrossFiles(t *testing.T) {
	modalPath, pagePath, sources := contentLSPWorkspace(t)
	uses := []string{"modal.ktml|footer|footer?: content", "modal.ktml|footer|footer? {", "modal.ktml|footer|footer\n\t\t}", "page.ktml|footer|footer {"}
	position := positionOf(t, lspPage, "footer {", 1)
	uri := "file://" + pagePath
	var references struct {
		Result []lspLocation `json:"result"`
	}
	response := callLSP(t, "textDocument/references", map[string]any{
		"id": 1, "textDocument": map[string]any{"uri": uri}, "position": position,
		"context": map[string]any{"includeDeclaration": true},
	})
	if err := json.Unmarshal(response, &references); err != nil {
		t.Fatalf("decode references %s: %v", response, err)
	}
	var spelled []string
	for _, location := range references.Result {
		spelled = append(spelled, location.at(t, sources))
	}
	sort.Strings(spelled)
	want := fileOffsets(t, sources, uses...)
	if got := strings.Join(spelled, ","); got != want {
		t.Errorf("references = %s, want %s", got, want)
	}

	var rename struct {
		Result *struct {
			Changes map[string][]struct {
				Range   json.RawMessage `json:"range"`
				NewText string          `json:"newText"`
			} `json:"changes"`
		} `json:"result"`
	}
	response = callLSP(t, "textDocument/rename", map[string]any{"id": 1, "textDocument": map[string]any{"uri": uri}, "position": position, "newName": "bottom"})
	if err := json.Unmarshal(response, &rename); err != nil || rename.Result == nil {
		t.Fatalf("rename = %s (%v)", response, err)
	}
	spelled = nil
	for editURI, edits := range rename.Result.Changes {
		for _, edit := range edits {
			location := lspLocation{URI: editURI}
			if err := json.Unmarshal(edit.Range, &location.Range); err != nil {
				t.Fatalf("decode range: %v", err)
			}
			spelled = append(spelled, location.at(t, sources))
		}
	}
	sort.Strings(spelled)
	if got := strings.Join(spelled, ","); got != want {
		t.Errorf("rename edits = %s, want %s", got, want)
	}

	for _, newName := range []string{"children", "title", "if"} {
		response = callLSP(t, "textDocument/rename", map[string]any{"id": 1, "textDocument": map[string]any{"uri": uri}, "position": position, "newName": newName})
		if !strings.Contains(string(response), `"result":null`) {
			t.Errorf("rename to %s = %s, want rejection", newName, response)
		}
	}
	response = callLSP(t, "textDocument/prepareRename", map[string]any{"id": 1, "textDocument": map[string]any{"uri": "file://" + modalPath}, "position": positionOf(t, lspModal, "children\n", 1)})
	if !strings.Contains(string(response), `"result":null`) {
		t.Errorf("prepareRename children = %s, want null", response)
	}
}

func TestLSP_SemanticTokens_GIVENConditionsAndSlots_THENEncodesKeywordsOperatorsAndOutlets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "card.ktml")
	source := "Card [on: boolean] {\n\tif !on { children } else { Box { slot top {} } }\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	response := callLSP(t, "textDocument/semanticTokens/full", map[string]any{
		"id": 1, "textDocument": map[string]any{"uri": "file://" + path},
	})
	var decoded struct {
		Result struct {
			Data []int `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode semantic tokens %s: %v", response, err)
	}
	// parameter on (declaration), type boolean; line 1: keyword if, operator
	// !, parameter on, parameter children, keyword else, keyword slot,
	// parameter top.
	want := []int{
		0, 6, 2, 3, 1,
		0, 4, 7, 1, 0,
		1, 1, 2, 0, 0,
		0, 3, 1, 5, 0,
		0, 1, 2, 3, 0,
		0, 5, 8, 3, 0,
		0, 11, 4, 0, 0,
		0, 13, 4, 0, 0,
		0, 5, 3, 3, 0,
	}
	if got, wanted := fmtInts(decoded.Result.Data), fmtInts(want); got != wanted {
		t.Errorf("data = %s, want %s", got, wanted)
	}
}
