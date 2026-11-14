package e2e_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

// gateClient drives ktml lsp as an editor would: it keeps the last
// diagnostics published for each URI while it waits for responses.
type gateClient struct {
	t        *testing.T
	stdin    io.Writer
	messages chan map[string]any
	nextID   int

	mu          sync.Mutex
	diagnostics map[string][]map[string]any
}

func startGateClient(t *testing.T, dir string) *gateClient {
	t.Helper()
	binary, err := filepath.Abs("ktml")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "lsp")
	command.Dir = dir
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start ktml lsp: %v", err)
	}
	client := &gateClient{t: t, stdin: stdin, messages: make(chan map[string]any, 256), diagnostics: make(map[string][]map[string]any)}
	go func() {
		defer close(client.messages)
		reader := bufio.NewReader(stdout)
		for {
			message, err := readGateFrame(reader)
			if err != nil {
				return
			}
			client.messages <- message
		}
	}()
	t.Cleanup(func() {
		// shutdown and exit let the server stop its Tailwind server.
		client.send(map[string]any{"id": "shutdown", "method": "shutdown"})
		client.send(map[string]any{"method": "exit"})
		done := make(chan struct{})
		go func() { _ = command.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	})
	return client
}

func readGateFrame(reader *bufio.Reader) (map[string]any, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if value, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			if length, err = strconv.Atoi(strings.TrimSpace(value)); err != nil {
				return nil, err
			}
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	var message map[string]any
	return message, json.Unmarshal(payload, &message)
}

func (c *gateClient) send(message map[string]any) {
	message["jsonrpc"] = "2.0"
	payload, _ := json.Marshal(message)
	_, _ = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
}

// handle records a notification and answers a server request.
func (c *gateClient) handle(message map[string]any) {
	if message["method"] == "textDocument/publishDiagnostics" {
		params, _ := message["params"].(map[string]any)
		uri, _ := params["uri"].(string)
		entries, _ := params["diagnostics"].([]any)
		var diagnostics []map[string]any
		for _, entry := range entries {
			if diagnostic, ok := entry.(map[string]any); ok {
				diagnostics = append(diagnostics, diagnostic)
			}
		}
		c.mu.Lock()
		c.diagnostics[uri] = diagnostics
		c.mu.Unlock()
		return
	}
	if message["method"] != nil && message["id"] != nil {
		c.send(map[string]any{"id": message["id"], "result": nil})
	}
}

func (c *gateClient) request(method string, params map[string]any) any {
	c.t.Helper()
	c.nextID++
	id := float64(c.nextID)
	c.send(map[string]any{"id": c.nextID, "method": method, "params": params})
	timeout := time.After(30 * time.Second)
	for {
		select {
		case message, ok := <-c.messages:
			if !ok {
				c.t.Fatal("ktml lsp closed its output")
			}
			if message["id"] == id && message["method"] == nil {
				if message["error"] != nil {
					c.t.Fatalf("%s failed: %v", method, message["error"])
				}
				return message["result"]
			}
			c.handle(message)
		case <-timeout:
			c.t.Fatalf("%s timed out", method)
		}
	}
}

// eventually repeats a request until accept takes its result: route and
// Tailwind indexes are built in the background.
func (c *gateClient) eventually(method string, params map[string]any, accept func(string) bool) string {
	c.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		encoded, _ := json.Marshal(c.request(method, params))
		if accept(string(encoded)) || time.Now().After(deadline) {
			return string(encoded)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitDiagnostics reads messages until the diagnostics of uri satisfy
// accept, and returns their "source:code" keys.
func (c *gateClient) waitDiagnostics(uri string, accept func([]string) bool) []string {
	c.t.Helper()
	timeout := time.After(30 * time.Second)
	for {
		c.mu.Lock()
		var keys []string
		for _, diagnostic := range c.diagnostics[uri] {
			keys = append(keys, fmt.Sprintf("%v:%v", diagnostic["source"], diagnostic["code"]))
		}
		_, published := c.diagnostics[uri]
		c.mu.Unlock()
		if published && accept(keys) {
			return keys
		}
		select {
		case message, ok := <-c.messages:
			if !ok {
				c.t.Fatal("ktml lsp closed its output")
			}
			c.handle(message)
		case <-timeout:
			return keys
		}
	}
}

// gatePosition is the LSP position of marker+shift in text.
func gatePosition(t *testing.T, text, marker string, shift int) map[string]any {
	t.Helper()
	index := strings.Index(text, marker)
	if index < 0 {
		t.Fatalf("marker %q not found", marker)
	}
	offset := index + shift
	lineStart := strings.LastIndex(text[:offset], "\n") + 1
	return map[string]any{"line": strings.Count(text[:offset], "\n"), "character": len(utf16.Encode([]rune(text[lineStart:offset])))}
}

// tailwindServer returns the pinned Tailwind CSS language server, or why it
// is unavailable.
func tailwindServer(repository string) (string, string) {
	if _, err := exec.LookPath("node"); err != nil {
		return "", "node is not installed"
	}
	candidates := []string{os.Getenv("KTML_TAILWIND_LS"), filepath.Join(repository, ".tools", "tailwind-ls", "node_modules", ".bin", "tailwindcss-language-server")}
	if path, err := exec.LookPath("tailwindcss-language-server"); err == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, ""
		}
	}
	return "", "the Tailwind CSS language server is not installed; run scripts/install-tailwind-ls.sh or set KTML_TAILWIND_LS"
}

func copyGateFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join("testdata", "gate")
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, strings.TrimPrefix(path, source))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// The authored block: an Alpine disclosure styled with the fixture's Tailwind
// theme, calling a component imported from another directory.
const gateAuthored = "\t\t\tdiv x-data=`{ open: false }` class=\"flex items-center gap-2 bg-surface0\" {\n" +
	"\t\t\t\tbutton (type=\"button\" @click=\"open = !open\" aria-expanded=\"false\" class=\"px-3 py-2 rounded-lg\") \"Details\"\n" +
	"\t\t\t\tp x-show=\"open\" class=\"text-sm text-subtext1\" \"Bookseller {view.booksellerID}\"\n" +
	"\t\t\t\tListLoadingState label=\"Saving...\"\n" +
	"\t\t\t}\n"

// The mistakes a reviewer would catch, one per analyzer.
const gateMistakes = "\t\t\tdiv x-text=\"$event\" class=\"flex block text-{view.booksellerID}-500\" hx-post=\"/reviewz/\" \"{view.booksellerId}\"\n" +
	"\t\t\timg src=\"/logo.png\"\n"

// TestLSP_GIVENAReviewsComponent_WHENAuthored_THENEveryAnalyzerCompletesNavigatesRenamesAndDiagnoses
// is the Phase 2 exit gate: the fixture's reviews dashboard component is authored
// through one ktml lsp with the gohtml (KTML and HTML), alpine, and htmx
// plugins and the Tailwind adapter.
func TestLSP_GIVENAReviewsComponent_WHENAuthored_THENEveryAnalyzerCompletesNavigatesRenamesAndDiagnoses(t *testing.T) {
	prepareBinaries(t)
	repository, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := copyGateFixture(t)
	server, missing := tailwindServer(repository)
	// Without the server the adapter still runs KTML's own class checks.
	command := server
	if command == "" {
		command = filepath.Join(dir, "tailwindcss-language-server-not-installed")
	}
	tailwind := `,
  "tailwind": {"command": [` + strconv.Quote(command) + `, "--stdio"]}`
	plugin := func(name string) string { return strconv.Quote(filepath.Join(repository, "plugins", name+".wasm")) }
	writeFile(t, filepath.Join(dir, "ktml.config.json"), `{
  "plugin": {
    "gohtml": {"path": `+plugin("gohtml")+`, "allow_read": ["."]},
    "alpine": {"path": `+plugin("alpine")+`, "allow_read": ["."]},
    "htmx": {"path": `+plugin("htmx")+`, "allow_read": ["."], "settings": {"chi": {"dir": "./web"}}}
  },
  "resolution": {"lsp": ["gohtml", "alpine", "htmx"]}`+tailwind+`
}`)

	client := startGateClient(t, dir)
	root := (&url.URL{Scheme: "file", Path: dir}).String()
	client.request("initialize", map[string]any{"rootUri": root, "capabilities": map[string]any{}})
	client.send(map[string]any{"method": "initialized", "params": map[string]any{}})

	path := filepath.Join(dir, "reviews", "ui.ktml")
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	original := string(readFile(t, path))
	client.send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "ktml", "version": 1, "text": original},
	}})

	// The fixture component has no errors.
	client.waitDiagnostics(uri, func([]string) bool { return true })
	client.mu.Lock()
	for _, diagnostic := range client.diagnostics[uri] {
		if diagnostic["severity"] == float64(1) {
			t.Errorf("the fixture component has an error: %v", diagnostic)
		}
	}
	client.mu.Unlock()

	// Author a block into NewReviewForm.
	anchor := "\t\t\tdiv id=\"form-messages\"\n"
	if !strings.Contains(original, anchor) {
		t.Fatal("anchor not found in the fixture")
	}
	authored := strings.Replace(original, anchor, anchor+gateAuthored, 1)
	version := 2
	change := func(text string) {
		client.send(map[string]any{"method": "textDocument/didChange", "params": map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": version},
			"contentChanges": []any{map[string]any{"text": text}},
		}})
		version++
	}
	change(authored)
	at := func(marker string, shift int) map[string]any {
		return map[string]any{"textDocument": map[string]any{"uri": uri}, "position": gatePosition(t, authored, marker, shift)}
	}

	t.Run("completion", func(t *testing.T) {
		tests := []struct {
			name, marker string
			shift        int
			want         string
		}{
			{"KTML Host Schema path", "{view.booksellerID}", len("{view.book"), `"label":"booksellerID"`},
			{"KTML component from another directory", "ListLoadingState label=\"Saving", len("ListLoad"), `"label":"ListLoadingState"`},
			{"HTML attribute", "aria-expanded=\"false\"", len("aria-ex"), `"label":"aria-expanded"`},
			{"HTML keyword value", "type=\"button\" @click", len("type=\"bu"), `"label":"button"`},
			{"HTMX Chi route", "hx-post=\"/reviews/\"", len("hx-post=\"/rev"), `/reviews/`},
			{"Alpine scope", "@click=\"open", len("@click=\"op"), `"label":"open"`},
		}
		for _, test := range tests {
			result := client.eventually("textDocument/completion", at(test.marker, test.shift), func(result string) bool {
				return strings.Contains(result, test.want)
			})
			if !strings.Contains(result, test.want) {
				t.Errorf("%s completion lacks %s: %.600s", test.name, test.want, result)
			}
		}
		t.Run("Tailwind class", func(t *testing.T) {
			if server == "" {
				t.Skip("skipping Tailwind: " + missing)
			}
			result := client.eventually("textDocument/completion", at("bg-surface0\" {", len("bg-surf")), func(result string) bool {
				return strings.Contains(result, `"label":"bg-surface0"`)
			})
			if !strings.Contains(result, `"label":"bg-surface0"`) {
				t.Errorf("Tailwind completion lacks bg-surface0 from the fixture's theme: %.600s", result)
			}
			if strings.Contains(result, `"label":"NewReviewForm"`) {
				t.Errorf("class completion offers components: %.300s", result)
			}
		})
	})

	t.Run("navigation", func(t *testing.T) {
		tests := []struct {
			name, marker string
			shift        int
			want         string
		}{
			{"component in another directory", "ListLoadingState label=\"Saving", 2, "/ui/list.ktml"},
			{"Host Schema field", "{view.booksellerID}", len("{view.bo"), "/reviews/reviews.ktmlschema"},
			{"HTMX route to its Chi registration", "hx-post=\"/reviews/\"", len("hx-post=\"/re"), "/web/router.go"},
			{"Alpine data", "x-show=\"open\"", len("x-show=\"op"), "/reviews/ui.ktml"},
		}
		for _, test := range tests {
			result := client.eventually("textDocument/definition", at(test.marker, test.shift), func(result string) bool {
				return strings.Contains(result, test.want)
			})
			if !strings.Contains(result, test.want) {
				t.Errorf("%s definition = %s, want %s", test.name, result, test.want)
			}
		}
		references, _ := json.Marshal(client.request("textDocument/references", map[string]any{
			"textDocument": map[string]any{"uri": uri},
			"position":     gatePosition(t, authored, "NewReviewForm [view", 3),
			"context":      map[string]any{"includeDeclaration": true},
		}))
		if count := strings.Count(string(references), uri); count < 2 {
			t.Errorf("references to NewReviewForm = %s, want its declaration and its call", references)
		}
		t.Run("Tailwind theme", func(t *testing.T) {
			if server == "" {
				t.Skip("skipping Tailwind: " + missing)
			}
			result := client.eventually("textDocument/definition", at("bg-surface0\" {", len("bg-surf")), func(result string) bool {
				return strings.Contains(result, "site.css")
			})
			if !strings.Contains(result, "site.css") {
				t.Errorf("Tailwind definition of bg-surface0 = %s, want the fixture's site.css theme", result)
			}
		})
	})

	t.Run("structure", func(t *testing.T) {
		symbols, _ := json.Marshal(client.request("textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": uri}}))
		for _, name := range []string{"Dashboard", "NewReviewForm", "ReviewsGrid"} {
			if !strings.Contains(string(symbols), `"name":"`+name+`"`) {
				t.Errorf("document symbols lack %s: %.300s", name, symbols)
			}
		}
		folds, _ := json.Marshal(client.request("textDocument/foldingRange", map[string]any{"textDocument": map[string]any{"uri": uri}}))
		authoredLine := gatePosition(t, authored, "div x-data=`{ open", 0)["line"].(int)
		if want := fmt.Sprintf(`"startLine":%d`, authoredLine); !strings.Contains(string(folds), want) {
			t.Errorf("folding ranges lack the authored block at line %d: %.300s", authoredLine, folds)
		}
	})

	t.Run("rename", func(t *testing.T) {
		prepare, _ := json.Marshal(client.request("textDocument/prepareRename", at("NewReviewForm [view", 3)))
		if !strings.Contains(string(prepare), `"start"`) {
			t.Errorf("prepareRename NewReviewForm = %s", prepare)
		}
		params := at("NewReviewForm [view", 3)
		params["newName"] = "ReviewForm"
		edits, _ := json.Marshal(client.request("textDocument/rename", params))
		if count := strings.Count(string(edits), `"newText":"ReviewForm"`); count < 2 {
			t.Errorf("component rename edits = %d: %s", count, edits)
		}
		// A Host Schema field renames across the schema and its readers.
		params = at("{view.booksellerID}", len("{view.bo"))
		params["newName"] = "sellerID"
		edits, _ = json.Marshal(client.request("textDocument/rename", params))
		if !strings.Contains(string(edits), "reviews.ktmlschema") || strings.Count(string(edits), `"newText":"sellerID"`) < 3 {
			t.Errorf("Host Schema field rename = %s, want the field and every reader", edits)
		}
		params = at("x-data=`{ open", len("x-data=`{ "))
		params["newName"] = "expanded"
		edits, _ = json.Marshal(client.request("textDocument/rename", params))
		if count := strings.Count(string(edits), `"newText":"expanded"`); count != 4 {
			t.Errorf("Alpine rename edits = %d, want 4: %s", count, edits)
		}
	})

	t.Run("diagnostics", func(t *testing.T) {
		mistaken := strings.Replace(authored, anchor, anchor+gateMistakes, 1)
		change(mistaken)
		want := []string{
			"ktml:unknown-field",
			"ktml:a11y-missing-alt",
			"alpine:alpine-scope-reference",
			"htmx:htmx-missing-route",
			"tailwind:tailwind-dynamic-class",
		}
		if server != "" {
			want = append(want, "tailwindcss:cssConflict")
		}
		got := client.waitDiagnostics(uri, func(keys []string) bool {
			for _, key := range want {
				if !strings.Contains(strings.Join(keys, " "), key) {
					return false
				}
			}
			return true
		})
		for _, key := range want {
			if !strings.Contains(strings.Join(got, " "), key) {
				t.Errorf("diagnostics lack %s: %v", key, got)
			}
		}
		if server == "" {
			t.Log("Tailwind cssConflict not checked: " + missing)
		}
	})
}
