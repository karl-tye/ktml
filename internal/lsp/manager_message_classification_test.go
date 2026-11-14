package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

func TestManager_GIVENServerRequestCollidesWithPendingID_WHENResponseArrivesLater_THENDeliversResponse(t *testing.T) {
	t.Setenv("KTML_LSP_MESSAGE_HELPER", "1")
	manager := GetManager(lspMessageClassificationTestConfig())
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	response, err := manager.ProxyRequest(ctx, "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if err != nil {
		t.Fatalf("ProxyRequest() error = %v", err)
	}
	if string(response) != `{"jsonrpc":"2.0","id":1,"result":"actual-response"}` {
		t.Fatalf("response = %s, want actual response after colliding server request", response)
	}
}

func TestLSPMessageClassificationHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_MESSAGE_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	requestPayload, err := readLSPManagerTestFrame(reader)
	if err != nil {
		os.Exit(2)
	}
	var pendingRequest struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(requestPayload, &pendingRequest) != nil {
		os.Exit(7)
	}
	writeLSPMessageClassificationTestFrame(`{"jsonrpc":"2.0","id":9,"method":null}`)
	invalidResponse, err := readLSPManagerTestFrame(reader)
	if err != nil {
		os.Exit(3)
	}
	var invalidBackendError struct {
		ID    json.RawMessage `json:"id"`
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(invalidResponse, &invalidBackendError) != nil || string(invalidBackendError.ID) != "9" || invalidBackendError.Error.Code != -32600 {
		os.Exit(4)
	}
	writeLSPMessageClassificationTestFrame(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"workspace/configuration","params":{}}`, pendingRequest.ID))
	response, err := readLSPManagerTestFrame(reader)
	if err != nil {
		os.Exit(5)
	}
	var backendError struct {
		ID    json.RawMessage `json:"id"`
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(response, &backendError) != nil || string(backendError.ID) != string(pendingRequest.ID) || backendError.Error.Code != -32601 {
		os.Exit(6)
	}
	writeLSPMessageClassificationTestFrame(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"actual-response"}`, pendingRequest.ID))
	os.Exit(0)
}

func writeLSPMessageClassificationTestFrame(payload string) {
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
}

func lspMessageClassificationTestConfig() *config.Config {
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{
		os.Args[0], "-test.run=^TestLSPMessageClassificationHelperProcess$",
	}}
	return cfg
}
