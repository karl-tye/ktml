package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/config"
)

type cleanupErrorManager struct {
	closeErr error
}

func (*cleanupErrorManager) ProxyRequest(context.Context, string, []byte) ([]byte, error) {
	return nil, nil
}

func (*cleanupErrorManager) GenerationCacheIdentity() any { return "test" }

func (m *cleanupErrorManager) Close() error { return m.closeErr }

func installCleanupErrorManager(t *testing.T, closeErr error) {
	t.Helper()
	original := acquireLSPManager
	acquireLSPManager = func(*config.Config) (managedLSPProxy, error) {
		return &cleanupErrorManager{closeErr: closeErr}, nil
	}
	t.Cleanup(func() { acquireLSPManager = original })
}

func TestGenerateFileContext_GIVENManagerCleanupFails_THENReturnsCleanupError(t *testing.T) {
	closeErr := errors.New("manager cleanup failed")
	installCleanupErrorManager(t, closeErr)
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	pluginPath := buildCLIGohtmlWasm(t, filepath.Join(workingDirectory, "..", "plugins", "gohtml"))
	dir := t.TempDir()
	t.Chdir(dir)
	configJSON := `{"package_dir":".","resolution":{"generate":"test-plugin"},"plugin":{"test-plugin":{"outputs":[{"dir":"."}],"path":"` + pluginPath + `"}}}`
	if err := os.WriteFile("ktml.config.json", []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	err = generateFileContext(context.Background(), "page.ktml", false)
	if !errors.Is(err, closeErr) {
		t.Fatalf("generateFileContext() error = %v, want manager cleanup error", err)
	}
}

func TestFmtCmd_GIVENFormattingAndManagerCleanupFail_THENJoinsErrors(t *testing.T) {
	closeErr := errors.New("manager cleanup failed")
	installCleanupErrorManager(t, closeErr)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("plugin.wasm", []byte("invalid wasm"), 0o644); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	if err := os.WriteFile("ktml.config.json", []byte(`{"resolution":{"format":["fmt"]},"plugin":{"fmt":{"path":"plugin.wasm"}}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile("invalid.ktml", []byte("div {"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	originalArgs := os.Args
	os.Args = []string{"ktml", "fmt", "invalid.ktml"}
	t.Cleanup(func() { os.Args = originalArgs })

	err := fmtCmd()
	if !errors.Is(err, closeErr) {
		t.Fatalf("fmtCmd() error = %v, want manager cleanup error", err)
	}
	if err == closeErr {
		t.Fatal("fmtCmd() discarded the primary formatting error")
	}
}

func TestLSPCmd_GIVENPrimaryAndManagerCleanupFail_THENJoinsErrors(t *testing.T) {
	closeErr := errors.New("manager cleanup failed")
	installCleanupErrorManager(t, closeErr)
	t.Chdir(t.TempDir())
	if err := os.WriteFile("ktml.config.json", []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	err := lspCmd()
	if !errors.Is(err, closeErr) {
		t.Fatalf("lspCmd() error = %v, want manager cleanup error", err)
	}
	if err == closeErr {
		t.Fatal("lspCmd() discarded the primary no-plugin error")
	}
}
