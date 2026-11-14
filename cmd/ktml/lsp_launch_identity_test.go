package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/buildstate"
)

func TestGenerateFile_GIVENLSPExecutableOrEnvironmentChanges_THENInvalidatesHashedCacheIdentity(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	executable := filepath.Join(project, "test-lsp")
	writeExecutable := func(contents string) {
		t.Helper()
		if err := os.WriteFile(executable, []byte(contents), 0o700); err != nil {
			t.Fatalf("write LSP executable: %v", err)
		}
	}
	writeExecutable("#!/bin/sh\nexit 0\n")
	pluginPath := buildOutputPathPlugin(t, "page.out")
	configJSON := `{"package_dir":".","lsp":{"go":{"command":[` + quoteJSON(t, executable) + `]}},"resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":` + quoteJSON(t, pluginPath) + `}}}`
	if err := os.WriteFile("ktml.config.json", []byte(configJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	source, err := filepath.Abs("page.ktml")
	if err != nil {
		t.Fatalf("resolve source: %v", err)
	}
	statePath := filepath.Join(".ktml", "state.json")
	generateHash := func() string {
		t.Helper()
		if err := generateFile("page.ktml", false); err != nil {
			t.Fatalf("generateFile() error = %v", err)
		}
		manifest, err := buildstate.Load(statePath)
		if err != nil {
			t.Fatalf("load build state: %v", err)
		}
		return manifest.Files[source].Hash
	}

	initialHash := generateHash()
	writeExecutable("#!/bin/sh\n# changed implementation\nexit 0\n")
	executableHash := generateHash()
	if executableHash == initialHash {
		t.Fatal("resolved LSP executable content change reused the generation cache")
	}

	const secret = "launch-secret-must-not-be-persisted"
	t.Setenv("KTML_LSP_TEST_SECRET", secret)
	environmentHash := generateHash()
	if environmentHash == executableHash {
		t.Fatal("LSP launch environment change reused the generation cache")
	}
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read build state: %v", err)
	}
	if bytes.Contains(state, []byte(secret)) {
		t.Fatal("raw LSP launch environment secret was persisted in build state")
	}
}
