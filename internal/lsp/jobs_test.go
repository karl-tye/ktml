package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/wasm"
)

// jobGuest hands out one background job that takes 1.5 s, answers hover
// immediately, and publishes a diagnostic when the job's result arrives.
const jobGuest = `package main

import (
	"strings"
	"time"
	"unsafe"
)

var keep [][]byte
var handedOut bool

func pack(s string) uint64 {
	b := []byte(s)
	keep = append(keep, b)
	return uint64(uintptr(unsafe.Pointer(&b[0])))<<32 | uint64(len(b))
}

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	b := make([]byte, size+1)
	keep = append(keep, b)
	return uint32(uintptr(unsafe.Pointer(&b[0])))
}

//go:wasmexport ktml_manifest
func manifest() uint64 {
	return pack("{\"name\":\"jobs\",\"version\":\"1.0.0\",\"abi\":1,\"capabilities\":[\"completion\"]}")
}

//go:wasmexport ktml_lsp_handle
func handle(mPtr, mLen, pPtr, pLen uint32) uint64 {
	method := string(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(mPtr))), mLen))
	payload := string(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(pPtr))), pLen))
	switch method {
	case "$/ktml/pendingJob":
		if handedOut {
			return 0
		}
		handedOut = true
		return pack("{\"kind\":\"slow\"}")
	case "$/ktml/runJob":
		time.Sleep(1500 * time.Millisecond)
		return pack("{\"done\":true}")
	case "$/ktml/jobResult":
		if !strings.Contains(payload, "\"done\":true") || !strings.Contains(payload, "\"kind\":\"slow\"") {
			return pack("{\"jsonrpc\":\"2.0\",\"method\":\"window/logMessage\",\"params\":{\"type\":1,\"message\":\"bad delivery\"}}")
		}
		return pack("{\"jsonrpc\":\"2.0\",\"method\":\"textDocument/publishDiagnostics\",\"params\":{\"uri\":\"file:///job\",\"diagnostics\":[{\"range\":{\"start\":{\"line\":0,\"character\":0},\"end\":{\"line\":0,\"character\":1}},\"message\":\"from job\"}]}}")
	case "textDocument/hover":
		return pack("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"contents\":\"quick\"}}")
	}
	return 0
}

func main() {}
`

func buildJobGuest(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(jobGuest), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", "plugin.wasm", "main.go")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build guest: %v\n%s", err, output)
	}
	wasmBytes, err := os.ReadFile(filepath.Join(dir, "plugin.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	return wasmBytes
}

func frame(message string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(message), message)
}

func TestServe_GIVENPluginBackgroundJob_WHENRequestsArriveMeanwhile_THENAnswersThemFirstAndPublishesTheResultLater(t *testing.T) {
	ctx := context.Background()
	plugin, err := wasm.NewPluginWithOptions(ctx, buildJobGuest(t), wasm.Options{})
	if err != nil {
		t.Fatalf("load guest: %v", err)
	}
	defer plugin.Close(ctx)

	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	go func() {
		Serve(ctx, inputReader, outputWriter, []*wasm.Plugin{plugin})
		outputWriter.Close()
	}()
	messages := make(chan map[string]any)
	go func() {
		reader := bufio.NewReader(outputReader)
		for {
			payload, ok := readFrame(reader)
			if !ok {
				close(messages)
				return
			}
			var message map[string]any
			_ = json.Unmarshal(payload, &message)
			messages <- message
		}
	}()

	started := time.Now()
	io.WriteString(inputWriter, frame(`{"jsonrpc":"2.0","id":1,"method":"textDocument/hover","params":{"textDocument":{"uri":"file:///a"},"position":{"line":0,"character":0}}}`))
	first := <-messages
	if first["id"] != float64(1) || time.Since(started) > time.Second {
		t.Fatalf("first message = %#v after %s, want the hover answer before the job finishes", first, time.Since(started))
	}
	select {
	case second := <-messages:
		encoded, _ := json.Marshal(second)
		if !bytes.Contains(encoded, []byte(`"message":"from job"`)) {
			t.Fatalf("second message = %s, want the job's diagnostics", encoded)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the job result was never published")
	}
	inputWriter.Close()
}

func TestServe_GIVENClientWithDynamicWatchers_WHENInitialized_THENRegistersGoFileWatching(t *testing.T) {
	var output bytes.Buffer
	input := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"workspace":{"didChangeWatchedFiles":{"dynamicRegistration":true}}}}}`) +
		frame(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	Serve(context.Background(), strings.NewReader(input), &output, nil)
	if !strings.Contains(output.String(), `"method":"client/registerCapability"`) || !strings.Contains(output.String(), `"globPattern":"**/*.go"`) {
		t.Errorf("output = %s, want a **/*.go watcher registration", output.String())
	}

	output.Reset()
	input = frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}`) + frame(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	Serve(context.Background(), strings.NewReader(input), &output, nil)
	if strings.Contains(output.String(), "registerCapability") {
		t.Errorf("output = %s, want no registration for a client without dynamic watchers", output.String())
	}
}
