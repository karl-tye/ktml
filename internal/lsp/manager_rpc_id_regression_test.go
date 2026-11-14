package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

func TestManager_GIVENEscapedStringRequestID_THENResponsePreservesExactClientID(t *testing.T) {
	t.Setenv("KTML_LSP_ID_HELPER", "1")
	manager := GetManager(lspRPCIDTestConfig("escaped-string"))
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	response, err := manager.ProxyRequest(ctx, "go", []byte(`{"jsonrpc":"2.0","id":"\u0073ame","method":"test"}`))
	if err != nil {
		t.Fatalf("ProxyRequest() error = %v", err)
	}
	if string(response) != `{"jsonrpc":"2.0","id":"\u0073ame","result":"matched"}` {
		t.Fatalf("response = %s", response)
	}
}

func TestManager_GIVENEquivalentNumericClientID_THENResponsePreservesExactClientID(t *testing.T) {
	t.Setenv("KTML_LSP_ID_HELPER", "1")
	manager := GetManager(lspRPCIDTestConfig("equivalent-number"))
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	response, err := manager.ProxyRequest(ctx, "go", []byte(`{"jsonrpc":"2.0","id":1.0,"method":"test"}`))
	if err != nil {
		t.Fatalf("ProxyRequest() error = %v", err)
	}
	if string(response) != `{"jsonrpc":"2.0","id":1.0,"result":"matched"}` {
		t.Fatalf("response = %s", response)
	}
}

func TestManager_GIVENInvalidRequestIDs_THENRejectsThemWithoutCollisionOrWait(t *testing.T) {
	manager := GetManager(lspRPCIDTestConfig("escaped-string"))
	t.Cleanup(func() { _ = manager.Close() })
	for _, payload := range []string{
		`{"jsonrpc":"2.0","id":null,"method":"test"}`,
		`{"jsonrpc":"2.0","id":true,"method":"test"}`,
		`{"jsonrpc":"2.0","id":1.5,"method":"test"}`,
		`{"jsonrpc":"2.0","id":{},"method":"test"}`,
		`{"jsonrpc":"2.0","id":[],"method":"test"}`,
		`{"jsonrpc":"2.0","method":"test"}`,
	} {
		started := time.Now()
		if _, err := manager.ProxyRequest(context.Background(), "go", []byte(payload)); err == nil || !strings.Contains(err.Error(), "request ID") {
			t.Fatalf("ProxyRequest(%s) error = %v, want invalid request ID", payload, err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("invalid request ID took %v to reject", elapsed)
		}
	}
}

func TestManager_GIVENInvalidResponseID_WHENRequestIsPending_THENFailsWithoutHanging(t *testing.T) {
	t.Setenv("KTML_LSP_ID_HELPER", "1")
	manager := GetManager(lspRPCIDTestConfig("invalid-response"))
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := manager.ProxyRequest(ctx, "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid LSP response ID") {
		t.Fatalf("ProxyRequest() error = %v, want invalid response ID", err)
	}
}

func TestManager_GIVENEquivalentIDIsAlreadyPending_WHENRequestedAgain_THENRejectsDuplicateWithoutCollision(t *testing.T) {
	t.Setenv("KTML_LSP_ID_HELPER", "1")
	readyPath := filepath.Join(t.TempDir(), "ready")
	manager := GetManager(lspRPCIDTestConfig("hold", readyPath))
	t.Cleanup(func() { _ = manager.Close() })
	firstResult := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":"same","method":"first"}`))
		firstResult <- err
	}()
	waitForHelperPID(t, readyPath)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := manager.ProxyRequest(ctx, "go", []byte(`{"jsonrpc":"2.0","id":"\u0073ame","method":"second"}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate JSON-RPC request ID") {
		t.Fatalf("second ProxyRequest() error = %v, want duplicate ID rejection", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-firstResult:
		if err == nil {
			t.Fatal("first pending request succeeded during duplicate test")
		}
	case <-time.After(time.Second):
		t.Fatal("first pending request remained blocked after Close")
	}
}

func TestLSPRPCIDHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_ID_HELPER") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator == 0 || len(os.Args) <= separator+1 {
		os.Exit(2)
	}
	requestPayload, err := readLSPManagerTestFrame(bufio.NewReader(os.Stdin))
	if err != nil {
		os.Exit(3)
	}
	var request struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(requestPayload, &request) != nil {
		os.Exit(7)
	}
	var response string
	switch os.Args[separator+1] {
	case "escaped-string":
		response = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"matched"}`, request.ID)
	case "equivalent-number":
		response = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"matched"}`, request.ID)
	case "invalid-response":
		response = `{"jsonrpc":"2.0","id":{},"result":"invalid"}`
	case "hold":
		if len(os.Args) <= separator+2 {
			os.Exit(5)
		}
		if err := os.WriteFile(os.Args[separator+2], []byte(fmt.Sprint(os.Getpid())), 0o600); err != nil {
			os.Exit(6)
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	default:
		os.Exit(4)
	}
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(response), response)
	os.Exit(0)
}

func lspRPCIDTestConfig(mode string, values ...string) *config.Config {
	cfg := config.Default()
	command := []string{os.Args[0], "-test.run=^TestLSPRPCIDHelperProcess$", "--", mode}
	command = append(command, values...)
	cfg.LSP["go"] = config.LSP{Command: command}
	return cfg
}
