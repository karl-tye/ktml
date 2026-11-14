package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/buildstate"
)

func TestGenerateFile_GIVENAuxiliaryWriteFailure_WHENPrimaryChangedOrUnchanged_THENReturnsFailureWithoutPublishing(t *testing.T) {
	wasmPath := buildCLISuccessResponseWasm(t, true)
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
			config := `{"resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":"` + wasmPath + `"}}}`
			if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
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
			initialState := mustReadFile(t, statePath)

			err = generateFile("page.ktml", false)

			if err == nil || !strings.Contains(err.Error(), "failed to write aux file aux.out") {
				t.Fatalf("generateFile() error = %v, want auxiliary write failure", err)
			}
			if got := string(mustReadFile(t, "primary.out")); got != test.primaryContent {
				t.Errorf("primary output = %q, want unchanged %q", got, test.primaryContent)
			}
			if _, err := os.Stat("stale.out"); err != nil {
				t.Errorf("stale output was removed after auxiliary failure: %v", err)
			}
			if got := string(mustReadFile(t, statePath)); got != string(initialState) {
				t.Errorf("build state changed after auxiliary failure:\n%s", got)
			}
		})
	}
}

func TestGenerateFile_GIVENStaleRemovalFailure_WHENGenerating_THENReturnsFailureWithoutAdvancingState(t *testing.T) {
	wasmPath := buildCLISuccessResponseWasm(t, false)
	t.Chdir(t.TempDir())
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	config := `{"resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":"` + wasmPath + `"}}}`
	if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
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
	initialState := mustReadFile(t, statePath)

	err = generateFile("page.ktml", false)

	if err == nil || !strings.Contains(err.Error(), "failed to remove stale output stale.out") {
		t.Fatalf("generateFile() error = %v, want stale removal failure", err)
	}
	if got := string(mustReadFile(t, statePath)); got != string(initialState) {
		t.Errorf("build state changed after stale removal failure:\n%s", got)
	}
	if got := string(mustReadFile(t, "primary.out")); got != "old primary" {
		t.Errorf("primary output = %q, want rollback to old primary", got)
	}
}

func TestGenerateFile_GIVENStateSaveFailure_WHENPrimaryChanged_THENRollsBackOutputAndState(t *testing.T) {
	wasmPath := buildCLISuccessResponseWasm(t, false)
	t.Chdir(t.TempDir())
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	config := `{"resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":"` + wasmPath + `"}}}`
	if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
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
	initialState := mustReadFile(t, statePath)
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

	err = generateFile("page.ktml", false)

	if err == nil || !strings.Contains(err.Error(), "failed to save build state") {
		t.Fatalf("generateFile() error = %v, want state save failure", err)
	}
	if got := string(mustReadFile(t, statePath)); got != string(initialState) {
		t.Errorf("build state changed after save failure:\n%s", got)
	}
	if got := string(mustReadFile(t, "primary.out")); got != "old primary" {
		t.Errorf("primary output = %q, want rollback to old primary", got)
	}
}

func buildCLISuccessResponseWasm(t *testing.T, writeAuxiliary bool) string {
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
	return wasmPath
}
