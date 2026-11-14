package wasm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/buildstate"
)

func TestPlugin_GIVENResponseErrorsAndUnchangedPrimary_WHENGenerating_THENPublishesNothing(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
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
	initialState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read initial state: %v", err)
	}

	ctx := context.Background()
	plugin, err := NewPlugin(ctx, buildErrorResponseWasm(t), nil, nil)
	if err != nil {
		t.Fatalf("create plugin: %v", err)
	}
	defer plugin.Close(ctx)

	err = plugin.Generate("page.ktml")
	if err == nil {
		t.Fatal("Generate() error = nil, want plugin response error")
	}
	for path, want := range files {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read preserved %s: %v", path, readErr)
		}
		if string(content) != want {
			t.Errorf("%s = %q, want unchanged %q", path, content, want)
		}
	}
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read preserved state: %v", err)
	}
	if string(state) != string(initialState) {
		t.Errorf("build state changed after plugin error:\n%s\nwant:\n%s", state, initialState)
	}
}

func buildErrorResponseWasm(t *testing.T) []byte {
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
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	return wasmBytes
}
