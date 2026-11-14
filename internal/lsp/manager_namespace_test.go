package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

func TestManager_GIVENTwoNamespacesUseSameID_WHENOneCancels_THENNeitherCollidesAndOnlyItsOwnRequestIsCancelled(t *testing.T) {
	t.Setenv("KTML_LSP_NAMESPACE_HELPER", "1")
	ready := filepath.Join(t.TempDir(), "ready")
	manager := GetManager(lspNamespaceTestConfig("namespaces", ready))
	t.Cleanup(func() { _ = manager.Close() })
	first, second := manager.NewLSPNamespace(), manager.NewLSPNamespace()

	type outcome struct {
		payload []byte
		err     error
	}
	firstResult, secondResult := make(chan outcome, 1), make(chan outcome, 1)
	go func() {
		payload, err := first.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"a"}`))
		firstResult <- outcome{payload, err}
	}()
	go func() {
		payload, err := second.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"b"}`))
		secondResult <- outcome{payload, err}
	}()
	waitForHelperPID(t, ready)

	if response, err := second.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":1}}`)); err != nil || response != nil {
		t.Fatalf("cancellation = %s, %v", response, err)
	}
	for name, want := range map[string]struct {
		result chan outcome
		body   string
	}{
		"first":  {firstResult, `{"jsonrpc":"2.0","id":1,"result":"a"}`},
		"second": {secondResult, `{"jsonrpc":"2.0","id":1,"result":"cancelled-b"}`},
	} {
		select {
		case got := <-want.result:
			if got.err != nil || string(got.payload) != want.body {
				t.Errorf("%s response = %s, %v; want %s", name, got.payload, got.err, want.body)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s request remained blocked", name)
		}
	}
}

func TestLSPNamespaceHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_NAMESPACE_HELPER") != "1" {
		return
	}
	separator := 0
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator == 0 || len(os.Args) <= separator+2 {
		os.Exit(2)
	}
	mode, value := os.Args[separator+1], os.Args[separator+2]
	reader := bufio.NewReader(os.Stdin)
	switch mode {
	case "echo":
		for {
			payload, err := readLSPManagerTestFrame(reader)
			if err != nil {
				os.Exit(0)
			}
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(payload, &request) != nil {
				os.Exit(3)
			}
			if request.ID != nil {
				writeTransportResponse(request.ID, strconv.Quote(request.Method))
			}
		}
	case "namespaces":
		transportIDs := make(map[string]json.RawMessage)
		for range 2 {
			payload, err := readLSPManagerTestFrame(reader)
			if err != nil {
				os.Exit(4)
			}
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(payload, &request) != nil {
				os.Exit(5)
			}
			transportIDs[request.Method] = request.ID
		}
		if string(transportIDs["a"]) == string(transportIDs["b"]) {
			os.Exit(6)
		}
		if err := os.WriteFile(value, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(7)
		}
		cancel := mustReadTransportRequest(reader)
		var params struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(cancel.Params, &params) != nil {
			os.Exit(8)
		}
		for _, method := range []string{"a", "b"} {
			result := method
			if string(params.ID) == string(transportIDs[method]) {
				result = "cancelled-" + method
			}
			writeTransportResponse(transportIDs[method], strconv.Quote(result))
		}
	default:
		os.Exit(9)
	}
	os.Exit(0)
}

func lspNamespaceTestConfig(mode, value string) *config.Config {
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{
		os.Args[0], "-test.run=^TestLSPNamespaceHelperProcess$", "--", mode, value,
	}}
	return cfg
}
