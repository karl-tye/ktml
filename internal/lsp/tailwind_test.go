package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

// tailwindClient drives ServeWithAdapters as an editor would.
type tailwindClient struct {
	t        *testing.T
	input    *io.PipeWriter
	messages chan map[string]any
	nextID   int
	// pending holds notifications read while waiting for a response.
	pending []map[string]any
	// latest holds the last diagnostics published for each URI that
	// awaitDiagnostics has read.
	latest map[string][]any
}

func startTailwindHost(t *testing.T, adapter Adapter) *tailwindClient {
	t.Helper()
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ServeWithAdapters(ctx, inputReader, outputWriter, nil, ConfigReport{}, []Adapter{adapter})
		_ = outputWriter.Close()
	}()
	client := &tailwindClient{t: t, input: inputWriter, messages: make(chan map[string]any, 64)}
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
		_ = adapter.Close()
	})
	return client
}

func (c *tailwindClient) send(message map[string]any) {
	c.t.Helper()
	message["jsonrpc"] = "2.0"
	payload, _ := json.Marshal(message)
	if _, err := fmt.Fprintf(c.input, "Content-Length: %d\r\n\r\n%s", len(payload), payload); err != nil {
		c.t.Fatal(err)
	}
}

func (c *tailwindClient) request(method string, params map[string]any) map[string]any {
	c.t.Helper()
	c.nextID++
	id := c.nextID
	c.send(map[string]any{"id": id, "method": method, "params": params})
	for {
		message := c.next()
		if identifier, ok := message["id"].(float64); ok && int(identifier) == id {
			return message
		}
		c.pending = append(c.pending, message)
	}
}

func (c *tailwindClient) next() map[string]any {
	c.t.Helper()
	select {
	case message, ok := <-c.messages:
		if !ok {
			c.t.Fatal("server closed its output")
		}
		return message
	case <-time.After(10 * time.Second):
		c.t.Fatal("timed out waiting for the server")
	}
	return nil
}

// notification returns the next notification of method that satisfies
// accept, skipping others.
func (c *tailwindClient) notification(method string, accept func(map[string]any) bool) map[string]any {
	c.t.Helper()
	for index, message := range c.pending {
		if message["method"] == method && accept(message) {
			c.pending = append(c.pending[:index], c.pending[index+1:]...)
			return message
		}
	}
	for {
		message := c.next()
		if message["method"] == method && accept(message) {
			return message
		}
	}
}

func diagnosticCodes(message map[string]any) []string {
	params, _ := message["params"].(map[string]any)
	diagnostics, _ := params["diagnostics"].([]any)
	var codes []string
	for _, entry := range diagnostics {
		diagnostic, _ := entry.(map[string]any)
		codes = append(codes, fmt.Sprint(diagnostic["code"]))
	}
	return codes
}

const tailwindTestSource = "Card [color: string] {\n" +
	"\tdiv class=\"flex block bg-peach p-1\" {\n" +
	"\t\tp class=\"text-{color}-500 latte\" \"x\"\n" +
	"\t}\n" +
	"}\n"

func tailwindProject(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	css := "@import \"tailwindcss\";\n@theme {\n  --color-peach: #fe640b;\n}\n@utility latte {\n  --ctp-base: 1;\n  --ctp-peach: 2;\n}\n"
	if err := os.WriteFile(filepath.Join(root, "site.css"), []byte(css), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "ui", "card.ktml")
	if err := os.WriteFile(path, []byte(tailwindTestSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, (&url.URL{Scheme: "file", Path: path}).String()
}

func fakeTailwindAdapter(t *testing.T, root string) *TailwindAdapter {
	t.Helper()
	t.Setenv("KTML_TAILWIND_HELPER", "1")
	return NewTailwindAdapter(root, &config.Tailwind{
		Command:  []string{os.Args[0], "-test.run=^TestTailwindHelperProcess$", "--"},
		Settings: map[string]any{"lint": map[string]any{"cssConflict": "warning"}},
	})
}

func openTailwindDocument(client *tailwindClient, uri string) {
	client.send(map[string]any{"method": "initialize", "id": 0, "params": map[string]any{}})
	client.send(map[string]any{"method": "initialized", "params": map[string]any{}})
	client.send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "ktml", "version": 1, "text": tailwindTestSource}}})
}

func position(line, character int) map[string]any {
	return map[string]any{"line": line, "character": character}
}

func TestTailwindAdapter_GIVENAKTMLDocument_WHENOpened_THENPublishesDynamicClassesAndTheServersDiagnosticsInClassLists(t *testing.T) {
	root, uri := tailwindProject(t)
	client := startTailwindHost(t, fakeTailwindAdapter(t, root))
	openTailwindDocument(client, uri)

	first := client.notification(publishDiagnosticsMethod, func(map[string]any) bool { return true })
	if got := strings.Join(diagnosticCodes(first), ","); got != "tailwind-dynamic-class" {
		t.Errorf("first diagnostics = %s, want the native dynamic class only", got)
	}
	// The fake server reports cssConflict on flex, a diagnostic outside any
	// class list, which the adapter drops, and an interpolation it saw.
	merged := client.notification(publishDiagnosticsMethod, func(message map[string]any) bool {
		return len(diagnosticCodes(message)) > 1
	})
	if got := strings.Join(diagnosticCodes(merged), ","); got != "tailwind-dynamic-class,cssConflict" {
		t.Errorf("merged diagnostics = %s, want tailwind-dynamic-class,cssConflict", got)
	}
	params := merged["params"].(map[string]any)
	dynamic := params["diagnostics"].([]any)[0].(map[string]any)
	if dynamic["severity"] != float64(2) || !strings.Contains(fmt.Sprint(dynamic["message"]), "text-{color}-500") {
		t.Errorf("dynamic diagnostic = %v", dynamic)
	}
	conflict := params["diagnostics"].([]any)[1].(map[string]any)
	if got := fmt.Sprint(conflict["range"]); got != fmt.Sprint(map[string]any{"start": position(1, 12), "end": position(1, 16)}) {
		t.Errorf("cssConflict range = %s, want 1:12-1:16 (flex)", got)
	}
}

func TestTailwindAdapter_GIVENACursorInAClassList_WHENCompleting_THENReturnsTheServersMatchingItemsWithoutCommands(t *testing.T) {
	root, uri := tailwindProject(t)
	client := startTailwindHost(t, fakeTailwindAdapter(t, root))
	openTailwindDocument(client, uri)

	// After "p-1" in the div's class list.
	response := client.request("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position(1, 35)})
	result, _ := response["result"].(map[string]any)
	items, _ := result["items"].([]any)
	if len(items) != 111 {
		t.Fatalf("items = %d, want the 111 classes starting with p-1", len(items))
	}
	for _, entry := range items {
		item := entry.(map[string]any)
		if !strings.HasPrefix(fmt.Sprint(item["label"]), "p-1") || item["command"] != nil {
			t.Fatalf("item = %v, want a p-1 class without a command", item)
		}
	}
	if result["isIncomplete"] != false {
		t.Errorf("isIncomplete = %v, want false", result["isIncomplete"])
	}

	// Outside class lists the server is not asked.
	response = client.request("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position(0, 2)})
	if response["result"] != nil {
		t.Errorf("completion outside a class list = %v, want null", response["result"])
	}
}

func TestTailwindAdapter_GIVENAnEmptyWord_WHENCompleting_THENTruncatesToTheLimitAndAsksAgain(t *testing.T) {
	root, uri := tailwindProject(t)
	client := startTailwindHost(t, fakeTailwindAdapter(t, root))
	openTailwindDocument(client, uri)

	// After "flex " in the div's class list.
	response := client.request("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position(1, 17)})
	result, _ := response["result"].(map[string]any)
	items, _ := result["items"].([]any)
	if len(items) != tailwindCompletionLimit || result["isIncomplete"] != true {
		t.Fatalf("items = %d, isIncomplete = %v; want %d and true", len(items), result["isIncomplete"], tailwindCompletionLimit)
	}
}

func TestTailwindAdapter_GIVENAThemeClass_WHENHoveredAndDefined_THENForwardsHoverAndNavigatesToTheTheme(t *testing.T) {
	root, uri := tailwindProject(t)
	client := startTailwindHost(t, fakeTailwindAdapter(t, root))
	openTailwindDocument(client, uri)
	css := (&url.URL{Scheme: "file", Path: filepath.Join(root, "site.css")}).String()

	hover := client.request("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position(1, 25)})
	contents := fmt.Sprint(hover["result"])
	if !strings.Contains(contents, "var(--color-peach)") || !strings.Contains(contents, "ktml=html") {
		t.Errorf("hover = %s, want the server's CSS, configured for KTML as HTML", contents)
	}

	definition := client.request("textDocument/definition", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position(1, 25)})
	want := fmt.Sprint([]any{map[string]any{"uri": css, "range": map[string]any{"start": position(2, 2), "end": position(2, 15)}}})
	if got := fmt.Sprint(definition["result"]); got != want {
		t.Errorf("definition of bg-peach = %s, want %s", got, want)
	}

	// latte is a custom @utility.
	definition = client.request("textDocument/definition", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position(2, 29)})
	want = fmt.Sprint([]any{map[string]any{"uri": css, "range": map[string]any{"start": position(4, 9), "end": position(4, 14)}}})
	if got := fmt.Sprint(definition["result"]); got != want {
		t.Errorf("definition of latte = %s, want %s", got, want)
	}
}

func TestTailwindAdapter_GIVENServerCodeActions_WHENRequested_THENKeepsOnlyEditsInsideClassLists(t *testing.T) {
	root, uri := tailwindProject(t)
	client := startTailwindHost(t, fakeTailwindAdapter(t, root))
	openTailwindDocument(client, uri)

	response := client.request("textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"range":        map[string]any{"start": position(1, 12), "end": position(1, 16)},
		"context":      map[string]any{"diagnostics": []any{}},
	})
	actions, _ := response["result"].([]any)
	if len(actions) != 1 || actions[0].(map[string]any)["title"] != "Delete 'flex'" {
		t.Errorf("actions = %v, want only the edit inside the class list", actions)
	}
}

func TestTailwindAdapter_GIVENAQuery_WHENSearchingWorkspaceSymbols_THENReturnsClassTokensFromOpenAndUnopenedFiles(t *testing.T) {
	root, uri := tailwindProject(t)
	other := filepath.Join(root, "ui", "other.ktml")
	if err := os.WriteFile(other, []byte("Other [] {\n\tspan class=\"bg-peach/30\" \"y\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := startTailwindHost(t, fakeTailwindAdapter(t, root))
	openTailwindDocument(client, uri)

	response := client.request("workspace/symbol", map[string]any{"query": ".peach"})
	symbols, _ := response["result"].([]any)
	var got []string
	for _, entry := range symbols {
		symbol := entry.(map[string]any)
		location := symbol["location"].(map[string]any)
		got = append(got, fmt.Sprintf("%s@%s", symbol["name"], filepath.Base(fmt.Sprint(location["uri"]))))
	}
	if want := ".bg-peach@card.ktml .bg-peach/30@other.ktml"; strings.Join(got, " ") != want {
		t.Errorf("symbols = %q, want %q", strings.Join(got, " "), want)
	}
	response = client.request("workspace/symbol", map[string]any{"query": ""})
	if response["result"] != nil {
		t.Errorf("empty query = %v, want null", response["result"])
	}
}

func TestTailwindAdapter_GIVENAMissingServer_WHENADocumentOpens_THENWarnsOnceAndKeepsKTMLChecks(t *testing.T) {
	root, uri := tailwindProject(t)
	adapter := NewTailwindAdapter(root, &config.Tailwind{Command: []string{filepath.Join(root, "no-such-tailwindcss-language-server"), "--stdio"}})
	client := startTailwindHost(t, adapter)
	openTailwindDocument(client, uri)

	diagnostics := client.notification(publishDiagnosticsMethod, func(map[string]any) bool { return true })
	if got := strings.Join(diagnosticCodes(diagnostics), ","); got != "tailwind-dynamic-class" {
		t.Errorf("diagnostics = %s, want tailwind-dynamic-class", got)
	}
	warning := client.notification("window/showMessage", func(map[string]any) bool { return true })
	message := fmt.Sprint(warning["params"].(map[string]any)["message"])
	if !strings.Contains(message, "Tailwind CSS language server unavailable") || !strings.Contains(message, "@tailwindcss/language-server@"+TailwindPinnedVersion) {
		t.Errorf("warning = %q", message)
	}
	completion := client.request("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position(1, 35)})
	if completion["result"] != nil {
		t.Errorf("completion without a server = %v, want null", completion["result"])
	}
	symbols := client.request("workspace/symbol", map[string]any{"query": "peach"})
	if list, _ := symbols["result"].([]any); len(list) != 1 {
		t.Errorf("symbols without a server = %v, want bg-peach", symbols["result"])
	}
}

// TestTailwindHelperProcess is a fake Tailwind CSS language server.
func TestTailwindHelperProcess(t *testing.T) {
	if os.Getenv("KTML_TAILWIND_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	write := func(message map[string]any) {
		message["jsonrpc"] = "2.0"
		payload, _ := json.Marshal(message)
		fmt.Printf("Content-Length: %d\r\n\r\n%s", len(payload), payload)
	}
	ktmlLanguage := "unset"
	for {
		payload, err := readChildFrame(reader)
		if err != nil {
			os.Exit(0)
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		_ = json.Unmarshal(payload, &message)
		switch message.Method {
		case "initialize":
			// The configuration request goes first, so its answer arrives
			// before any request the tests send.
			write(map[string]any{"id": 900, "method": "workspace/configuration", "params": map[string]any{"items": []any{map[string]any{"section": "tailwindCSS"}}}})
			write(map[string]any{"id": message.ID, "result": map[string]any{"capabilities": map[string]any{}}})
		case "textDocument/didOpen", "textDocument/didChange":
			var params struct {
				TextDocument struct {
					URI  string `json:"uri"`
					Text string `json:"text"`
				} `json:"textDocument"`
				ContentChanges []struct {
					Text string `json:"text"`
				} `json:"contentChanges"`
			}
			_ = json.Unmarshal(message.Params, &params)
			text := params.TextDocument.Text
			if len(params.ContentChanges) > 0 {
				text = params.ContentChanges[0].Text
			}
			diagnostics := []any{
				map[string]any{"range": map[string]any{"start": position(0, 0), "end": position(0, 4)}, "code": "outside", "message": "outside class lists"},
			}
			for lineNumber, line := range strings.Split(text, "\n") {
				if index := strings.Index(line, "flex"); index >= 0 && strings.Contains(line, "block") {
					diagnostics = append(diagnostics, map[string]any{"range": map[string]any{"start": position(lineNumber, index), "end": position(lineNumber, index+4)}, "code": "cssConflict", "source": "tailwindcss", "message": "'flex' applies the same CSS properties as 'block'."})
				}
				if index := strings.IndexAny(line, "{}"); index >= 0 {
					diagnostics = append(diagnostics, map[string]any{"range": map[string]any{"start": position(lineNumber, index), "end": position(lineNumber, index+1)}, "code": "brace", "message": "the virtual document kept a brace"})
				}
			}
			write(map[string]any{"method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": params.TextDocument.URI, "diagnostics": diagnostics}})
		case "textDocument/completion":
			var items []any
			for index := range 1000 {
				items = append(items, map[string]any{"label": fmt.Sprintf("p-%d", index), "sortText": fmt.Sprintf("%04d", index), "command": map[string]any{"command": "editor.action.triggerSuggest"}})
			}
			items = append(items, map[string]any{"label": "bg-peach", "sortText": "9999"})
			write(map[string]any{"id": message.ID, "result": map[string]any{"isIncomplete": false, "items": items}})
		case "textDocument/hover":
			var params struct {
				Position lspPosition `json:"position"`
			}
			_ = json.Unmarshal(message.Params, &params)
			css := ".latte {\n  --ctp-base: 1;\n}"
			if params.Position.Line == 1 {
				css = ".bg-peach {\n  background-color: var(--color-peach) /* rgba(var(--ctp-peach)) ktml=" + ktmlLanguage + " */;\n}"
			}
			write(map[string]any{"id": message.ID, "result": map[string]any{"contents": map[string]any{"kind": "markdown", "value": "```css\n" + css + "\n```"}}})
		case "textDocument/codeAction":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(message.Params, &params)
			edit := func(start, end map[string]any) map[string]any {
				return map[string]any{"changes": map[string]any{params.TextDocument.URI: []any{map[string]any{"range": map[string]any{"start": start, "end": end}, "newText": ""}}}}
			}
			write(map[string]any{"id": message.ID, "result": []any{
				map[string]any{"title": "Delete 'flex'", "kind": "quickfix", "edit": edit(position(1, 12), position(1, 17))},
				map[string]any{"title": "Outside", "kind": "quickfix", "edit": edit(position(0, 0), position(0, 4))},
			}})
		case "shutdown":
			write(map[string]any{"id": message.ID, "result": nil})
		case "exit":
			os.Exit(0)
		case "":
			// The configuration response.
			var results []struct {
				IncludeLanguages map[string]string `json:"includeLanguages"`
				Lint             map[string]string `json:"lint"`
			}
			if json.Unmarshal(message.Result, &results) == nil && len(results) == 1 && results[0].Lint["cssConflict"] == "warning" {
				ktmlLanguage = results[0].IncludeLanguages["ktml"]
			}
		default:
			if len(message.ID) > 0 {
				write(map[string]any{"id": message.ID, "result": nil})
			}
		}
	}
}

func TestTailwindAdapter_GIVENTheSymbolIndex_WHENAClassIsReferenced_THENEveryDocumentThatWritesItAnswers(t *testing.T) {
	root, uri := tailwindProject(t)
	other := filepath.Join(root, "ui", "badge.ktml")
	if err := os.WriteFile(other, []byte("Badge {\n\tspan class=\"p-1 bg-peach\" \"b\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := startTailwindHost(t, fakeTailwindAdapter(t, root))
	openTailwindDocument(client, uri)

	references := client.request("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position(1, 25), "context": map[string]any{"includeDeclaration": true}})
	if got := strings.Join(spans(t, references["result"]), " "); got != "badge.ktml:1:17 card.ktml:1:23" {
		t.Errorf("references of bg-peach = %s", got)
	}
	highlights := client.request("textDocument/documentHighlight", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position(1, 35)})
	if got := strings.Join(spans(t, highlights["result"]), " "); got != ".:1:32" {
		t.Errorf("highlights of p-1 = %s", got)
	}
}
