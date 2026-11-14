package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateFile_GIVENRawResponseAndAbsoluteSource_WHENGenerating_THENContainsFallbackInsideProjectOnly(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	t.Chdir(project)
	pluginPath := buildRawResponsePlugin(t, "raw generated output")
	config := `{"package_dir":".","resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":` + quoteJSON(t, pluginPath) + `}}}`
	if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	insideSource := filepath.Join(project, "page.ktml")
	outsideSource := filepath.Join(outside, "outside.ktml")
	for _, source := range []string{insideSource, outsideSource} {
		if err := os.WriteFile(source, []byte(`Page { div }`), 0o600); err != nil {
			t.Fatalf("write source: %v", err)
		}
	}

	if err := generateFile(insideSource, false); err != nil {
		t.Fatalf("generate contained absolute source: %v", err)
	}
	insideOutput := filepath.Join(project, "page.ktml.go")
	if content, err := os.ReadFile(insideOutput); err != nil || string(content) != "raw generated output" {
		t.Fatalf("contained fallback output = %q, %v", content, err)
	}

	err := generateFile(outsideSource, false)
	if err == nil || !strings.Contains(err.Error(), "outside project root") {
		t.Fatalf("generate outside source error = %v, want project-root rejection", err)
	}
	if _, statErr := os.Stat(outsideSource + ".go"); !os.IsNotExist(statErr) {
		t.Fatalf("fallback output written outside project: %v", statErr)
	}
}

func buildRawResponsePlugin(t *testing.T, response string) string {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "main.go")
	wasmPath := filepath.Join(dir, "plugin.wasm")
	source := `package main

import "unsafe"

var buffer []byte

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	buffer = make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buffer[0])))
}

//go:wasmexport ktml_generate
func generate(_, _, _, _ uint32) uint64 {
	response := []byte(` + quoteGoString([]byte(response)) + `)
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
