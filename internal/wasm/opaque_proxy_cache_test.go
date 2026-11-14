package wasm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type opaqueOutputProxy struct {
	content string
	calls   *int
}

func (p opaqueOutputProxy) ProxyRequest(context.Context, string, []byte) ([]byte, error) {
	*p.calls++
	return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":1,"result":{"content":%q,"output_path":"page.out"}}`, p.content), nil
}

func TestPlugin_GIVENSameTypeOpaqueProxies_WHENConfigurationChanges_THENBypassesGenerationCache(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	wasmBytes := buildProxyOutputPlugin(t)
	ctx := context.Background()
	calls := 0

	generate := func(content string) {
		t.Helper()
		plugin, err := NewPluginWithOptions(ctx, wasmBytes, Options{Proxy: opaqueOutputProxy{content: content, calls: &calls}, Outputs: anyOutputs})
		if err != nil {
			t.Fatalf("NewPluginWithProxy() error = %v", err)
		}
		defer plugin.Close(ctx)
		if err := plugin.Generate("page.ktml"); err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
	}

	generate("first proxy")
	generate("second proxy")

	output, err := os.ReadFile("page.out")
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(output) != "second proxy" {
		t.Fatalf("output = %q, want second opaque proxy output", output)
	}
	if calls != 2 {
		t.Fatalf("proxy calls = %d, want both opaque proxies executed", calls)
	}
}

func TestPlugin_GIVENOpaqueProxyPublishedState_WHENNoProxyGenerates_THENDoesNotReuseOpaqueOutput(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	wasmBytes := buildProxyOutputPlugin(t)
	ctx := context.Background()
	calls := 0
	opaque, err := NewPluginWithOptions(ctx, wasmBytes, Options{Proxy: opaqueOutputProxy{content: "opaque", calls: &calls}, Outputs: anyOutputs})
	if err != nil {
		t.Fatalf("create opaque plugin: %v", err)
	}
	if err := opaque.Generate("page.ktml"); err != nil {
		t.Fatalf("opaque Generate() error = %v", err)
	}
	if err := opaque.Close(ctx); err != nil {
		t.Fatalf("close opaque plugin: %v", err)
	}

	withoutProxy, err := NewPluginWithOptions(ctx, wasmBytes, Options{Outputs: anyOutputs})
	if err != nil {
		t.Fatalf("create no-proxy plugin: %v", err)
	}
	t.Cleanup(func() { _ = withoutProxy.Close(ctx) })
	err = withoutProxy.Generate("page.ktml")
	if err == nil || !strings.Contains(err.Error(), "LSP proxy not configured") {
		t.Fatalf("no-proxy Generate() error = %v, want execution instead of opaque cache hit", err)
	}
	output, readErr := os.ReadFile("page.out")
	if readErr != nil || string(output) != "opaque" {
		t.Fatalf("output after rejected no-proxy generation = %q, %v", output, readErr)
	}
}

func buildProxyOutputPlugin(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "main.go")
	wasmPath := filepath.Join(dir, "plugin.wasm")
	source := `package main

import (
	"encoding/json"
	"unsafe"
)

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
	target := []byte("go")
	payload := []byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"test\"}")
	packed := proxyLSP(
		uint32(uintptr(unsafe.Pointer(&target[0]))), uint32(len(target)),
		uint32(uintptr(unsafe.Pointer(&payload[0]))), uint32(len(payload)),
	)
	response := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(uint32(packed >> 32)))), uint32(packed))
	var envelope struct { Result json.RawMessage ` + "`json:\"result\"`" + ` }
	if json.Unmarshal(response, &envelope) != nil || len(envelope.Result) == 0 {
		return 0
	}
	buffer = append(buffer[:0], envelope.Result...)
	return uint64(uint32(uintptr(unsafe.Pointer(&buffer[0]))))<<32 | uint64(len(buffer))
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
