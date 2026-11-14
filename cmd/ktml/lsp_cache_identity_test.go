package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/buildstate"
)

func TestGenerateFile_GIVENLSPCommandChanges_WHENSourceIsUnchanged_THENInvalidatesCache(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	pluginPath := buildOutputPathPlugin(t, "page.out")
	writeConfig := func(command string) {
		t.Helper()
		contents := `{"package_dir":".","lsp":{"go":{"command":[` + quoteJSON(t, command) + `]}},"resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":` + quoteJSON(t, pluginPath) + `}}}`
		if err := os.WriteFile("ktml.config.json", []byte(contents), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	source, err := filepath.Abs("page.ktml")
	if err != nil {
		t.Fatalf("resolve source: %v", err)
	}
	statePath := filepath.Join(".ktml", "state.json")
	writeConfig("first-lsp")
	if err := generateFile("page.ktml", false); err != nil {
		t.Fatalf("initial generation: %v", err)
	}
	initial, err := buildstate.Load(statePath)
	if err != nil {
		t.Fatalf("load initial build state: %v", err)
	}
	initialHash := initial.Files[source].Hash

	writeConfig("second-lsp")
	if err := generateFile("page.ktml", false); err != nil {
		t.Fatalf("generation after LSP command change: %v", err)
	}
	changed, err := buildstate.Load(statePath)
	if err != nil {
		t.Fatalf("load changed build state: %v", err)
	}
	if changed.Files[source].Hash == initialHash {
		t.Fatal("changed LSP command reused the previous generation cache identity")
	}
}
