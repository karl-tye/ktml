package e2e_test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

const alpineEditorSource = "Toggle [] {\n" +
	"\tdiv x-data=`{ open: false, toggle() { this.open = !this.open } }` {\n" +
	"\t\tbutton (type=\"button\" @click=\"toggle()\") \"Menu\"\n" +
	"\t\tul x-show=\"open\" { li \"a\" }\n" +
	"\t}\n" +
	"\tp x-text=\"open\"\n" +
	"\timg src=\"/logo.png\"\n" +
	"}\n"

// TestLSP_GIVENAlpineAndHTML_WHENEdited_THENTheAnalyzersAnswerThroughOneServer
// drives the real ktml lsp with the gohtml and alpine wasm plugins.
func TestLSP_GIVENAlpineAndHTML_WHENEdited_THENTheAnalyzersAnswerThroughOneServer(t *testing.T) {
	prepareBinaries(t)
	command := exec.Command("./ktml", "lsp")
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
		t.Fatalf("start LSP: %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	reader := bufio.NewReader(stdout)
	writeLSP(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"rootUri": "file://" + fixtureDirectory(t)}})
	readLSP(t, reader)

	uri := "file://" + fixtureDirectory(t) + "/alpine-editor.ktml"
	writeLSP(t, stdin, map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "ktml", "version": 1, "text": alpineEditorSource},
	}})
	// The host merges the plugins' diagnostics into one notification.
	notification := readLSPNotification(t, reader, uri)
	params, _ := notification["params"].(map[string]any)
	diagnostics, _ := params["diagnostics"].([]any)
	var codes []string
	for _, diagnostic := range diagnostics {
		entry, _ := diagnostic.(map[string]any)
		codes = append(codes, entry["source"].(string)+":"+entry["code"].(string))
	}
	sort.Strings(codes)
	if got := strings.Join(codes, ","); got != "alpine:alpine-out-of-scope,ktml:a11y-missing-alt" {
		t.Fatalf("diagnostics = %s", got)
	}

	position := func(marker string, shift int) map[string]any {
		offset := strings.Index(alpineEditorSource, marker) + shift
		line := strings.Count(alpineEditorSource[:offset], "\n")
		return map[string]any{"line": line, "character": offset - strings.LastIndex(alpineEditorSource[:offset], "\n") - 1}
	}
	request := func(id int, method string, params map[string]any) string {
		params["textDocument"] = map[string]any{"uri": uri}
		writeLSP(t, stdin, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		encoded, _ := json.Marshal(readLSP(t, reader))
		return string(encoded)
	}
	if hover := request(2, "textDocument/hover", map[string]any{"position": position(`x-show="open"`, 8)}); !strings.Contains(hover, "(property) open: boolean") {
		t.Errorf("Alpine hover = %s", hover)
	}
	if hover := request(3, "textDocument/hover", map[string]any{"position": position("img", 1)}); !strings.Contains(hover, "An image.") {
		t.Errorf("HTML hover = %s", hover)
	}
	if completion := request(4, "textDocument/completion", map[string]any{"position": position(`@click="toggle()"`, 8)}); !strings.Contains(completion, `"label":"toggle"`) || !strings.Contains(completion, `"label":"$event"`) {
		t.Errorf("Alpine completion = %s", completion)
	}
	rename := request(5, "textDocument/rename", map[string]any{"position": position("open: false", 0), "newName": "expanded"})
	if count := strings.Count(rename, `"newText":"expanded"`); count != 4 {
		t.Errorf("rename edits = %d: %s", count, rename)
	}
}
