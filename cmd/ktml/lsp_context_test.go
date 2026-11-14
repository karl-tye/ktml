package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGenerateFileContext_GIVENLiveLSPNeverReplies_WHENCallerCancels_THENFailsAndReapsWithoutPublication(t *testing.T) {
	t.Setenv("KTML_CLI_LSP_HELPER", "1")
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	pidPath := filepath.Join(project, "lsp.pid")
	pluginPath := buildCLIProxyOutputPlugin(t)
	commandJSON := quoteJSON(t, os.Args[0]) + `,"-test.run=^TestCLISilentLSPHelperProcess$","--",` + quoteJSON(t, pidPath)
	configJSON := `{"package_dir":".","lsp":{"go":{"command":[` + commandJSON + `]}},"resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":` + quoteJSON(t, pluginPath) + `}}}`
	if err := os.WriteFile("ktml.config.json", []byte(configJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- generateFileContext(ctx, "page.ktml", false) }()
	pid := waitForCLILSPPID(t, pidPath)
	cancel()
	var err error
	select {
	case err = <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("generateFileContext() did not honor caller cancellation")
	}
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("generateFileContext() error = %v, want caller cancellation", err)
	}
	if _, statErr := os.Stat("page.out"); !os.IsNotExist(statErr) {
		t.Fatalf("output published after cancellation: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(".ktml", "state.json")); !os.IsNotExist(statErr) {
		t.Fatalf("build state published after cancellation: %v", statErr)
	}
	waitForCLIProcessExit(t, pid)
}

func TestCLISilentLSPHelperProcess(t *testing.T) {
	if os.Getenv("KTML_CLI_LSP_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" && len(os.Args) > i+1 {
			if err := os.WriteFile(os.Args[i+1], []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
				os.Exit(2)
			}
			_, _ = io.Copy(io.Discard, os.Stdin)
			os.Exit(0)
		}
	}
	os.Exit(3)
}

func buildCLIProxyOutputPlugin(t *testing.T) string {
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
	return wasmPath
}

func waitForCLILSPPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(string(contents))
			if err != nil {
				t.Fatalf("parse LSP PID: %v", err)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("silent LSP did not report PID at %s", path)
	return 0
}

func waitForCLIProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("CLI LSP process %d remains after generation cancellation", pid)
}
