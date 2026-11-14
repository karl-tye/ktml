package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/symbolindex"
)

// indexProject writes a project and makes it the working directory, as the
// host does for the guest.
func indexProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	return dir
}

func requestIndex(t *testing.T, request symbolindex.Request) symbolindex.Result {
	t.Helper()
	params, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]any
	_ = json.Unmarshal(params, &members)
	members["id"] = 7
	var response struct {
		Result symbolindex.Result `json:"result"`
	}
	raw := callLSP(t, symbolindex.Method, members)
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return response.Result
}

func documentFor(t *testing.T, result symbolindex.Result, path string) symbolindex.Document {
	t.Helper()
	for _, document := range result.Documents {
		if document.URI == "file://"+path {
			return document
		}
	}
	t.Fatalf("no document %s in %+v", path, result)
	return symbolindex.Document{}
}

func TestLSP_Index_GIVENAProject_WHENTheHostAsksForTheWorkspace_THENEveryDocumentContributesItsSymbols(t *testing.T) {
	dir := indexProject(t, map[string]string{
		"ktml.config.json": "{}",
		"card.ktml":        "Card [title: string] {\n\th2 \"{title}\"\n}\n",
		"page.ktml":        "import { Card } from \"./card.ktml\"\n\nPage {\n\tCard title=\"x\"\n}\n",
	})
	result := requestIndex(t, symbolindex.Request{Workspace: true})

	card := documentFor(t, result, filepath.Join(dir, "card.ktml"))
	var cardID string
	for _, symbol := range card.Symbols {
		if symbol.Kind == symbolindex.Component && symbol.Name == "Card" {
			cardID = symbol.ID
			if symbol.Range != (symbolindex.Range{Start: symbolindex.Position{Line: 0, Character: 0}, End: symbolindex.Position{Line: 0, Character: 4}}) {
				t.Errorf("Card range = %+v", symbol.Range)
			}
		}
	}
	if cardID == "" {
		t.Fatalf("card.ktml symbols = %+v", card.Symbols)
	}
	page := documentFor(t, result, filepath.Join(dir, "page.ktml"))
	var kinds []string
	for _, reference := range page.References {
		if reference.Symbol == cardID {
			kinds = append(kinds, string(reference.Kind)+"@"+strconv.Itoa(reference.Range.Start.Line)+" from "+reference.From)
		}
	}
	pageID := ""
	for _, symbol := range page.Symbols {
		if symbol.Name == "Page" {
			pageID = symbol.ID
		}
	}
	if got, want := strings.Join(kinds, ", "), "import@0 from , call@3 from "+pageID; got != want {
		t.Errorf("references to Card = %q, want %q", got, want)
	}
}

func TestLSP_Index_GIVENAnEditedDeclaration_WHENTheHostAsksForIt_THENItsImportersAreIndexedAgain(t *testing.T) {
	dir := indexProject(t, map[string]string{
		"ktml.config.json": "{}",
		"card.ktml":        "Card [title: string] {\n\th2 \"{title}\"\n}\n",
		"page.ktml":        "import { Card } from \"./card.ktml\"\n\nPage {\n\tCard title=\"x\"\n}\n",
		"other.ktml":       "Other {\n\tp \"o\"\n}\n",
	})
	requestIndex(t, symbolindex.Request{Workspace: true})
	cardPath := filepath.Join(dir, "card.ktml")
	openDocument(t, cardPath, "Card [heading: string] {\n\th2 \"{heading}\"\n}\n", 2)

	result := requestIndex(t, symbolindex.Request{URIs: []string{"file://" + cardPath}})
	var uris []string
	for _, document := range result.Documents {
		uris = append(uris, filepath.Base(document.URI))
	}
	if got := strings.Join(uris, ","); got != "card.ktml,page.ktml" {
		t.Fatalf("indexed %s, want the edited document and its importer", got)
	}
	found := false
	for _, symbol := range documentFor(t, result, cardPath).Symbols {
		found = found || symbol.Kind == symbolindex.Input && symbol.Name == "heading"
	}
	if !found {
		t.Errorf("card.ktml symbols = %+v, want the edited input", documentFor(t, result, cardPath).Symbols)
	}
}

func TestLSP_Index_GIVENADeletedDocument_WHENTheHostAsksForIt_THENItIsRemoved(t *testing.T) {
	dir := indexProject(t, map[string]string{
		"ktml.config.json": "{}",
		"card.ktml":        "Card {\n\tp \"c\"\n}\n",
		"gone.ktml":        "Gone {\n\tp \"g\"\n}\n",
	})
	requestIndex(t, symbolindex.Request{Workspace: true})
	gone := filepath.Join(dir, "gone.ktml")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	result := requestIndex(t, symbolindex.Request{URIs: []string{"file://" + gone}})
	if len(result.Removed) != 1 || result.Removed[0] != "file://"+gone {
		t.Fatalf("result = %+v, want gone.ktml removed", result)
	}
}

func TestLSP_GIVENAWordThatOnlySpellsADeclaration_WHENNavigating_THENNothingIsFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ktml")
	text := "Button {\n\tbutton\n}\n\nMain {\n\tp \"Button\"\n\tButton\n}"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	position := positionOf(t, text, "\"Button\"", 2)
	params := map[string]any{"id": 1, "textDocument": map[string]any{"uri": "file://" + path}, "position": position, "context": map[string]any{"includeDeclaration": true}}

	var definition struct {
		Result any `json:"result"`
	}
	if raw := callLSP(t, "textDocument/definition", params); json.Unmarshal(raw, &definition) != nil || definition.Result != nil {
		t.Errorf("definition in a text literal = %s, want null", raw)
	}
	var references struct {
		Result []any `json:"result"`
	}
	if raw := callLSP(t, "textDocument/references", params); json.Unmarshal(raw, &references) != nil || len(references.Result) != 0 {
		t.Errorf("references in a text literal = %s, want none", raw)
	}
	var hover struct {
		Result any `json:"result"`
	}
	if raw := callLSP(t, "textDocument/hover", params); json.Unmarshal(raw, &hover) != nil || hover.Result != nil {
		t.Errorf("hover in a text literal = %s, want null", raw)
	}
}

func TestLSP_WorkspaceSymbol_GIVENTheProjectRoot_WHENQueried_THENLocationsAreAbsoluteFileURIs(t *testing.T) {
	dir := indexProject(t, map[string]string{
		"ktml.config.json": "{}",
		"card.ktml":        "Card {\n\tp \"c\"\n}\n",
	})
	var response struct {
		Result []struct {
			Name     string `json:"name"`
			Location struct {
				URI string `json:"uri"`
			} `json:"location"`
		} `json:"result"`
	}
	raw := callLSP(t, "workspace/symbol", map[string]any{"id": 1, "query": "card"})
	if err := json.Unmarshal(raw, &response); err != nil || len(response.Result) != 1 {
		t.Fatalf("workspace/symbol = %s", raw)
	}
	if got, want := response.Result[0].Location.URI, "file://"+filepath.Join(dir, "card.ktml"); got != want {
		t.Errorf("URI = %q, want %q", got, want)
	}
}

func TestLSP_GIVENAnIndexedProject_WHENAnEditedReaderIsRenamedFromANewWorkspace_THENTheRenameSeesTheUnsavedText(t *testing.T) {
	dir := indexProject(t, map[string]string{"ktml.config.json": "{}"})
	sub := filepath.Join(dir, "reviews")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	schema := "record View [\n\tagent: string,\n]\n"
	page := "import { View } from \"./view.ktmlschema\"\n\nPage [view: View] {\n\tp \"{view.agent}\"\n}\n"
	for name, source := range map[string]string{"view.ktmlschema": schema, "page.ktml": page} {
		if err := os.WriteFile(filepath.Join(sub, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	requestIndex(t, symbolindex.Request{Workspace: true})
	pagePath := filepath.Join(sub, "page.ktml")
	edited := strings.Replace(page, "\tp \"{view.agent}\"\n", "\tp \"{view.agent}\"\n\tspan \"{view.agent}\"\n", 1)
	openDocument(t, pagePath, edited, 2)

	params := map[string]any{"id": 1, "textDocument": map[string]any{"uri": "file://" + pagePath}, "position": positionOf(t, edited, "{view.agent}", len("{view.a")), "newName": "owner"}
	raw := callLSP(t, "textDocument/rename", params)
	if count := strings.Count(string(raw), `"newText":"owner"`); count != 3 {
		t.Errorf("rename edits = %d, want the field and both readers: %s", count, raw)
	}
}

// TestLSP_Index_GIVENStoryDocuments_WHENTheWorkspaceIsIndexed_THENTheyJoinOnlyWhenOpened
// keeps story Documents out of the initial index, where they cost the most
// and are needed the least; an opened one is indexed.
func TestLSP_Index_GIVENStoryDocuments_WHENTheWorkspaceIsIndexed_THENTheyJoinOnlyWhenOpened(t *testing.T) {
	story := "import { Card } from \"./card.ktml\"\n\nstory Card.Default [title = \"x\"]\n"
	dir := indexProject(t, map[string]string{
		"ktml.config.json": "{}",
		"card.ktml":        "Card [title: string] {\n\th2 \"{title}\"\n}\n",
		"card.story.ktml":  story,
	})
	storyPath := filepath.Join(dir, "card.story.ktml")
	result := requestIndex(t, symbolindex.Request{Workspace: true})
	for _, document := range result.Documents {
		if document.URI == "file://"+storyPath {
			t.Fatalf("the workspace index holds the unopened story Document")
		}
	}
	documentFor(t, result, filepath.Join(dir, "card.ktml"))

	openDocument(t, storyPath, story, 1)
	result = requestIndex(t, symbolindex.Request{URIs: []string{"file://" + storyPath}})
	found := false
	for _, symbol := range documentFor(t, result, storyPath).Symbols {
		found = found || symbol.Kind == symbolindex.Story
	}
	if !found {
		t.Errorf("the opened story Document has no story symbol: %+v", documentFor(t, result, storyPath))
	}
}

// TestLSP_GIVENUnopenedStoryDocuments_WHENAComponentIsRenamed_THENTheirHeadersAreRenamed
// loads the deferred story Documents for a rename, which must change them.
func TestLSP_GIVENUnopenedStoryDocuments_WHENAComponentIsRenamed_THENTheirHeadersAreRenamed(t *testing.T) {
	card := "Card [title: string] {\n\th2 \"{title}\"\n}\n"
	dir := indexProject(t, map[string]string{
		"ktml.config.json": "{}",
		"card.ktml":        card,
		"card.story.ktml":  "import { Card } from \"./card.ktml\"\n\nstory Card.Default [title = \"x\"]\n",
	})
	requestIndex(t, symbolindex.Request{Workspace: true})
	cardPath := filepath.Join(dir, "card.ktml")
	openDocument(t, cardPath, card, 1)
	raw := callLSP(t, "textDocument/rename", map[string]any{"id": 1, "textDocument": map[string]any{"uri": "file://" + cardPath}, "position": map[string]any{"line": 0, "character": 1}, "newName": "Panel"})
	if !strings.Contains(string(raw), "card.story.ktml") || strings.Count(string(raw), `"newText":"Panel"`) != 3 {
		t.Errorf("rename = %s, want the declaration, the story's import, and its header", raw)
	}
}
