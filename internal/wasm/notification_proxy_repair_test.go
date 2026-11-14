package wasm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type cancellationNoResponseProxy struct {
	called bool
}

func (p *cancellationNoResponseProxy) ProxyRequest(_ context.Context, target string, payload []byte) ([]byte, error) {
	if target != "gopls" {
		return nil, fmt.Errorf("target = %q", target)
	}
	var notification struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(payload, &notification); err != nil {
		return nil, err
	}
	if notification.Method != "$/cancelRequest" {
		return nil, fmt.Errorf("method = %q", notification.Method)
	}
	p.called = true
	return nil, nil
}

func TestPlugin_GIVENCancellationProxyWithoutResponse_THENReturnsPackedZeroWithoutAllocate(t *testing.T) {
	ctx := context.Background()
	proxy := &cancellationNoResponseProxy{}
	plugin, err := NewPluginWithProxy(ctx, buildCancellationProxyGuest(t), nil, nil, proxy)
	if err != nil {
		t.Fatalf("NewPluginWithProxy() error = %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })

	response, err := plugin.ExecuteLSP(ctx, "$/cancelRequest", []byte(`{"id":"client"}`))
	if err != nil {
		t.Fatalf("ExecuteLSP() error = %v", err)
	}
	if response != nil {
		t.Fatalf("ExecuteLSP() response = %q, want none", response)
	}
	if !proxy.called {
		t.Fatal("guest did not invoke the cancellation proxy")
	}
}

type failingProxy struct{}

func (failingProxy) ProxyRequest(context.Context, string, []byte) ([]byte, error) {
	return nil, fmt.Errorf("backend unavailable")
}

func TestPlugin_GIVENProxyError_WHENGuestProxies_THENHostWritesNothingToStdout(t *testing.T) {
	ctx := context.Background()
	plugin, err := NewPluginWithProxy(ctx, buildCancellationProxyGuest(t), nil, nil, failingProxy{})
	if err != nil {
		t.Fatalf("NewPluginWithProxy() error = %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	originalStdout := os.Stdout
	os.Stdout = writer
	// Stdout carries LSP frames in `ktml lsp`; host diagnostics must not corrupt it.
	_, _ = plugin.ExecuteLSP(ctx, "$/cancelRequest", []byte(`{"id":"client"}`))
	os.Stdout = originalStdout
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	written, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("host wrote %q to stdout, want nothing", written)
	}
}

func buildCancellationProxyGuest(t *testing.T) []byte {
	t.Helper()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "main.go")
	wasmPath := filepath.Join(directory, "plugin.wasm")
	source := `package main

import "unsafe"

var buffer []byte
var zeroAllocated bool

//go:wasmimport env ktml_host_proxy_lsp
func proxyLSP(targetPtr, targetLen, payloadPtr, payloadLen uint32) uint64

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	if size == 0 {
		zeroAllocated = true
		return 0
	}
	buffer = make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buffer[0])))
}

//go:wasmexport ktml_lsp_handle
func handle(_, _, _, _ uint32) uint64 {
	target := []byte("gopls")
	payload := []byte("{\"jsonrpc\":\"2.0\",\"method\":\"$/cancelRequest\",\"params\":{\"id\":\"client\"}}")
	packed := proxyLSP(
		uint32(uintptr(unsafe.Pointer(&target[0]))), uint32(len(target)),
		uint32(uintptr(unsafe.Pointer(&payload[0]))), uint32(len(payload)),
	)
	if packed != 0 {
		panic("notification returned a response")
	}
	if zeroAllocated {
		panic("allocate(0)")
	}
	return 0
}

//go:wasmexport ktml_manifest
func ktmlManifest() uint64 {
	m := []byte("{\"name\":\"test\",\"version\":\"0.0.0\",\"abi\":1,\"capabilities\":[\"generate\",\"format\",\"completion\"]}")
	return uint64(uintptr(unsafe.Pointer(&m[0])))<<32 | uint64(len(m))
}

func main() {}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("write guest source: %v", err)
	}
	command := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, sourcePath)
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build cancellation guest: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read cancellation guest: %v", err)
	}
	return contents
}
