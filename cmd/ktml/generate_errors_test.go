package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/buildstate"
)

func TestGenerateFile_GIVENResponseErrorsAndUnchangedPrimary_WHENGenerating_THENPublishesNothing(t *testing.T) {
	wasmPath := buildCLIErrorResponseWasm(t)
	t.Chdir(t.TempDir())
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	config := `{"resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":"` + wasmPath + `"}}}`
	if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	absPath, err := filepath.Abs("page.ktml")
	if err != nil {
		t.Fatalf("absolute source path: %v", err)
	}
	files := map[string]string{
		"primary.out": "unchanged primary",
		"aux.out":     "old auxiliary",
		"stale.out":   "stale output",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	manifest := &buildstate.Manifest{Files: map[string]buildstate.FileEntry{
		absPath: {
			Hash:             "old hash",
			PrimaryOutput:    "primary.out",
			AuxiliaryOutputs: []string{"stale.out"},
		},
	}}
	statePath := filepath.Join(".ktml", "state.json")
	if err := buildstate.Save(statePath, manifest); err != nil {
		t.Fatalf("save initial state: %v", err)
	}
	initialState := mustReadFile(t, statePath)

	if err := generateFile("page.ktml", false); err == nil {
		t.Fatal("generateFile() error = nil, want plugin response error")
	}
	for path, want := range files {
		if got := string(mustReadFile(t, path)); got != want {
			t.Errorf("%s = %q, want unchanged %q", path, got, want)
		}
	}
	if got := string(mustReadFile(t, statePath)); got != string(initialState) {
		t.Errorf("build state changed after plugin error:\n%s\nwant:\n%s", got, initialState)
	}
}

func buildCLIErrorResponseWasm(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "main.go")
	wasmPath := filepath.Join(dir, "plugin.wasm")
	source := `package main

import "unsafe"

var buffer []byte

//go:wasmimport env ktml_host_write_file
func writeFile(pathPtr, pathLen, contentPtr, contentLen uint32)

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	buffer = make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buffer[0])))
}

//go:wasmexport ktml_generate
func generate(_, _, _, _ uint32) uint64 {
	path := []byte("aux.out")
	content := []byte("new auxiliary")
	writeFile(
		uint32(uintptr(unsafe.Pointer(&path[0]))), uint32(len(path)),
		uint32(uintptr(unsafe.Pointer(&content[0]))), uint32(len(content)),
	)
	response := []byte(` + "`" + `{"content":"unchanged primary","output_path":"primary.out","errors":["schema invalid"]}` + "`" + `)
	return uint64(uint32(uintptr(unsafe.Pointer(&response[0]))))<<32 | uint64(len(response))
}

//go:wasmexport ktml_manifest
func ktmlManifest() uint64 {
	m := []byte("{\"name\":\"test\",\"version\":\"0.0.0\",\"abi\":1,\"capabilities\":[\"generate\",\"format\",\"completion\"]}")
	return uint64(uintptr(unsafe.Pointer(&m[0])))<<32 | uint64(len(m))
}

func main() {}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("write plugin source: %v", err)
	}
	command := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, sourcePath)
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, output)
	}
	return wasmPath
}

func TestGenerateFile_GIVENVoidElementWithContent_WHENGenerating_THENFailsWithTheRangedCompilerDiagnostic(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("page.ktml", []byte("Page [] {\n\tbr { \"x\" }\n}\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	err := generateFile("page.ktml", false)
	if err == nil || !strings.Contains(err.Error(), "page.ktml:11-13: [void-content]") {
		t.Fatalf("generateFile() error = %v, want the ranged void-content diagnostic", err)
	}
	if _, statErr := os.Stat("page.ktml.go"); !os.IsNotExist(statErr) {
		t.Errorf("generated output exists after a diagnostic: %v", statErr)
	}
}
