package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

func TestServe_GIVENConflictingContentLengthHeaders_WHENReceived_THENStopsWithoutOutput(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"method":"test"}`
	input := bytes.NewBufferString(fmt.Sprintf("Content-Length: 1\r\nContent-Length: %d\r\n\r\n%s", len(payload), payload))
	var output bytes.Buffer
	Serve(context.Background(), input, &output, nil)
	if output.Len() != 0 {
		t.Fatalf("Serve() output = %q, want no response for ambiguously framed input", output.String())
	}
}

func TestServe_GIVENRepeatedEqualContentLengthHeaders_WHENReceived_THENStillAnswers(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"method":"test"}`
	input := bytes.NewBufferString(fmt.Sprintf("Content-Length: %d\r\nContent-Length: %d\r\n\r\n%s", len(payload), len(payload), payload))
	var output bytes.Buffer
	Serve(context.Background(), input, &output, nil)
	if output.Len() == 0 {
		t.Fatal("Serve() wrote nothing for consistently framed input")
	}
}

func TestManager_GIVENBackendSendsConflictingContentLengthHeaders_THENRequestFails(t *testing.T) {
	t.Setenv("KTML_LSP_CONTENT_LENGTH_HELPER", "1")
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{os.Args[0], "-test.run=^TestLSPContentLengthHelperProcess$"}}
	manager := GetManager(cfg)
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	response, err := manager.ProxyRequest(ctx, "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if err == nil || !strings.Contains(err.Error(), "conflicting LSP Content-Length") {
		t.Fatalf("ProxyRequest() = %s, %v; want conflicting Content-Length error", response, err)
	}
}

func TestLSPContentLengthHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_CONTENT_LENGTH_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	payload, err := readLSPManagerTestFrame(reader)
	if err != nil {
		os.Exit(2)
	}
	var request struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(payload, &request) != nil {
		os.Exit(3)
	}
	response := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"ok"}`, request.ID)
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: 1\r\nContent-Length: %d\r\n\r\n%s", len(response), response)
	_, _ = reader.ReadByte()
	os.Exit(0)
}
