package wasm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewPlugin_GIVENLSPProxyFailsDuringConfigure_THENConstructionFails(t *testing.T) {
	ctx := context.Background()
	plugin, err := NewPluginWithProxy(ctx, buildConfigureProxyPlugin(t), nil, nil, failingGenerationProxy{})
	if plugin != nil {
		t.Cleanup(func() { _ = plugin.Close(ctx) })
		t.Fatal("NewPluginWithProxy() returned a plugin after Configure proxy failure")
	}
	if err == nil || !strings.Contains(err.Error(), "host LSP proxy failed: proxy unavailable") {
		t.Fatalf("NewPluginWithProxy() error = %v, want Configure proxy failure", err)
	}
}

func buildConfigureProxyPlugin(t *testing.T) []byte {
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

//go:wasmexport ktml_configure
func configure(_, _ uint32) {
	target := []byte("gopls")
	payload := []byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}")
	_ = proxyLSP(
		uint32(uintptr(unsafe.Pointer(&target[0]))), uint32(len(target)),
		uint32(uintptr(unsafe.Pointer(&payload[0]))), uint32(len(payload)),
	)
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
