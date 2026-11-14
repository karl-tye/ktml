package wasm

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/buildstate"
)

func TestProcessPluginResponse_GIVENOutputEscapesProject_WHENProcessed_THENRejectsWithoutMutation(t *testing.T) {
	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.out")
	if err := os.WriteFile(outside, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	t.Chdir(project)
	response, err := json.Marshal(PluginResponse{Content: "replace", OutputPath: outside})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	err = ProcessPluginResponse(&buildstate.Writer{Name: "test", Roots: []buildstate.OutputRoot{{Dir: "."}}}, response)

	if err == nil || !strings.Contains(err.Error(), "outside project root") {
		t.Fatalf("ProcessPluginResponse() error = %v, want project-root rejection", err)
	}
	content, readErr := os.ReadFile(outside)
	if readErr != nil || string(content) != "preserve" {
		t.Fatalf("outside output = %q, %v; want preserved", content, readErr)
	}
}

func TestPlugin_GIVENHostWriteCallbackFails_WHENGenerating_THENRejectsBeforePublication(t *testing.T) {
	tests := []struct {
		name string
		call string
	}{
		{name: "invalid path memory", call: `writeFile(^uint32(0), 1, 0, 0)`},
		{name: "invalid content memory", call: `path := []byte("aux.out"); writeFile(uint32(uintptr(unsafe.Pointer(&path[0]))), uint32(len(path)), ^uint32(0), 1)`},
		{name: "rejected auxiliary path", call: `path := []byte("../outside.out"); writeFile(uint32(uintptr(unsafe.Pointer(&path[0]))), uint32(len(path)), 0, 0)`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wasmBytes := buildHostWriteFailurePlugin(t, test.call)
			project := t.TempDir()
			t.Chdir(project)
			if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
				t.Fatalf("write source: %v", err)
			}
			ctx := context.Background()
			plugin, err := NewPlugin(ctx, wasmBytes, nil, nil)
			if err != nil {
				t.Fatalf("NewPlugin() error = %v", err)
			}
			t.Cleanup(func() { _ = plugin.Close(ctx) })

			err = plugin.Generate("page.ktml")

			if err == nil || !strings.Contains(err.Error(), "host write") {
				t.Fatalf("Generate() error = %v, want captured host write failure", err)
			}
			if _, statErr := os.Stat("page.out"); !os.IsNotExist(statErr) {
				t.Fatalf("primary output published after callback failure: %v", statErr)
			}
			if _, statErr := os.Stat(filepath.Join(".ktml", "state.json")); !os.IsNotExist(statErr) {
				t.Fatalf("build state published after callback failure: %v", statErr)
			}
		})
	}
}

func buildHostWriteFailurePlugin(t *testing.T, call string) []byte {
	t.Helper()
	dir := t.TempDir()
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
	` + call + `
	response := []byte("{\"content\":\"generated\",\"output_path\":\"page.out\"}")
	return uint64(uint32(uintptr(unsafe.Pointer(&response[0]))))<<32 | uint64(len(response))
}

//go:wasmexport ktml_manifest
func ktmlManifest() uint64 {
	m := []byte("{\"name\":\"test\",\"version\":\"0.0.0\",\"abi\":1,\"capabilities\":[\"generate\",\"format\",\"completion\"]}")
	return uint64(uintptr(unsafe.Pointer(&m[0])))<<32 | uint64(len(m))
}

func main() {}
`
	sourcePath := filepath.Join(dir, "main.go")
	wasmPath := filepath.Join(dir, "plugin.wasm")
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

func TestPlugin_GIVENAGuestWriteOutsideItsDeclaredOutputs_WHENGenerating_THENRefusedNamingPluginAndPath(t *testing.T) {
	tests := []struct{ name, path string }{
		{"git hook", ".git/hooks/pre-commit"},
		{"config", "ktml.config.json"},
		{"KTML source", "page.ktml"},
		{"prefix lookalike", "generated/x.go"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wasmBytes := buildHostWriteFailurePlugin(t, `path := []byte("`+test.path+`"); content := []byte("x"); writeFile(uint32(uintptr(unsafe.Pointer(&path[0]))), uint32(len(path)), uint32(uintptr(unsafe.Pointer(&content[0]))), 1)`)
			project := t.TempDir()
			t.Chdir(project)
			for _, dir := range []string{filepath.Join(".git", "hooks"), "gen", "generated"} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{"page.ktml": "Page { div }", filepath.Join(".git", "hooks", "pre-commit"): "#!/bin/sh\n", "ktml.config.json": "{}"}
			for name, content := range files {
				if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			plugin, err := NewPluginWithOptions(ctx, wasmBytes, Options{Name: "evil", Outputs: []buildstate.OutputRoot{{Dir: "gen"}, {Dir: ".", Suffixes: []string{".out"}}}})
			if err != nil {
				t.Fatalf("NewPluginWithOptions() error = %v", err)
			}
			t.Cleanup(func() { _ = plugin.Close(ctx) })

			err = plugin.Generate("page.ktml")

			if err == nil || !strings.Contains(err.Error(), `plugin "evil"`) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("Generate() error = %v; want a refusal naming plugin \"evil\" and %s", err, test.path)
			}
			for name, content := range files {
				if got, _ := os.ReadFile(name); string(got) != content {
					t.Fatalf("%s = %q; want it unchanged", name, got)
				}
			}
			for _, name := range []string{"page.out", filepath.Join("generated", "x.go"), filepath.Join(".ktml", "state.json")} {
				if _, statErr := os.Stat(name); !os.IsNotExist(statErr) {
					t.Fatalf("%s stat error = %v; want nothing published", name, statErr)
				}
			}
		})
	}
}
