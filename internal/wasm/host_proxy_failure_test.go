package wasm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type failingGenerationProxy struct{}

func (failingGenerationProxy) ProxyRequest(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("proxy unavailable")
}

func TestPlugin_GIVENGeneratorIgnoresLSPProxyErrorResponse_WHENExecutingGenerate_THENFailsBeforePublication(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	ctx := context.Background()
	plugin, err := NewPluginWithProxy(ctx, buildIgnoringProxyErrorPlugin(t), nil, nil, failingGenerationProxy{})
	if err != nil {
		t.Fatalf("NewPluginWithProxy() error = %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })

	err = plugin.Generate("page.ktml")

	if err == nil || !strings.Contains(err.Error(), "host LSP proxy failed: proxy unavailable") {
		t.Fatalf("Generate() error = %v, want captured LSP proxy failure", err)
	}
	if _, statErr := os.Stat("page.out"); !os.IsNotExist(statErr) {
		t.Fatalf("primary output published after proxy failure: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(".ktml", "state.json")); !os.IsNotExist(statErr) {
		t.Fatalf("build state published after proxy failure: %v", statErr)
	}
}

func buildIgnoringProxyErrorPlugin(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "main.go")
	wasmPath := filepath.Join(dir, "plugin.wasm")
	source := `package main

import "unsafe"

var buffer []byte

//go:wasmimport env ktml_host_proxy_lsp
func proxyLSP(targetPtr, targetLen, payloadPtr, payloadLen uint32) uint64

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	buffer = make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buffer[0])))
}

//go:wasmexport ktml_generate
func generate(_, _, _, _ uint32) uint64 {
	target := []byte("gopls")
	payload := []byte("{}")
	_ = proxyLSP(
		uint32(uintptr(unsafe.Pointer(&target[0]))), uint32(len(target)),
		uint32(uintptr(unsafe.Pointer(&payload[0]))), uint32(len(payload)),
	)
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
