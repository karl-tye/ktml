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

const lspBoardSchema = `record Card [
	id: string,
	title: string,
	score: number,
	note?: string,
]

record Column [
	id: integer,
	title: string,
	cards: [Card],
]
`

const lspBoard = `import { Card, Column } from "./board.ktmlschema"

Columns [columns: [Column], title: string] {
	for column in columns key column.id {
		h2 "{column.title}"
		for card in column.cards key card.id {
			p title="{card.title}" { "{column.title} {title}" }
		}
	}
	for card in columns key card.id { em "{card.title}" }
}
`

func repetitionLSPWorkspace(t *testing.T) (schemaPath, boardPath string, sources map[string]string) {
	t.Helper()
	dir := t.TempDir()
	schemaPath = filepath.Join(dir, "board.ktmlschema")
	boardPath = filepath.Join(dir, "board.ktml")
	sources = map[string]string{schemaPath: lspBoardSchema, boardPath: lspBoard}
	for path, source := range sources {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return schemaPath, boardPath, sources
}

// offsets renders "name|context" markers as sorted "board.ktml:offset" spellings.
func offsets(t *testing.T, source string, markers ...string) string {
	t.Helper()
	spelled := make([]string, 0, len(markers))
	for _, marker := range markers {
		name, context, _ := strings.Cut(marker, "|")
		start := strings.Index(source, context)
		if start < 0 {
			t.Fatalf("marker %q not found", context)
		}
		spelled = append(spelled, "board.ktml:"+strconv.Itoa(start+strings.Index(context, name)))
	}
	sort.Strings(spelled)
	return strings.Join(spelled, ",")
}

func TestLSP_RepetitionDiagnostics_GIVENInvalidRepetitions_THENPublishesCodesAtPreciseRanges(t *testing.T) {
	_, boardPath, _ := repetitionLSPWorkspace(t)
	source := `import { Card } from "./board.ktmlschema"

Board [title: string, maybe?: [Card], cards: [Card]] {
	for t in title key t {}
	for m in maybe key m.id {}
	for c in cards key title {}
	for d in cards key d.score {}
	for e in cards key e.note {}
	for title in cards key title.id {}
}
`
	response := openDocument(t, boardPath, source, 1)
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
		"1:repetition-not-collection:title",
		"1:optional-collection:maybe",
		"1:key-not-rooted:title",
		"1:invalid-key-type:d.score",
		"1:optional-key:e.note",
		"1:shadowing-binding:title",
	}, "\n")
	if joined := strings.Join(got, "\n"); joined != want {
		t.Errorf("diagnostics =\n%s\nwant\n%s", joined, want)
	}
}

func TestLSP_RepetitionCompletion_GIVENEditedHeadersAndBodies_THENOffersPathRootsItemsAndFields(t *testing.T) {
	_, boardPath, _ := repetitionLSPWorkspace(t)
	const header = "import { Card, Column } from \"./board.ktmlschema\"\n\nColumns [columns: [Column], title: string] {\n\tfor column in columns key column.id {\n\t\t"
	tests := []struct {
		name   string
		source string
		marker string
		want   string
	}{
		{name: "collection roots", source: header + "for card in ", marker: "card in ", want: "columns:6,title:6,column:6"},
		{name: "collection fields", source: header + "for card in column.", marker: "in column.", want: "id:5,title:5,cards:5"},
		{name: "key root", source: header + "for card in column.cards key ", marker: "cards key ", want: "card:6"},
		{name: "key fields", source: header + "for card in column.cards key card.", marker: "key card.", want: "id:5,title:5,score:5,note:5"},
		{name: "body roots", source: header + "for card in column.cards key card.id { p \"{", marker: "p \"{", want: "columns:6,title:6,column:6,card:6"},
		{name: "body fields", source: header + "for card in column.cards key card.id { p \"{card.", marker: "{card.", want: "id:5,title:5,score:5,note:5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openDocument(t, boardPath, tt.source, 1)
			response := callLSP(t, "textDocument/completion", map[string]any{
				"id": 1, "textDocument": map[string]any{"uri": "file://" + boardPath},
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

func TestLSP_RepetitionHoverAndDefinition_GIVENItemDeclarationsAndUses_THENDescribesAndLocatesTheItem(t *testing.T) {
	_, boardPath, sources := repetitionLSPWorkspace(t)
	const column = "item column: Column\nfor column in columns key column.id"
	const card = "item card: Card\nfor card in column.cards key card.id"
	tests := []struct {
		name       string
		marker     string
		hover      string
		definition string
	}{
		{name: "item declaration", marker: "column in", hover: column, definition: "column|column in"},
		{name: "key root", marker: "column.id {", hover: column, definition: "column|column in"},
		{name: "nested collection root", marker: "column.cards", hover: column, definition: "column|column in"},
		{name: "attribute body root", marker: "card.title}\" {", hover: card, definition: "card|card in column"},
		{name: "same-spelled sibling item", marker: "card.title}\" }", hover: "item card: Column\nfor card in columns key card.id", definition: "card|card in columns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{
				"id": 1, "textDocument": map[string]any{"uri": "file://" + boardPath},
				"position": positionOf(t, lspBoard, tt.marker, 1),
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
			var definition struct {
				Result *lspLocation `json:"result"`
			}
			response = callLSP(t, "textDocument/definition", params)
			if err := json.Unmarshal(response, &definition); err != nil || definition.Result == nil {
				t.Fatalf("definition = %s (%v)", response, err)
			}
			if got, want := definition.Result.at(t, sources), offsets(t, lspBoard, tt.definition); got != want {
				t.Errorf("definition = %s, want %s", got, want)
			}
		})
	}
}

func TestLSP_RepetitionReferencesAndRename_GIVENItemBindings_THENEditsOnlyThatItem(t *testing.T) {
	_, boardPath, sources := repetitionLSPWorkspace(t)
	columnUses := []string{"column|column in", "column|column.id {", "column|column.title}\"\n", "column|column.cards", "column|column.title} {title}"}
	cardUses := []string{"card|card in column", "card|card.id {\n\t\t\tp", "card|card.title}\" {"}
	tests := []struct {
		name       string
		marker     string
		references []string
		newName    string
		rename     bool
	}{
		{name: "outer item from a nested collection root", marker: "column.cards", references: columnUses, newName: "lane", rename: true},
		{name: "nested item from its declaration", marker: "card in column", references: cardUses, newName: "ticket", rename: true},
		{name: "invalid identifier is rejected", marker: "card in column", references: cardUses, newName: "two words"},
		{name: "input collision is rejected", marker: "column in", references: columnUses, newName: "title"},
		{name: "enclosing item collision is rejected", marker: "card in column", references: cardUses, newName: "column"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			position := positionOf(t, lspBoard, tt.marker, 1)
			uri := "file://" + boardPath
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
			want := offsets(t, lspBoard, tt.references...)
			if got := strings.Join(spelled, ","); got != want {
				t.Errorf("references = %s, want %s", got, want)
			}

			var prepare struct {
				Result *struct {
					Range       lspRange `json:"range"`
					Placeholder string   `json:"placeholder"`
				} `json:"result"`
			}
			response = callLSP(t, "textDocument/prepareRename", map[string]any{"id": 1, "textDocument": map[string]any{"uri": uri}, "position": position})
			if err := json.Unmarshal(response, &prepare); err != nil || prepare.Result == nil {
				t.Fatalf("prepareRename = %s (%v)", response, err)
			}
			name, _, _ := strings.Cut(tt.references[0], "|")
			if prepare.Result.Placeholder != name || prepare.Result.Range.spell(t, lspBoard) != name {
				t.Errorf("prepareRename = %q at %q, want %q", prepare.Result.Placeholder, prepare.Result.Range.spell(t, lspBoard), name)
			}

			var rename struct {
				Result *struct {
					Changes map[string][]struct {
						Range   json.RawMessage `json:"range"`
						NewText string          `json:"newText"`
					} `json:"changes"`
				} `json:"result"`
			}
			response = callLSP(t, "textDocument/rename", map[string]any{"id": 1, "textDocument": map[string]any{"uri": uri}, "position": position, "newName": tt.newName})
			if err := json.Unmarshal(response, &rename); err != nil {
				t.Fatalf("decode rename %s: %v", response, err)
			}
			if !tt.rename {
				if rename.Result != nil {
					t.Errorf("rename = %s, want rejection", response)
				}
				return
			}
			if rename.Result == nil {
				t.Fatalf("rename = %s, want edits", response)
			}
			spelled = nil
			for editURI, edits := range rename.Result.Changes {
				for _, edit := range edits {
					if edit.NewText != tt.newName {
						t.Errorf("newText = %q, want %q", edit.NewText, tt.newName)
					}
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
		})
	}
}

func TestLSP_SemanticTokens_GIVENKeyedRepetition_THENEncodesKeywordsAndItemParameters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "list.ktml")
	source := "List [items: [Item]] {\n\tfor item in items key item.id {}\n}\n"
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
	// parameter items (declaration), type Item; then on line 1: keyword for,
	// parameter item (declaration), keyword in, parameter items, keyword key,
	// parameter item, property id.
	want := []int{
		0, 6, 5, 3, 1,
		0, 8, 4, 1, 0,
		1, 1, 3, 0, 0,
		0, 4, 4, 3, 1,
		0, 5, 2, 0, 0,
		0, 3, 5, 3, 0,
		0, 6, 3, 0, 0,
		0, 4, 4, 3, 0,
		0, 5, 2, 2, 0,
	}
	if got, wanted := fmtInts(decoded.Result.Data), fmtInts(want); got != wanted {
		t.Errorf("data = %s, want %s", got, wanted)
	}
}
