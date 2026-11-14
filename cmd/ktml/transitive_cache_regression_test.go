package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateFile_GIVENChangedTransitiveImport_WHENRootIsUnchanged_THENRevalidatesBeforePublishing(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	pluginPath := buildCLIGohtmlWasm(t, filepath.Join(workingDirectory, "..", "plugins", "gohtml"))
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
	config := `{"resolution":{"generate":"gohtml"},"plugin":{"gohtml":{"outputs":[{"dir":"."}],"path":"` + pluginPath + `","allow_read":["` + dir + `"]}}}`
	if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := generateFile("page.ktml", false); err != nil {
		t.Fatalf("initial generateFile() error = %v", err)
	}
	initialOutput := mustReadFile(t, "page.html")
	initialState := mustReadFile(t, filepath.Join(".ktml", "state.json"))
	if err := os.WriteFile("leaf.ktml", []byte(`Leaf [label?: string] { span }`), 0o600); err != nil {
		t.Fatalf("write changed transitive import: %v", err)
	}

	err = generateFile("page.ktml", false)

	if err == nil || !strings.Contains(err.Error(), "unsupported input mode optional") {
		t.Fatalf("generateFile() error = %v, want transitive gohtml validation error", err)
	}
	if got := string(mustReadFile(t, "page.html")); got != string(initialOutput) {
		t.Errorf("primary output changed after transitive validation failure:\n%s", got)
	}
	if got := string(mustReadFile(t, filepath.Join(".ktml", "state.json"))); got != string(initialState) {
		t.Errorf("build state changed after transitive validation failure:\n%s", got)
	}
}

func buildCLIGohtmlWasm(t *testing.T, sourceDirectory string) string {
	t.Helper()
	wasmPath := filepath.Join(t.TempDir(), "gohtml.wasm")
	command := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, ".")
	command.Dir = sourceDirectory
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build gohtml WASM: %v\n%s", err, output)
	}
	return wasmPath
}
