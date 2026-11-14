package wasm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/buildstate"
)

func TestPlugin_GIVENAuxiliaryWriteFailure_WHENPrimaryChangedOrUnchanged_THENReturnsFailureWithoutPublishing(t *testing.T) {
	wasmBytes := buildSuccessResponseWasm(t, true)
	for _, test := range []struct {
		name           string
		primaryContent string
	}{
		{name: "changed primary", primaryContent: "old primary"},
		{name: "unchanged primary", primaryContent: "new primary"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
				t.Fatalf("write source: %v", err)
			}
			if err := os.WriteFile("primary.out", []byte(test.primaryContent), 0o600); err != nil {
				t.Fatalf("write primary: %v", err)
			}
			if err := os.Mkdir("aux.out", 0o700); err != nil {
				t.Fatalf("create blocked auxiliary target: %v", err)
			}
			if err := os.WriteFile("stale.out", []byte("stale"), 0o600); err != nil {
				t.Fatalf("write stale output: %v", err)
			}
			absPath, err := filepath.Abs("page.ktml")
			if err != nil {
				t.Fatalf("absolute source path: %v", err)
			}
			statePath := filepath.Join(".ktml", "state.json")
			manifest := &buildstate.Manifest{Files: map[string]buildstate.FileEntry{
				absPath: {Hash: "old hash", PrimaryOutput: "primary.out", AuxiliaryOutputs: []string{"stale.out"}},
			}}
			if err := buildstate.Save(statePath, manifest); err != nil {
				t.Fatalf("save initial state: %v", err)
			}
			initialState, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatalf("read initial state: %v", err)
			}
			ctx := context.Background()
			plugin, err := NewPluginWithOptions(ctx, wasmBytes, Options{Outputs: anyOutputs})
			if err != nil {
				t.Fatalf("NewPlugin() error = %v", err)
			}
			t.Cleanup(func() { _ = plugin.Close(ctx) })

			err = plugin.Generate("page.ktml")

			if err == nil || !strings.Contains(err.Error(), "failed to write aux file aux.out") {
				t.Fatalf("Generate() error = %v, want auxiliary write failure", err)
			}
			if primary, readErr := os.ReadFile("primary.out"); readErr != nil || string(primary) != test.primaryContent {
				t.Errorf("primary output = %q, %v; want unchanged %q", primary, readErr, test.primaryContent)
			}
			if _, err := os.Stat("stale.out"); err != nil {
				t.Errorf("stale output was removed after auxiliary failure: %v", err)
			}
			if state, readErr := os.ReadFile(statePath); readErr != nil || string(state) != string(initialState) {
				t.Errorf("build state after auxiliary failure = %q, %v", state, readErr)
			}
		})
	}
}

func TestPlugin_GIVENStaleRemovalFailure_WHENGenerating_THENReturnsFailureWithoutAdvancingState(t *testing.T) {
	wasmBytes := buildSuccessResponseWasm(t, false)
	t.Chdir(t.TempDir())
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile("primary.out", []byte("old primary"), 0o600); err != nil {
		t.Fatalf("write primary: %v", err)
	}
	if err := os.Mkdir("stale.out", 0o700); err != nil {
		t.Fatalf("create stale directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join("stale.out", "nested"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("write nested stale file: %v", err)
	}
	absPath, err := filepath.Abs("page.ktml")
	if err != nil {
		t.Fatalf("absolute source path: %v", err)
	}
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &buildstate.Manifest{Files: map[string]buildstate.FileEntry{
		absPath: {Hash: "old hash", PrimaryOutput: "primary.out", AuxiliaryOutputs: []string{"stale.out"}},
	}}
	if err := buildstate.Save(statePath, manifest); err != nil {
		t.Fatalf("save initial state: %v", err)
	}
	initialState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read initial state: %v", err)
	}
	ctx := context.Background()
	plugin, err := NewPluginWithOptions(ctx, wasmBytes, Options{Outputs: anyOutputs})
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })

	err = plugin.Generate("page.ktml")

	if err == nil || !strings.Contains(err.Error(), "failed to remove stale output stale.out") {
		t.Fatalf("Generate() error = %v, want stale removal failure", err)
	}
	if state, readErr := os.ReadFile(statePath); readErr != nil || string(state) != string(initialState) {
		t.Errorf("build state after stale removal failure = %q, %v", state, readErr)
	}
	if primary, readErr := os.ReadFile("primary.out"); readErr != nil || string(primary) != "old primary" {
		t.Errorf("primary output after stale removal failure = %q, %v", primary, readErr)
	}
}

func TestPlugin_GIVENStateSaveFailure_WHENPrimaryChanged_THENRollsBackOutputAndState(t *testing.T) {
	wasmBytes := buildSuccessResponseWasm(t, false)
	t.Chdir(t.TempDir())
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile("primary.out", []byte("old primary"), 0o600); err != nil {
		t.Fatalf("write primary: %v", err)
	}
	absPath, err := filepath.Abs("page.ktml")
	if err != nil {
		t.Fatalf("absolute source path: %v", err)
	}
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &buildstate.Manifest{Files: map[string]buildstate.FileEntry{
		absPath: {Hash: "old hash", PrimaryOutput: "primary.out"},
	}}
	if err := buildstate.Save(statePath, manifest); err != nil {
		t.Fatalf("save initial state: %v", err)
	}
	initialState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read initial state: %v", err)
	}
	if err := os.Chmod(statePath, 0o400); err != nil {
		t.Fatalf("make state read-only: %v", err)
	}
	if err := os.Chmod(filepath.Dir(statePath), 0o500); err != nil {
		t.Fatalf("make state directory read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Dir(statePath), 0o700)
		_ = os.Chmod(statePath, 0o600)
	})
	ctx := context.Background()
	plugin, err := NewPluginWithOptions(ctx, wasmBytes, Options{Outputs: anyOutputs})
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })

	err = plugin.Generate("page.ktml")

	if err == nil || !strings.Contains(err.Error(), "failed to save build state") {
		t.Fatalf("Generate() error = %v, want state save failure", err)
	}
	if state, readErr := os.ReadFile(statePath); readErr != nil || string(state) != string(initialState) {
		t.Errorf("build state after save failure = %q, %v", state, readErr)
	}
	if primary, readErr := os.ReadFile("primary.out"); readErr != nil || string(primary) != "old primary" {
		t.Errorf("primary output after save failure = %q, %v", primary, readErr)
	}
}

func buildSuccessResponseWasm(t *testing.T, writeAuxiliary bool) []byte {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "main.go")
	wasmPath := filepath.Join(dir, "plugin.wasm")
	auxiliary := ""
	if writeAuxiliary {
		auxiliary = `
	path := []byte("aux.out")
	content := []byte("new auxiliary")
	writeFile(uint32(uintptr(unsafe.Pointer(&path[0]))), uint32(len(path)), uint32(uintptr(unsafe.Pointer(&content[0]))), uint32(len(content)))`
	}
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
func generate(_, _, _, _ uint32) uint64 {` + auxiliary + `
	response := []byte("{\"content\":\"new primary\",\"output_path\":\"primary.out\"}")
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
