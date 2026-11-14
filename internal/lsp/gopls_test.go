package lsp

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

// requireGopls skips a test when gopls is not installed. The tests are
// written against GoplsPinnedVersion; another version only logs a note.
func requireGopls(t *testing.T) {
	t.Helper()
	path, err := exec.LookPath("gopls")
	if err != nil {
		t.Skipf("gopls is not installed; install it with go install golang.org/x/tools/gopls@%s", GoplsPinnedVersion)
	}
	if output, err := exec.Command(path, "version").Output(); err == nil && !strings.Contains(string(output), GoplsPinnedVersion) {
		t.Logf("gopls %s is installed; these tests are pinned to %s", strings.TrimSpace(string(output)), GoplsPinnedVersion)
	}
}

// writeGoModule writes files into a new module that replaces
// github.com/karl-tye/ktml with this repository.
func writeGoModule(t *testing.T, module string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	files["go.mod"] = "module " + module + "\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0\n\nreplace github.com/karl-tye/ktml => " + filepath.ToSlash(repository) + "\n"
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const goplsSchema = `record CardView [
	title: string,
	count: integer,
]
`

const goplsComponents = `import { CardView } from "./view.ktmlschema"

Card [view: CardView] {
	article {
		h2 "{view.title}"
		p "{view.count}"
	}
}
`

const goplsPresenter = `package views

// NewCardView presents a card.
func NewCardView(title string, count int) CardView {
	var v CardView
	v.Title = title
	v.Count = int64(count)
	return v
}
`

func viewsModule(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"views/view.ktmlschema": goplsSchema,
		"views/view.ktml":       goplsComponents,
		"views/present.go":      goplsPresenter,
	}
	for name, content := range extra {
		files[name] = content
	}
	return writeGoModule(t, "example.com/app", files)
}

// goplsHost serves the gopls adapter alone, as ktml lsp does beside the
// plugins.
func goplsHost(t *testing.T, root string, command []string) (*tailwindClient, *GoplsAdapter) {
	t.Helper()
	return goplsHostWith(t, root, &config.Gopls{Command: command})
}

func (c *tailwindClient) open(path string) {
	c.t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		c.t.Fatal(err)
	}
	c.openText(path, 1, string(text))
}

func (c *tailwindClient) openText(path string, version int, text string) {
	c.send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "languageId": "ktml", "version": version, "text": text}}})
}

func (c *tailwindClient) change(path string, version int, text string) {
	c.send(map[string]any{"method": "textDocument/didChange", "params": map[string]any{"textDocument": map[string]any{"uri": fileURI(path), "version": version}, "contentChanges": []any{map[string]any{"text": text}}}})
}

// awaitDiagnostics waits up to a minute, which a cold gopls may need, for
// the last diagnostics published for path to satisfy accept, and returns
// them. It records every publish it reads, for any URI.
func (c *tailwindClient) awaitDiagnostics(path string, accept func([]any) bool) []any {
	c.t.Helper()
	if c.latest == nil {
		c.latest = make(map[string][]any)
	}
	record := func(message map[string]any) bool {
		if message["method"] != publishDiagnosticsMethod {
			return false
		}
		params, _ := message["params"].(map[string]any)
		uri, _ := params["uri"].(string)
		diagnostics, _ := params["diagnostics"].([]any)
		c.latest[uri] = diagnostics
		return true
	}
	kept := c.pending[:0]
	for _, message := range c.pending {
		if !record(message) {
			kept = append(kept, message)
		}
	}
	c.pending = kept
	uri := fileURI(path)
	deadline := time.After(time.Minute)
	for {
		if diagnostics, ok := c.latest[uri]; ok && accept(diagnostics) {
			return diagnostics
		}
		select {
		case message, ok := <-c.messages:
			if !ok {
				c.t.Fatal("server closed its output")
			}
			if !record(message) {
				c.pending = append(c.pending, message)
			}
		case <-deadline:
			c.t.Fatalf("timed out waiting for diagnostics of %s; last: %v", filepath.Base(path), c.latest[uri])
		}
	}
}

// diagnosticAt returns the diagnostic whose message contains text.
func diagnosticAt(diagnostics []any, text string) (map[string]any, bool) {
	for _, value := range diagnostics {
		diagnostic, _ := value.(map[string]any)
		if message, _ := diagnostic["message"].(string); strings.Contains(message, text) {
			return diagnostic, true
		}
	}
	return nil, false
}

func hasDiagnostic(text string) func([]any) bool {
	return func(diagnostics []any) bool {
		_, ok := diagnosticAt(diagnostics, text)
		return ok
	}
}

func rangeStart(diagnostic map[string]any) (int, int) {
	start := diagnostic["range"].(map[string]any)["start"].(map[string]any)
	return int(start["line"].(float64)), int(start["character"].(float64))
}

func TestGopls_GIVENAGoTypeErrorInGeneratedCode_WHENADocumentIsOpen_THENItIsReportedOnTheKTMLRange(t *testing.T) {
	requireGopls(t)
	tests := []struct {
		name     string
		extra    map[string]string
		document string
		message  string
		line     int
		column   int
	}{
		{
			name:     "a Go type redeclares a component",
			extra:    map[string]string{"views/card.go": "package views\n\ntype Card struct{}\n"},
			document: "views/view.ktml",
			message:  "Card redeclared in this block",
			line:     2, column: 0,
		},
		{
			name:     "a presenter method makes a field a method value",
			extra:    map[string]string{"views/title.go": "package views\n\nfunc (v CardView) Title() string { return \"\" }\n"},
			document: "views/view.ktml",
			message:  "cannot use props.View.Title (value of type func() string) as string value",
			line:     4, column: 6,
		},
		{
			name:     "a presenter method collides with a record field",
			extra:    map[string]string{"views/title.go": "package views\n\nfunc (v CardView) Title() string { return \"\" }\n"},
			document: "views/view.ktmlschema",
			message:  "field and method with the same name Title (title.go:3:19)",
			line:     1, column: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := viewsModule(t, tt.extra)
			client, _ := goplsHost(t, root, nil)
			client.open(filepath.Join(root, "views", "view.ktml"))
			diagnostics := client.awaitDiagnostics(filepath.Join(root, filepath.FromSlash(tt.document)), hasDiagnostic(tt.message))
			diagnostic, _ := diagnosticAt(diagnostics, tt.message)
			if line, column := rangeStart(diagnostic); line != tt.line || column != tt.column {
				t.Errorf("diagnostic %q starts at %d:%d; want %d:%d", tt.message, line, column, tt.line, tt.column)
			}
			if diagnostic["source"] != goplsSource || diagnostic["severity"] != float64(1) {
				t.Errorf("diagnostic = %v; want an error from gopls", diagnostic)
			}
		})
	}
}

// TestGopls_GIVENEditedKTML_WHENRegenerated_THENDiagnosticsFollowTheEditAndStaleMapsAreNotUsed
// edits the document under a reported error: a document that stops
// compiling withdraws the mapped diagnostics, and a later valid edit maps
// them through the new source map, on the moved line.
func TestGopls_GIVENEditedKTML_WHENRegenerated_THENDiagnosticsFollowTheEditAndStaleMapsAreNotUsed(t *testing.T) {
	requireGopls(t)
	root := viewsModule(t, map[string]string{"views/card.go": "package views\n\ntype Card struct{}\n"})
	client, _ := goplsHost(t, root, nil)
	document := filepath.Join(root, "views", "view.ktml")
	client.open(document)
	diagnostics := client.awaitDiagnostics(document, hasDiagnostic("Card redeclared"))
	diagnostic, _ := diagnosticAt(diagnostics, "Card redeclared")
	if line, _ := rangeStart(diagnostic); line != 2 {
		t.Fatalf("diagnostic on line %d; want 2", line)
	}

	broken := strings.Replace(goplsComponents, "{view.title}", "{view.missing}", 1)
	client.change(document, 2, broken)
	client.awaitDiagnostics(document, func(diagnostics []any) bool { return len(diagnostics) == 0 })

	moved := "// A comment moves every declaration down two lines.\n\n" + goplsComponents
	client.change(document, 3, moved)
	diagnostics = client.awaitDiagnostics(document, hasDiagnostic("Card redeclared"))
	diagnostic, _ = diagnosticAt(diagnostics, "Card redeclared")
	if line, _ := rangeStart(diagnostic); line != 4 {
		t.Errorf("after the edit the diagnostic is on line %d; want 4", line)
	}

	// Removing the handwritten declaration clears the error.
	if err := os.Remove(filepath.Join(root, "views", "card.go")); err != nil {
		t.Fatal(err)
	}
	client.send(map[string]any{"method": "workspace/didChangeWatchedFiles", "params": map[string]any{"changes": []any{map[string]any{"uri": fileURI(filepath.Join(root, "views", "card.go")), "type": 3}}}})
	client.awaitDiagnostics(document, func(diagnostics []any) bool { return len(diagnostics) == 0 })
}

func locationsIn(t *testing.T, response map[string]any) []string {
	t.Helper()
	if response["error"] != nil {
		t.Fatalf("error response: %v", response["error"])
	}
	results, _ := response["result"].([]any)
	var locations []string
	for _, value := range results {
		location := value.(map[string]any)
		line, character := rangeStart(location)
		locations = append(locations, filepath.Base(strings.TrimPrefix(location["uri"].(string), "file://"))+":"+strconv.Itoa(line)+":"+strconv.Itoa(character))
	}
	return locations
}

func goplsPosition(uri string, line, character int) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": character}}
}

func TestGopls_GIVENAHostSchemaField_WHENNavigated_THENGoPresenterUsagesAreIncluded(t *testing.T) {
	requireGopls(t)
	root := viewsModule(t, nil)
	client, _ := goplsHost(t, root, nil)
	schema := filepath.Join(root, "views", "view.ktmlschema")
	document := filepath.Join(root, "views", "view.ktml")
	client.open(schema)
	client.open(document)

	// title is at view.ktmlschema 1:1 and view.title at view.ktml 4:13.
	references := goplsPosition(fileURI(schema), 1, 2)
	references["context"] = map[string]any{"includeDeclaration": false}
	if got := locationsIn(t, client.request("textDocument/references", references)); strings.Join(got, " ") != "present.go:5:3" {
		t.Errorf("references = %v; want the presenter's assignment present.go:5:3", got)
	}
	if got := locationsIn(t, client.request("textDocument/definition", goplsPosition(fileURI(document), 4, 15))); strings.Join(got, " ") != "present.go:5:3" {
		t.Errorf("definition = %v; want the presenter's assignment present.go:5:3", got)
	}
	// The record's Go uses: the presenter's signature and variable.
	record := goplsPosition(fileURI(schema), 0, 9)
	record["context"] = map[string]any{"includeDeclaration": false}
	if got := locationsIn(t, client.request("textDocument/references", record)); strings.Join(got, " ") != "present.go:3:42 present.go:4:7" {
		t.Errorf("record references = %v; want the presenter's signature and variable", got)
	}
}

func TestGopls_GIVENAHostSchemaFieldRename_WHENRenamed_THENGoPresenterCodeIsRenamed(t *testing.T) {
	requireGopls(t)
	root := viewsModule(t, nil)
	client, _ := goplsHost(t, root, nil)
	schema := filepath.Join(root, "views", "view.ktmlschema")
	client.open(schema)
	client.open(filepath.Join(root, "views", "view.ktml"))

	params := goplsPosition(fileURI(schema), 1, 2)
	params["newName"] = "heading"
	response := client.request("textDocument/rename", params)
	result, _ := response["result"].(map[string]any)
	changes, _ := result["changes"].(map[string]any)
	if len(changes) != 1 {
		t.Fatalf("rename changes = %v; want only the presenter", changes)
	}
	edits, _ := changes[fileURI(filepath.Join(root, "views", "present.go"))].([]any)
	if len(edits) != 1 || edits[0].(map[string]any)["newText"] != "Heading" {
		t.Fatalf("presenter edits = %v; want v.Title renamed to v.Heading", edits)
	}
	if line, character := rangeStart(edits[0].(map[string]any)); line != 5 || character != 3 {
		t.Errorf("edit at %d:%d; want 5:3", line, character)
	}

	// An invalid KTML name renames nothing, in KTML or Go.
	params["newName"] = "Heading"
	if response := client.request("textDocument/rename", params); response["result"] != nil {
		t.Errorf("rename to an invalid field name = %v; want null", response["result"])
	}
}

func TestGopls_GIVENNoGopls_WHENDocumentsAreEdited_THENKTMLWorksAndTheUserIsToldOnce(t *testing.T) {
	root := viewsModule(t, nil)
	client, _ := goplsHost(t, root, []string{"ktml-test-no-such-gopls"})
	schema := filepath.Join(root, "views", "view.ktmlschema")
	client.open(schema)
	client.open(filepath.Join(root, "views", "view.ktml"))
	message := client.notification("window/showMessage", func(map[string]any) bool { return true })
	text, _ := message["params"].(map[string]any)["message"].(string)
	if !strings.Contains(text, "gopls unavailable") || !strings.Contains(text, GoplsPinnedVersion) {
		t.Errorf("message = %q; want gopls unavailable with the pinned version", text)
	}
	references := goplsPosition(fileURI(schema), 1, 2)
	references["context"] = map[string]any{"includeDeclaration": false}
	if response := client.request("textDocument/references", references); response["result"] != nil {
		t.Errorf("references without gopls = %v; want null", response["result"])
	}
	params := goplsPosition(fileURI(schema), 1, 2)
	params["newName"] = "heading"
	if response := client.request("textDocument/rename", params); response["result"] != nil {
		t.Errorf("rename without gopls = %v; want null", response["result"])
	}
	warning := client.notification("window/showMessage", func(map[string]any) bool { return true })
	if text, _ := warning["params"].(map[string]any)["message"].(string); !strings.Contains(text, "Renamed field title in KTML only: gopls is unavailable") {
		t.Errorf("rename warning = %q", text)
	}
}

// copyFixture copies a testdata tree into a new module.
func copyFixture(t *testing.T, fixture, module string) string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(fixture, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(fixture, path)
		if err != nil {
			return err
		}
		if relative == "go.mod.txt" || relative == "README.md" {
			return nil
		}
		content, err := os.ReadFile(path)
		files[filepath.ToSlash(relative)] = string(content)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return writeGoModule(t, module, files)
}

// TestGopls_GIVENTheReviewsModule_WHENAPresenterIntroducesAGoTypeError_THENItShowsOnTheKTMLLine
// opens the reviews fixture's dashboard component and edits its presenter so that
// a record field becomes a method value: the generated text write no longer
// type checks, and the error shows on the .ktml line that renders the
// field, and on the field's schema declaration.
func TestGopls_GIVENTheReviewsModule_WHENAPresenterIntroducesAGoTypeError_THENItShowsOnTheKTMLLine(t *testing.T) {
	requireGopls(t)
	root := copyFixture(t, filepath.Join("testdata", "gopls", "reviews"), "example.com/larkspur")
	client, _ := goplsHost(t, root, nil)
	document := filepath.Join(root, "internal", "reviews", "ui.ktml")
	client.open(document)
	presenter := filepath.Join(root, "internal", "reviews", "ui_present.go")
	content, err := os.ReadFile(presenter)
	if err != nil {
		t.Fatal(err)
	}
	edited := string(content) + "\n// Month is a presenter helper that shadows the record field.\nfunc (v DashboardView) Month() string { return \"\" }\n"
	if err := os.WriteFile(presenter, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	client.send(map[string]any{"method": "workspace/didChangeWatchedFiles", "params": map[string]any{"changes": []any{map[string]any{"uri": fileURI(presenter), "type": 2}}}})
	// gopls may publish intermediate states while it loads; wait for the
	// one where the introduced error is the only kind left.
	onlyMonth := func(diagnostics []any) bool {
		for _, value := range diagnostics {
			if message, _ := value.(map[string]any)["message"].(string); !strings.Contains(message, "Month") {
				return false
			}
		}
		return hasDiagnostic("props.View.Month")(diagnostics)
	}
	diagnostics := client.awaitDiagnostics(document, onlyMonth)
	diagnostic, _ := diagnosticAt(diagnostics, "props.View.Month")
	source, _ := os.ReadFile(document)
	lines := strings.Split(string(source), "\n")
	line, character := rangeStart(diagnostic)
	if !strings.Contains(lines[line], "Reader Reviews - {view.month}") || !strings.HasPrefix(lines[line][character:], "{view.month}") {
		t.Errorf("diagnostic at %d:%d (%q); want {view.month} in the dashboard heading", line, character, lines[line])
	}
	schema := filepath.Join(root, "internal", "reviews", "reviews.ktmlschema")
	diagnostics = client.awaitDiagnostics(schema, hasDiagnostic("field and method with the same name Month (ui_present.go:"))
	diagnostic, _ = diagnosticAt(diagnostics, "field and method with the same name Month")
	schemaLines := strings.Split(string(must(os.ReadFile(schema))), "\n")
	line, character = rangeStart(diagnostic)
	if !strings.HasPrefix(schemaLines[line][character:], "month: string") {
		t.Errorf("schema diagnostic at %d:%d (%q); want the month field of DashboardView", line, character, schemaLines[line])
	}
}

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func TestGopls_GIVENAPathInKTML_WHENHovered_THENGoplsDescribesTheGoItGenerates(t *testing.T) {
	requireGopls(t)
	root := viewsModule(t, nil)
	client, _ := goplsHost(t, root, nil)
	schema := filepath.Join(root, "views", "view.ktmlschema")
	document := filepath.Join(root, "views", "view.ktml")
	client.open(schema)
	client.open(document)

	// {view.title} is at view.ktml 4:6; title at 4:12.
	hover := client.request("textDocument/hover", goplsPosition(fileURI(document), 4, 13))
	result, _ := hover["result"].(map[string]any)
	contents, _ := result["contents"].(map[string]any)
	value, _ := contents["value"].(string)
	if !strings.Contains(value, "Title string") {
		t.Fatalf("hover = %v, want gopls's description of the Title field", hover)
	}
	start := result["range"].(map[string]any)["start"].(map[string]any)
	if start["line"].(float64) != 4 || start["character"].(float64) != 12 {
		t.Errorf("hover range = %v, want the title part", result["range"])
	}

	// The root names the component's input: props.View.
	root4 := client.request("textDocument/hover", goplsPosition(fileURI(document), 4, 8))
	if value := jsonText(root4["result"]); !strings.Contains(value, "View CardView") {
		t.Errorf("hover on the root = %v, want the View field", root4)
	}

	// An edited buffer that no longer compiles has no current map.
	client.change(document, 2, strings.Replace(goplsComponents, "{view.count}", "{view.", 1))
	if stale := client.request("textDocument/hover", goplsPosition(fileURI(document), 4, 13)); stale["result"] != nil {
		t.Errorf("hover on a stale map = %v, want none", stale)
	}
}
