package wasm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlugin_GIVENChangedTransitiveImport_WHENRootIsUnchanged_THENRevalidatesBeforePublishing(t *testing.T) {
	wasmBytes := buildGohtmlWasm(t)
	dir := t.TempDir()
	t.Chdir(dir)
	for name, source := range map[string]string{
		"leaf.ktml":   `Leaf [label: string] { span }`,
		"middle.ktml": "import { Leaf } from \"leaf\"\nMiddle { Leaf label=\"ok\" }",
		"page.ktml":   "import { Middle } from \"middle\"\nPage { Middle }",
	} {
		if err := os.WriteFile(name, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	ctx := context.Background()
	plugin, err := NewPluginWithOptions(ctx, wasmBytes, Options{AllowRead: []string{dir}, Outputs: anyOutputs})
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })
	if err := plugin.Generate("page.ktml"); err != nil {
		t.Fatalf("initial Generate() error = %v", err)
	}
	initialOutput, err := os.ReadFile("page.html")
	if err != nil {
		t.Fatalf("read initial output: %v", err)
	}
	statePath := filepath.Join(".ktml", "state.json")
	initialState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read initial state: %v", err)
	}
	if err := os.WriteFile("leaf.ktml", []byte(`Leaf [label?: string] { span }`), 0o600); err != nil {
		t.Fatalf("write changed transitive import: %v", err)
	}

	err = plugin.Generate("page.ktml")

	if err == nil || !strings.Contains(err.Error(), "unsupported input mode optional") {
		t.Fatalf("Generate() error = %v, want transitive gohtml validation error", err)
	}
	if output, readErr := os.ReadFile("page.html"); readErr != nil || string(output) != string(initialOutput) {
		t.Errorf("primary output after validation failure = %q, %v", output, readErr)
	}
	if state, readErr := os.ReadFile(statePath); readErr != nil || string(state) != string(initialState) {
		t.Errorf("build state after validation failure = %q, %v", state, readErr)
	}
}
