package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/textposition"
	"github.com/karl-tye/ktml/internal/wasm"
)

var (
	builtPluginsMu sync.Mutex
	builtPlugins   = make(map[string][]byte)
)

// firstPartyPlugin builds cmd/plugins/<name> once per test binary.
func firstPartyPlugin(t *testing.T, name string) []byte {
	t.Helper()
	builtPluginsMu.Lock()
	defer builtPluginsMu.Unlock()
	if wasmBytes, ok := builtPlugins[name]; ok {
		return wasmBytes
	}
	out := filepath.Join(t.TempDir(), name+".wasm")
	command := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, ".")
	command.Dir = filepath.Join("..", "..", "cmd", "plugins", name)
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s plugin: %v\n%s", name, err, output)
	}
	wasmBytes, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	builtPlugins[name] = wasmBytes
	return wasmBytes
}

// startProjectHost serves the first-party plugins over a project, as
// ktml lsp does from its root.
func startProjectHost(t *testing.T, dir string, names ...string) *tailwindClient {
	t.Helper()
	return startConfiguredHost(t, dir, nil, names...)
}

// startConfiguredHost is startProjectHost with plugin settings by name.
func startConfiguredHost(t *testing.T, dir string, settings map[string]map[string]any, names ...string) *tailwindClient {
	t.Helper()
	ctx := context.Background()
	var plugins []*wasm.Plugin
	for _, name := range names {
		plugin, err := wasm.NewPluginWithOptions(ctx, firstPartyPlugin(t, name), wasm.Options{AllowRead: []string{dir}, WorkDir: dir, Name: name, Settings: settings[name]})
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		t.Cleanup(func() { _ = plugin.Close(ctx) })
		plugins = append(plugins, plugin)
	}
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	serveCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ServeWithAdapters(serveCtx, inputReader, outputWriter, plugins, ConfigReport{}, nil)
		_ = outputWriter.Close()
	}()
	client := &tailwindClient{t: t, input: inputWriter, messages: make(chan map[string]any, 256)}
	go func() {
		reader := bufio.NewReader(outputReader)
		for {
			payload, err := readChildFrame(reader)
			if err != nil {
				close(client.messages)
				return
			}
			var message map[string]any
			if json.Unmarshal(payload, &message) == nil {
				client.messages <- message
			}
		}
	}()
	t.Cleanup(func() {
		_ = inputWriter.Close()
		cancel()
		<-done
	})
	client.request("initialize", map[string]any{"capabilities": map[string]any{}})
	client.send(map[string]any{"method": "initialized", "params": map[string]any{}})
	return client
}

func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, source := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// at returns the position of marker in source, plus shift bytes.
func at(t *testing.T, source, marker string, shift int) map[string]any {
	t.Helper()
	offset := strings.Index(source, marker)
	if offset < 0 {
		t.Fatalf("marker %q not found", marker)
	}
	line, character := textposition.Position([]byte(source), offset+shift)
	return map[string]any{"line": line, "character": character}
}

// spans renders locations or highlights as file:line:character, for
// comparison.
func spans(t *testing.T, result any) []string {
	t.Helper()
	var entries []any
	switch value := result.(type) {
	case []any:
		entries = value
	case map[string]any:
		entries = []any{value}
	}
	var rendered []string
	for _, entry := range entries {
		object, _ := entry.(map[string]any)
		uri, _ := object["uri"].(string)
		r, _ := object["range"].(map[string]any)
		start, _ := r["start"].(map[string]any)
		line, _ := start["line"].(float64)
		character, _ := start["character"].(float64)
		rendered = append(rendered, filepath.Base(uri)+":"+strconv.Itoa(int(line))+":"+strconv.Itoa(int(character)))
	}
	return rendered
}

const (
	indexCard = "Card [title: string] {\n\th2 \"{title}\"\n}\n"
	indexPage = "import { Card } from \"./ui/card.ktml\"\n\nPage {\n\tp \"Card\"\n\tCard title=\"a\"\n\tCard title=\"b\"\n}\n"
)

func indexProject(t *testing.T) (dir, cardURI, pageURI string) {
	t.Helper()
	dir = writeProject(t, map[string]string{"ktml.config.json": "{}", "ui/card.ktml": indexCard, "page.ktml": indexPage})
	return dir, "file://" + filepath.Join(dir, "ui", "card.ktml"), "file://" + filepath.Join(dir, "page.ktml")
}

func TestServe_GIVENTheSymbolIndex_WHENNavigatingAcrossDirectories_THENAnswersFromSemanticSymbols(t *testing.T) {
	_, cardURI, pageURI := indexProject(t)
	client := startProjectHost(t, filepath.Dir(strings.TrimPrefix(pageURI, "file://")), "gohtml")
	document := func(uri string) map[string]any { return map[string]any{"uri": uri} }

	definition := client.request("textDocument/definition", map[string]any{"textDocument": document(pageURI), "position": at(t, indexPage, "\tCard title=\"a\"", 2)})
	if got := strings.Join(spans(t, definition["result"]), " "); got != "card.ktml:0:0" {
		t.Errorf("definition = %s, want the declaration", got)
	}
	references := client.request("textDocument/references", map[string]any{"textDocument": document(cardURI), "position": map[string]any{"line": 0, "character": 1}, "context": map[string]any{"includeDeclaration": true}})
	if got := strings.Join(spans(t, references["result"]), " "); got != "card.ktml:0:0 page.ktml:0:9 page.ktml:4:1 page.ktml:5:1" {
		t.Errorf("references = %s", got)
	}
	highlights := client.request("textDocument/documentHighlight", map[string]any{"textDocument": document(pageURI), "position": at(t, indexPage, "\tCard title=\"b\"", 1)})
	if got := strings.Join(spans(t, highlights["result"]), " "); got != ".:0:9 .:4:1 .:5:1" {
		t.Errorf("highlights = %s, want the import and both calls, not the text", got)
	}
	inputHighlights := client.request("textDocument/documentHighlight", map[string]any{"textDocument": document(cardURI), "position": at(t, indexCard, "title:", 1)})
	if got := strings.Join(spans(t, inputHighlights["result"]), " "); got != ".:0:6 .:1:6" {
		t.Errorf("input highlights = %s", got)
	}
	for _, method := range []string{"textDocument/definition", "textDocument/references", "textDocument/documentHighlight"} {
		response := client.request(method, map[string]any{"textDocument": document(pageURI), "position": at(t, indexPage, "\"Card\"", 2), "context": map[string]any{"includeDeclaration": true}})
		if got := spans(t, response["result"]); len(got) != 0 {
			t.Errorf("%s in a text literal = %v, want nothing", method, got)
		}
	}
}

func TestServe_GIVENTheSymbolIndex_WHENADocumentChanges_THENItsSymbolsAndItsImportersAreReindexed(t *testing.T) {
	_, cardURI, pageURI := indexProject(t)
	client := startProjectHost(t, filepath.Dir(strings.TrimPrefix(pageURI, "file://")), "gohtml")
	client.send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": pageURI, "version": 1, "languageId": "ktml", "text": indexPage}}})
	edited := strings.Replace(indexPage, "\tCard title=\"b\"\n", "", 1)
	client.send(map[string]any{"method": "textDocument/didChange", "params": map[string]any{"textDocument": map[string]any{"uri": pageURI, "version": 2}, "contentChanges": []any{map[string]any{"text": edited}}}})

	references := client.request("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": cardURI}, "position": map[string]any{"line": 0, "character": 1}, "context": map[string]any{"includeDeclaration": false}})
	if got := strings.Join(spans(t, references["result"]), " "); got != "page.ktml:0:9 page.ktml:4:1" {
		t.Errorf("references after the edit = %s", got)
	}
}

func TestServe_GIVENTheSymbolIndex_WHENAskedForCallHierarchy_THENFollowsCallGraphEdges(t *testing.T) {
	_, cardURI, pageURI := indexProject(t)
	client := startProjectHost(t, filepath.Dir(strings.TrimPrefix(pageURI, "file://")), "gohtml")
	prepared := client.request("textDocument/prepareCallHierarchy", map[string]any{"textDocument": map[string]any{"uri": cardURI}, "position": map[string]any{"line": 0, "character": 1}})
	items, _ := prepared["result"].([]any)
	if len(items) != 1 {
		t.Fatalf("prepareCallHierarchy = %v", prepared)
	}
	incoming := client.request("callHierarchy/incomingCalls", map[string]any{"item": items[0]})
	calls, _ := incoming["result"].([]any)
	if len(calls) != 1 {
		t.Fatalf("incomingCalls = %v", incoming)
	}
	call, _ := calls[0].(map[string]any)
	from, _ := call["from"].(map[string]any)
	if from["name"] != "Page" || len(call["fromRanges"].([]any)) != 2 {
		t.Errorf("incoming call = %v, want Page with its two call sites", call)
	}
	outgoing := client.request("callHierarchy/outgoingCalls", map[string]any{"item": from})
	calls, _ = outgoing["result"].([]any)
	if len(calls) != 1 || calls[0].(map[string]any)["to"].(map[string]any)["name"] != "Card" {
		t.Errorf("outgoingCalls = %v, want Card", outgoing)
	}
}

func TestServe_GIVENTheSymbolIndex_WHENAFileIsDeletedOnDisk_THENItsSymbolsLeaveTheIndex(t *testing.T) {
	dir, cardURI, pageURI := indexProject(t)
	client := startProjectHost(t, dir, "gohtml")
	references := client.request("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": cardURI}, "position": map[string]any{"line": 0, "character": 1}, "context": map[string]any{"includeDeclaration": false}})
	if len(spans(t, references["result"])) != 3 {
		t.Fatalf("references before the deletion = %v", references["result"])
	}
	if err := os.Remove(strings.TrimPrefix(pageURI, "file://")); err != nil {
		t.Fatal(err)
	}
	client.send(map[string]any{"method": "workspace/didChangeWatchedFiles", "params": map[string]any{"changes": []any{map[string]any{"uri": pageURI, "type": 3}}}})
	references = client.request("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": cardURI}, "position": map[string]any{"line": 0, "character": 1}, "context": map[string]any{"includeDeclaration": false}})
	if got := spans(t, references["result"]); len(got) != 0 {
		t.Errorf("references after the deletion = %v, want none", got)
	}
}

func TestServe_GIVENSeveralProviders_WHENARouteIsReferencedFromKTML_THENTheIndexJoinsGoAndEveryDocument(t *testing.T) {
	router := "package web\n\nimport \"github.com/go-chi/chi/v5\"\n\nfunc Routes(r chi.Router) {\n\tr.Get(\"/enquiries\", list)\n}\n"
	panel := "Panel {\n\tdiv hx-get=\"/enquiries\" x-data=`{ open: false }` {\n\t\tbutton @click=\"open = !open\" \"t\"\n\t}\n}\n"
	badge := "Badge {\n\ta hx-get=\"/enquiries\" \"e\"\n}\n"
	dir := writeProject(t, map[string]string{"ktml.config.json": "{}", "router.go": router, "panel.ktml": panel, "badge.ktml": badge})
	client := startConfiguredHost(t, dir, map[string]map[string]any{"htmx": {"chi": map[string]any{"dir": dir}}}, "gohtml", "htmx", "alpine")
	panelURI := "file://" + filepath.Join(dir, "panel.ktml")

	// The route scan is a background job: ask until its result is indexed.
	var got string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		references := client.request("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": panelURI}, "position": at(t, panel, "/enquiries", 2), "context": map[string]any{"includeDeclaration": true}})
		if got = strings.Join(spans(t, references["result"]), " "); strings.Contains(got, "router.go") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got != "router.go:5:7 badge.ktml:1:11 panel.ktml:1:13" {
		t.Errorf("route references = %s, want both attributes and the registration", got)
	}
	highlights := client.request("textDocument/documentHighlight", map[string]any{"textDocument": map[string]any{"uri": panelURI}, "position": at(t, panel, "open:", 1)})
	if got := strings.Join(spans(t, highlights["result"]), " "); got != ".:1:35 .:2:17 .:2:25" {
		t.Errorf("Alpine highlights = %s", got)
	}
}
