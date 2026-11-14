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
	"strconv"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/wasm"
)

func TestServe_GIVENProductionPluginAndLosslessIDs_THENPreservesExactOuterID(t *testing.T) {
	ctx := context.Background()
	plugin, err := wasm.NewPlugin(ctx, buildProductionGohtmlPlugin(t), nil, nil)
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })

	ids := []json.RawMessage{json.RawMessage(`"plain"`), json.RawMessage(`"\u0073ame"`), json.RawMessage(`1.0`)}
	var input bytes.Buffer
	for _, id := range ids {
		payload := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"textDocument/completion","params":{"textDocument":{"uri":"file:///missing.ktml"},"position":{"line":0,"character":0}}}`, id)
		fmt.Fprintf(&input, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
	}

	var output bytes.Buffer
	Serve(ctx, &input, &output, []*wasm.Plugin{plugin})
	reader := bufio.NewReader(&output)
	for _, wantID := range ids {
		payload := readServeTestFrame(t, reader)
		message := classifyRPCMessage(payload)
		if message.err != nil || message.kind != rpcResponse {
			t.Fatalf("response = %s, classification error = %v", payload, message.err)
		}
		if string(message.id) != string(wantID) {
			t.Fatalf("response ID = %s, want exact %s", message.id, wantID)
		}
	}
	if remainder, err := io.ReadAll(reader); err != nil || len(remainder) != 0 {
		t.Fatalf("unexpected trailing output = %q, %v", remainder, err)
	}
}

func buildProductionGohtmlPlugin(t *testing.T) []byte {
	t.Helper()
	wasmPath := filepath.Join(t.TempDir(), "gohtml.wasm")
	command := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, ".")
	command.Dir = filepath.Join("..", "..", "cmd", "plugins", "gohtml")
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build production gohtml plugin: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read production gohtml plugin: %v", err)
	}
	return contents
}

func readServeTestFrame(t *testing.T, reader *bufio.Reader) []byte {
	t.Helper()
	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read response header: %v", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		contentLength, err = strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			t.Fatalf("parse response Content-Length: %v", err)
		}
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatalf("read response payload: %v", err)
	}
	return payload
}
