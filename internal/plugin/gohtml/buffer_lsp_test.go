package gohtml

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unsavedDocument writes saved to disk and opens unsaved in the editor.
func unsavedDocument(t *testing.T, saved, unsaved string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "page.ktml")
	if err := os.WriteFile(path, []byte(saved), 0o600); err != nil {
		t.Fatal(err)
	}
	openDocument(t, path, unsaved, 1)
	return "file://" + path
}

func TestLSP_Formatting_GIVENUnsavedEdits_THENFormatsTheOpenBuffer(t *testing.T) {
	uri := unsavedDocument(t, "Saved [] {\n\tp \"saved\"\n}\n", "Unsaved [] {\n      p    \"edited\"\n}\n")
	response := callLSP(t, "textDocument/formatting", map[string]any{"textDocument": map[string]any{"uri": uri}, "options": map[string]any{"tabSize": 2, "insertSpaces": false}})
	if !strings.Contains(string(response), `Unsaved`) || !strings.Contains(string(response), `edited`) || strings.Contains(string(response), `saved\"`) {
		t.Errorf("formatting = %s, want the open buffer formatted", response)
	}
}

func TestLSP_Formatting_GIVENADocumentThatDoesNotParse_THENChangesNothing(t *testing.T) {
	uri := unsavedDocument(t, "", "Page [] {\n\tdiv class=\"broken\n\tp \"kept\"\n}\n")
	response := callLSP(t, "textDocument/formatting", map[string]any{"textDocument": map[string]any{"uri": uri}, "options": map[string]any{"tabSize": 2}})
	var decoded struct {
		Result []any `json:"result"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil || len(decoded.Result) != 0 {
		t.Errorf("formatting = %s, want no edits for a document with syntax errors", response)
	}
}

func TestLSP_DocumentSymbol_GIVENUnsavedEdits_THENListsTheOpenBuffersComponents(t *testing.T) {
	uri := unsavedDocument(t, "Saved [] {\n\tp \"saved\"\n}\n", "Unsaved [] {\n\tp \"edited\"\n}\n")
	response := callLSP(t, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": uri}})
	if !strings.Contains(string(response), `"name":"Unsaved"`) || strings.Contains(string(response), `"name":"Saved"`) {
		t.Errorf("document symbols = %s, want the open buffer's components", response)
	}
}

func TestLSP_FoldingRange_GIVENMultilineBlocks_THENFoldsEachOne(t *testing.T) {
	source := "import { A } from \"./a.ktml\"\n\n" + // 0
		"Page [\n" + // 2
		"\ton: boolean,\n" +
		"] {\n" + // 4
		"\tdiv (\n" + // 5
		"\t\tid=\"x\"\n" +
		"\t) {\n" + // 7
		"\t\tif on {\n" + // 8
		"\t\t\tp \"a\"\n" +
		"\t\t}\n" + // 10
		"\t\tscript ```\n" + // 11
		"\t\t\tlet a = 1\n" +
		"\t\t```\n" + // 13
		"\t}\n" + // 14
		"\tspan \"one line\"\n" +
		"}\n" // 16
	uri := unsavedDocument(t, source, source)
	response := callLSP(t, "textDocument/foldingRange", map[string]any{"textDocument": map[string]any{"uri": uri}})
	var decoded struct {
		Result []struct {
			StartLine int `json:"startLine"`
			EndLine   int `json:"endLine"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode %s: %v", response, err)
	}
	got := map[[2]int]bool{}
	for _, folding := range decoded.Result {
		got[[2]int{folding.StartLine, folding.EndLine}] = true
	}
	for _, want := range [][2]int{{2, 16}, {2, 4}, {5, 14}, {5, 7}, {8, 10}, {11, 13}} {
		if !got[want] {
			t.Errorf("folding ranges = %v, want lines %d-%d", decoded.Result, want[0], want[1])
		}
	}
	for folding := range got {
		if folding[0] >= folding[1] {
			t.Errorf("folding range %v spans one line", folding)
		}
	}
}

func TestLSP_Completion_GIVENAComponentInASiblingDirectory_THENOffersItWithAnImport(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"ktml.config.json": "{}\n",
		"ui/card.ktml":     "Card [title: string] {\n\th2 \"{title}\"\n}\n",
		"web/page.ktml":    "Page [] {\n\tCa\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	page := filepath.Join(root, "web", "page.ktml")
	openDocument(t, page, files["web/page.ktml"], 1)
	response := callLSP(t, "textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": "file://" + page},
		"position":     positionOf(t, files["web/page.ktml"], "\tCa", 3),
	})
	var decoded struct {
		Result struct {
			Items []struct {
				Label               string `json:"label"`
				AdditionalTextEdits []struct {
					NewText string `json:"newText"`
				} `json:"additionalTextEdits"`
			} `json:"items"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode %s: %v", response, err)
	}
	for _, item := range decoded.Result.Items {
		if item.Label == "Card" {
			if len(item.AdditionalTextEdits) != 1 || item.AdditionalTextEdits[0].NewText != "import { Card } from \"../ui/card\"\n" {
				t.Errorf("Card import edit = %+v", item.AdditionalTextEdits)
			}
			return
		}
	}
	t.Errorf("completion = %s, want Card from ../ui", response)
}

func TestLSP_ReferencesAndRename_GIVENCallersInOtherDirectories_THENIncludeThemAtTheirOpenBufferPositions(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"ktml.config.json": "{}\n",
		"ui/card.ktml":     "Card [title: string] {\n\th2 \"{title}\"\n}\n",
		"web/page.ktml":    "import { Card } from \"../ui/card\"\n\nPage [] {\n\tCard title=\"a\"\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	card := filepath.Join(root, "ui", "card.ktml")
	page := filepath.Join(root, "web", "page.ktml")
	// The caller has unsaved edits that move its call down two lines.
	unsaved := strings.Replace(files["web/page.ktml"], "Page [] {\n", "Page [] {\n\tp \"new\"\n\tp \"lines\"\n", 1)
	openDocument(t, page, unsaved, 2)
	openDocument(t, card, files["ui/card.ktml"], 1)

	references := callLSP(t, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": "file://" + card},
		"position":     positionOf(t, files["ui/card.ktml"], "Card", 1),
		"context":      map[string]any{"includeDeclaration": false},
	})
	call := positionOf(t, unsaved, "\tCard title", 1)
	if !strings.Contains(string(references), "file://"+page) || !strings.Contains(string(references), fmt.Sprintf(`"start":{"character":%d,"line":%d}`, call["character"], call["line"])) {
		t.Errorf("references = %s, want the call in web/page.ktml at its unsaved line %v", references, call["line"])
	}

	rename := callLSP(t, "textDocument/rename", map[string]any{
		"textDocument": map[string]any{"uri": "file://" + card},
		"position":     positionOf(t, files["ui/card.ktml"], "Card", 1),
		"newName":      "Tile",
	})
	var decoded struct {
		Result struct {
			Changes map[string][]struct {
				Range struct {
					Start struct{ Line, Character int } `json:"start"`
				} `json:"range"`
				NewText string `json:"newText"`
			} `json:"changes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rename, &decoded); err != nil {
		t.Fatalf("decode %s: %v", rename, err)
	}
	pageEdits := decoded.Result.Changes["file://"+page]
	var lines []int
	for _, edit := range pageEdits {
		lines = append(lines, edit.Range.Start.Line)
	}
	if len(decoded.Result.Changes["file://"+card]) != 1 || fmt.Sprint(lines) != fmt.Sprint([]int{0, call["line"].(int)}) {
		t.Errorf("rename = %s, want the declaration, the import (line 0), and the call (line %v)", rename, call["line"])
	}
}

func TestLSP_CodeLens_GIVENComponents_THENShowsEachOnesReferenceCountAcrossTheProject(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"ktml.config.json": "{}\n",
		"ui/card.ktml":     "Card [title: string] {\n\th2 \"{title}\"\n}\n\nBadge [] {\n\tspan \"b\"\n}\n\nUnused [] {\n\tCard title=\"x\"\n}\n",
		"web/page.ktml":    "import { Card, Badge } from \"../ui/card\"\n\nPage [] {\n\tCard title=\"a\"\n\tBadge\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	card := filepath.Join(root, "ui", "card.ktml")
	openDocument(t, card, files["ui/card.ktml"], 1)
	response := callLSP(t, "textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": "file://" + card}})
	var decoded struct {
		Result []struct {
			Range struct {
				Start struct{ Line int } `json:"start"`
			} `json:"range"`
			Command struct {
				Title string `json:"title"`
			} `json:"command"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode %s: %v", response, err)
	}
	var got []string
	for _, lens := range decoded.Result {
		got = append(got, fmt.Sprintf("%d:%s", lens.Range.Start.Line, lens.Command.Title))
	}
	// Card: the import, the call in Page, and the call in Unused.
	if want := "0:3 references 4:2 references 8:0 references"; strings.Join(got, " ") != want {
		t.Errorf("code lenses = %q, want %q", strings.Join(got, " "), want)
	}
}
