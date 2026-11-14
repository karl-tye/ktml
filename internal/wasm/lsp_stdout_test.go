package wasm

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPPlugin_GIVENGuestWritesStdout_THENOutputReachesNeitherTheLSPStreamNorTheTerminal(t *testing.T) {
	ctx := context.Background()
	guest := buildStdoutGuest(t)
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	originalStdout, originalStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter
	plugin, err := NewLSPPlugin(ctx, guest, nil, nil, nil)
	if err == nil {
		_, err = plugin.ExecuteLSP(ctx, "textDocument/hover", []byte(`{}`))
		_ = plugin.Close(ctx)
	}
	os.Stdout, os.Stderr = originalStdout, originalStderr
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	if err != nil {
		t.Fatalf("plugin error = %v", err)
	}
	stdout, _ := io.ReadAll(stdoutReader)
	stderr, _ := io.ReadAll(stderrReader)
	if len(stdout) != 0 {
		t.Fatalf("guest wrote %q to stdout, want nothing", stdout)
	}
	if strings.Contains(string(stderr), "guest-stdout-marker") {
		t.Fatalf("stderr = %q, want guest stdout only in the host log", stderr)
	}
}

func buildStdoutGuest(t *testing.T) []byte {
	t.Helper()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "main.go")
	wasmPath := filepath.Join(directory, "plugin.wasm")
	source := `package main

import (
	"fmt"
	"unsafe"
)

var buffer []byte

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	if size == 0 {
		return 0
	}
	buffer = make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buffer[0])))
}

//go:wasmexport ktml_lsp_handle
func handle(_, _, _, _ uint32) uint64 {
	fmt.Println("guest-stdout-marker")
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
		t.Fatalf("build stdout guest: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read stdout guest: %v", err)
	}
	return contents
}
