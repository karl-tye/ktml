package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFmtCmd_Errors(t *testing.T) {
	tempDir := t.TempDir()
	exePath := filepath.Join(tempDir, "ktml")

	buildCmd := exec.Command("go", "build", "-o", exePath, ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	validFile1 := filepath.Join(tempDir, "valid1.ktml")
	invalidFile := filepath.Join(tempDir, "invalid.ktml")
	validFile2 := filepath.Join(tempDir, "valid2.ktml")

	os.WriteFile(validFile1, []byte("div {\n}"), 0644)
	os.WriteFile(invalidFile, []byte("div {"), 0644) // Syntax error (missing closing brace)
	os.WriteFile(validFile2, []byte("span {\n}"), 0644)

	cmd := exec.Command(exePath, "fmt", validFile1, invalidFile, validFile2)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()

	if err == nil {
		t.Fatal("expected failure exit status, got success")
	}

	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got: %v (stderr: %s)", err, stderr.String())
	}

	if !strings.Contains(stderr.String(), invalidFile) {
		t.Errorf("expected stderr to contain error for %s, got: %s", invalidFile, stderr.String())
	}

	content1, _ := os.ReadFile(validFile1)
	if !strings.Contains(string(content1), "div") {
		t.Errorf("expected validFile1 to remain valid, got: %s", string(content1))
	}

	content2, _ := os.ReadFile(validFile2)
	if !strings.Contains(string(content2), "span") {
		t.Errorf("expected validFile2 to be processed and formatted, got: %s", string(content2))
	}
}

func TestGenerateFile_GIVENCorruptCachedOutput_WHENGenerating_THENRepairsOutput(t *testing.T) {
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	t.Chdir(tempDir)

	pluginPath := buildCLIGohtmlWasm(t, filepath.Join(originalWd, "..", "plugins", "gohtml"))
	configJSON := `{"package_dir":".","resolution":{"generate":"test-plugin"},"plugin":{"test-plugin":{"outputs":[{"dir":"."}],"path":"` + pluginPath + `"}}}`
	if err := os.WriteFile("ktml.config.json", []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile("test.ktml", []byte(`Page { div }`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := generateFile("test.ktml", false); err != nil {
		t.Fatalf("initial generation: %v", err)
	}
	if err := os.WriteFile("test.html", []byte("TAMPERED"), 0o644); err != nil {
		t.Fatalf("tamper output: %v", err)
	}

	if err := generateFile("test.ktml", false); err != nil {
		t.Fatalf("regenerate corrupt output: %v", err)
	}

	if got := string(mustReadFile(t, "test.html")); got == "TAMPERED" {
		t.Fatal("corrupt output was trusted as a cache hit")
	}
}

func TestGenerateFile_ParseErrorsAreFatal(t *testing.T) {
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	defer os.Chdir(originalWd)
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	if err := os.WriteFile("invalid.ktml", []byte("div {"), 0644); err != nil {
		t.Fatalf("write invalid source: %v", err)
	}
	stateFile := filepath.Join(".ktml", "state.json")
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		t.Fatalf("create build-state directory: %v", err)
	}
	if err := os.WriteFile(stateFile, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write poisoned build state: %v", err)
	}

	err = generateFile("invalid.ktml", false)
	if err == nil || !strings.Contains(err.Error(), "invalid.ktml:5-6: [syntax]") {
		t.Fatalf("generateFile() error = %v, want parse error", err)
	}
	if _, err := os.Stat("invalid.ktml.go"); !os.IsNotExist(err) {
		t.Fatalf("generated output exists after parse error: %v", err)
	}
	if got := string(mustReadFile(t, stateFile)); got != "not-json" {
		t.Fatalf("build state = %q, want poisoned state unchanged", got)
	}
}

func TestGenerateFile_GIVENSemanticErrors_WHENGenerating_THENFailsBeforeCachePluginAndWrites(t *testing.T) {
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	defer os.Chdir(originalWd)
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	if err := os.WriteFile("invalid.ktml", []byte("Page { Missing }"), 0o644); err != nil {
		t.Fatalf("write invalid source: %v", err)
	}
	stateFile := filepath.Join(".ktml", "state.json")
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		t.Fatalf("create build-state directory: %v", err)
	}
	if err := os.WriteFile(stateFile, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write poisoned build state: %v", err)
	}

	err = generateFile("invalid.ktml", false)
	if err == nil || !strings.Contains(err.Error(), "invalid.ktml:7-14: [unknown-component]") {
		t.Fatalf("generateFile() error = %v, want unknown-component semantic error", err)
	}
	if _, err := os.Stat("invalid.ktml.go"); !os.IsNotExist(err) {
		t.Fatalf("generated output exists after semantic error: %v", err)
	}
	if got := string(mustReadFile(t, stateFile)); got != "not-json" {
		t.Fatalf("build state = %q, want poisoned state unchanged", got)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return content
}
