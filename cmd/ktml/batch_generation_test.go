package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/dev"
)

func TestGenerateBatchContext_GIVENLaterFileFails_WHENGenerating_THENPublishesNoOutputOrState(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	pluginPath := buildCLIGohtmlWasm(t, filepath.Join(workingDirectory, "..", "plugins", "gohtml"))
	dir := t.TempDir()
	t.Chdir(dir)
	for name, source := range map[string]string{
		"a.ktml": `A { div { "a" } }`,
		"b.ktml": `B { div { "b" } }`,
	} {
		if err := os.WriteFile(name, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	config := `{"resolution":{"generate":"gohtml"},"plugin":{"gohtml":{"outputs":[{"dir":"."}],"path":"` + pluginPath + `","allow_read":["` + dir + `"]}}}`
	if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := generateBatchContext(context.Background(), dev.Batch{Generate: []string{"a.ktml", "b.ktml"}}); err != nil {
		t.Fatalf("initial generateBatchContext() error = %v", err)
	}
	initialA := mustReadFile(t, "a.html")
	initialState := mustReadFile(t, filepath.Join(".ktml", "state.json"))
	if err := os.WriteFile("a.ktml", []byte(`A { div { "changed a" } }`), 0o600); err != nil {
		t.Fatalf("write changed a: %v", err)
	}
	if err := os.WriteFile("b.ktml", []byte(`B [label?: string] { div }`), 0o600); err != nil {
		t.Fatalf("write failing b: %v", err)
	}

	err = generateBatchContext(context.Background(), dev.Batch{Generate: []string{"a.ktml", "b.ktml"}})

	if err == nil || !strings.Contains(err.Error(), "unsupported input mode optional") {
		t.Fatalf("generateBatchContext() error = %v, want gohtml failure for b.ktml", err)
	}
	if got := string(mustReadFile(t, "a.html")); got != string(initialA) {
		t.Errorf("a.html was published although the batch failed:\n%s", got)
	}
	if got := string(mustReadFile(t, filepath.Join(".ktml", "state.json"))); got != string(initialState) {
		t.Errorf("build state changed although the batch failed:\n%s", got)
	}
}

func TestGenerateBatchContext_GIVENRemovedSource_WHENGenerating_THENRemovesItsOutputsWithTheBatch(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	pluginPath := buildCLIGohtmlWasm(t, filepath.Join(workingDirectory, "..", "plugins", "gohtml"))
	dir := t.TempDir()
	t.Chdir(dir)
	for name, source := range map[string]string{
		"keep.ktml":    `Keep { div }`,
		"removed.ktml": `Removed { div }`,
	} {
		if err := os.WriteFile(name, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	config := `{"resolution":{"generate":"gohtml"},"plugin":{"gohtml":{"outputs":[{"dir":"."}],"path":"` + pluginPath + `","allow_read":["` + dir + `"]}}}`
	if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := generateBatchContext(context.Background(), dev.Batch{Generate: []string{"keep.ktml", "removed.ktml"}}); err != nil {
		t.Fatalf("initial generateBatchContext() error = %v", err)
	}
	if err := os.Remove("removed.ktml"); err != nil {
		t.Fatalf("remove source: %v", err)
	}

	if err := generateBatchContext(context.Background(), dev.Batch{Remove: []string{"removed.ktml"}}); err != nil {
		t.Fatalf("generateBatchContext() error = %v", err)
	}

	if _, err := os.Stat("removed.html"); !os.IsNotExist(err) {
		t.Errorf("removed source output still exists: %v", err)
	}
	if _, err := os.Stat("keep.html"); err != nil {
		t.Errorf("surviving output was removed: %v", err)
	}
	if state := string(mustReadFile(t, filepath.Join(".ktml", "state.json"))); strings.Contains(state, "removed.ktml") {
		t.Errorf("build state still records the removed source:\n%s", state)
	}
}
