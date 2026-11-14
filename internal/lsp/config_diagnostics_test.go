package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/karl-tye/ktml/internal/configdiag"
)

func configFrame(payload string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload)
}

func TestServeWithConfig_GIVENConfigErrors_WHENInitialized_THENPublishesThemOnTheConfigAtPreciseRanges(t *testing.T) {
	source := "{\n  \"plugin\": {\n    \"htmx\": {\"settings\": {\"chi\": {\"dir\": \"\"}}}\n  }\n}\n"
	err := fmt.Errorf("ktml_configure failed: htmx settings: chi.dir: must not be empty: wasm error")
	report := ConfigReport{URI: "file:///w/ktml.config.json", Source: []byte(source), Diagnostics: configdiag.Plugin([]byte(source), "htmx", err)}
	input := bytes.NewBufferString(
		configFrame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
			configFrame(`{"jsonrpc":"2.0","method":"initialized","params":{}}`) +
			configFrame(`{"jsonrpc":"2.0","method":"exit"}`))
	var output bytes.Buffer
	ServeWithConfig(context.Background(), input, &output, nil, report)
	reader := bufio.NewReader(&output)
	var published *struct {
		Params struct {
			URI         string `json:"uri"`
			Diagnostics []struct {
				Range struct {
					Start struct{ Line, Character int } `json:"start"`
					End   struct{ Line, Character int } `json:"end"`
				} `json:"range"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	for {
		payload, ok := readFrame(reader)
		if !ok {
			break
		}
		var message struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(payload, &message)
		if message.Method == publishDiagnosticsMethod {
			if err := json.Unmarshal(payload, &published); err != nil {
				t.Fatal(err)
			}
		}
	}
	if published == nil || published.Params.URI != report.URI || len(published.Params.Diagnostics) != 1 {
		t.Fatalf("published = %#v\noutput %s", published, output.String())
	}
	diagnostic := published.Params.Diagnostics[0]
	if diagnostic.Code != "plugin-settings" || diagnostic.Range.Start.Line != 2 || diagnostic.Range.End.Line != 2 {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
	line := "    \"htmx\": {\"settings\": {\"chi\": {\"dir\": \"\"}}}"
	if got := line[diagnostic.Range.Start.Character:diagnostic.Range.End.Character]; got != `""` {
		t.Fatalf("range spells %q", got)
	}
}

// configMessages returns every published diagnostics notification for uri,
// in order, as lists of codes.
func configMessages(t *testing.T, output []byte, uri string) [][]string {
	t.Helper()
	reader := bufio.NewReader(bytes.NewReader(output))
	var published [][]string
	for {
		payload, ok := readFrame(reader)
		if !ok {
			return published
		}
		var message struct {
			Method string `json:"method"`
			Params struct {
				URI         string `json:"uri"`
				Diagnostics []struct {
					Code     string `json:"code"`
					Severity int    `json:"severity"`
				} `json:"diagnostics"`
			} `json:"params"`
		}
		if json.Unmarshal(payload, &message) != nil || message.Method != publishDiagnosticsMethod || message.Params.URI != uri {
			continue
		}
		codes := []string{}
		for _, diagnostic := range message.Params.Diagnostics {
			codes = append(codes, fmt.Sprintf("%s/%d", diagnostic.Code, diagnostic.Severity))
		}
		published = append(published, codes)
	}
}

func TestServeWithConfig_GIVENTheConfigIsEdited_WHENChanged_THENRepublishesItsDiagnostics(t *testing.T) {
	uri := "file:///w/ktml.config.json"
	original := `{"plugin": {"htmx": {"path": "./htmx.wasm"}}}`
	report := ConfigReport{URI: uri, Source: []byte(original), Diagnostics: configdiag.Plugin([]byte(original), "htmx", fmt.Errorf("failed to read plugin wasm"))}
	change := func(text string) string {
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": 2},
			"contentChanges": []any{map[string]any{"text": text}},
		}})
		return configFrame(string(payload))
	}
	input := bytes.NewBufferString(
		configFrame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
			configFrame(`{"jsonrpc":"2.0","method":"initialized","params":{}}`) +
			// A reserved plugin name is a core configuration error.
			change(`{"plugin": {"go": {"path": "./go.wasm"}}}`) +
			// A syntax error.
			change(`{"plugin": `) +
			// Fixed, but plugins load only at start.
			change(`{"plugin": {}}`) +
			// Back to the file the server started with.
			change(original) +
			configFrame(`{"jsonrpc":"2.0","method":"exit"}`))
	var output bytes.Buffer
	ServeWithConfig(context.Background(), input, &output, nil, report)
	got := configMessages(t, output.Bytes(), uri)
	want := [][]string{
		{"plugin-load/1"},
		{"config/1", "config-restart/3"},
		{"config-syntax/1", "config-restart/3"},
		{"config-restart/3"},
		{"plugin-load/1"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("published = %v, want %v", got, want)
	}
}

func TestServeWithConfig_GIVENTheClientWatchesFiles_THENTheConfigIsWatched(t *testing.T) {
	input := bytes.NewBufferString(
		configFrame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"workspace":{"didChangeWatchedFiles":{"dynamicRegistration":true}}}}}`) +
			configFrame(`{"jsonrpc":"2.0","method":"initialized","params":{}}`) +
			configFrame(`{"jsonrpc":"2.0","method":"exit"}`))
	var output bytes.Buffer
	ServeWithConfig(context.Background(), input, &output, nil, ConfigReport{URI: "file:///w/ktml.config.json"})
	if !bytes.Contains(output.Bytes(), []byte(`"globPattern":"**/ktml.config.json"`)) {
		t.Errorf("output = %s, want a ktml.config.json watcher", output.String())
	}
}
