package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

func TestManager_GIVENResponseWithoutResultOrError_WHENRequestIsPending_THENFailsServer(t *testing.T) {
	t.Setenv("KTML_LSP_ENVELOPE_HELPER", "1")
	manager := GetManager(lspEnvelopeTestConfig(`{"jsonrpc":"2.0","id":1}`))
	t.Cleanup(func() { _ = manager.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := manager.ProxyRequest(ctx, "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid LSP response envelope") {
		t.Fatalf("ProxyRequest() error = %v, want invalid response envelope", err)
	}
}

func TestManager_GIVENMalformedResponseEnvelope_WHENRequestIsPending_THENFailsServer(t *testing.T) {
	t.Setenv("KTML_LSP_ENVELOPE_HELPER", "1")
	responses := []string{
		`{"jsonrpc":"2.0","id":1,"result":null,"error":{"code":-32000,"message":"both"}}`,
		`{"jsonrpc":"2.0","id":1,"error":null}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":null,"message":"null code"}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32000}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":null}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":1.5,"message":"fractional"}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"code":-32001,"message":"duplicate"}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"first","message":"second"}}`,
		`{"jsonrpc":"1.0","id":1,"result":null}`,
		`{"jsonrpc":"2.0","id":true,"result":null}`,
		`{"jsonrpc":"2.0","id":1.5,"result":null}`,
		`{"jsonrpc":"2.0","jsonrpc":"2.0","id":1,"result":null}`,
		`{"jsonrpc":"2.0","id":1,"id":2,"result":null}`,
		`{"jsonrpc":"2.0","id":1,"result":null,"result":null}`,
		`{"jsonrpc":"2.0","id":1,"content":"generated"}`,
		`{"jsonrpc":"2.0","id":1,"output_path":"page.out"}`,
		`{"jsonrpc":"2.0","id":1,"content":null,"output_path":"page.out"}`,
		`{"jsonrpc":"2.0","id":1,"content":"generated","output_path":"page.out"}`,
	}
	for _, response := range responses {
		t.Run(response, func(t *testing.T) {
			manager := GetManager(lspEnvelopeTestConfig(response))
			t.Cleanup(func() { _ = manager.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			_, err := manager.ProxyRequest(ctx, "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
			if err == nil || !strings.Contains(err.Error(), "invalid LSP") {
				t.Fatalf("ProxyRequest() error = %v, want malformed envelope failure", err)
			}
		})
	}
}

func TestManager_GIVENNullResultResponse_WHENRequestIsPending_THENReturnsIt(t *testing.T) {
	t.Setenv("KTML_LSP_ENVELOPE_HELPER", "1")
	response := `{"jsonrpc":"2.0","id":1,"result":null}`
	manager := GetManager(lspEnvelopeTestConfig(response))
	t.Cleanup(func() { _ = manager.Close() })

	actual, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if err != nil {
		t.Fatalf("ProxyRequest() error = %v", err)
	}
	if string(actual) != response {
		t.Fatalf("ProxyRequest() response = %s, want null result", actual)
	}
}

func TestManager_GIVENGeneratedOutputResult_WHENRequestIsPending_THENReturnsValidEnvelope(t *testing.T) {
	t.Setenv("KTML_LSP_ENVELOPE_HELPER", "1")
	response := `{"jsonrpc":"2.0","id":1,"result":{"content":"generated","output_path":"page.out"}}`
	manager := GetManager(lspEnvelopeTestConfig(response))
	t.Cleanup(func() { _ = manager.Close() })

	actual, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if err != nil {
		t.Fatalf("ProxyRequest() error = %v", err)
	}
	if string(actual) != response {
		t.Fatalf("ProxyRequest() response = %s, want generated-output result", actual)
	}
}

func TestManager_GIVENValidErrorData_WHENRequestIsPending_THENPreservesIt(t *testing.T) {
	t.Setenv("KTML_LSP_ENVELOPE_HELPER", "1")
	response := `{"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"failed","data":{"retry":false}}}`
	manager := GetManager(lspEnvelopeTestConfig(response))
	t.Cleanup(func() { _ = manager.Close() })
	actual, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":"client","method":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"jsonrpc":"2.0","id":"client","error":{"code":-32001,"message":"failed","data":{"retry":false}}}`
	if string(actual) != want {
		t.Fatalf("response = %s, want %s", actual, want)
	}
}

func TestManager_GIVENMalformedRequestEnvelope_WHENProxied_THENRejectsBeforeServerLaunch(t *testing.T) {
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{"ktml-lsp-envelope-helper-that-does-not-exist"}}
	manager := GetManager(cfg)
	t.Cleanup(func() { _ = manager.Close() })
	payloads := []string{
		`{"jsonrpc":"2.0","id":1}`,
		`{"jsonrpc":"2.0","id":1,"method":null}`,
		`{"jsonrpc":"2.0","id":1,"method":42}`,
		`{"jsonrpc":"2.0","method":"notification"}`,
		`{"jsonrpc":"2.0","id":1,"method":"test","result":null}`,
		`{"jsonrpc":"2.0","id":1,"method":"test","error":{"code":-32000,"message":"bad"}}`,
		`{"jsonrpc":"1.0","id":1,"method":"test"}`,
		`{"jsonrpc":2,"id":1,"method":"test"}`,
		`{"jsonrpc":"2.0","jsonrpc":"2.0","id":1,"method":"test"}`,
		`{"jsonrpc":"2.0","id":1,"id":2,"method":"test"}`,
		`{"jsonrpc":"2.0","id":1,"method":"test","method":"other"}`,
		`{"jsonrpc":"2.0","id":1,"method":"test","params":null}`,
		`{"jsonrpc":"2.0","id":1,"method":"test","params":"bad"}`,
	}

	for _, payload := range payloads {
		if _, err := manager.ProxyRequest(context.Background(), "go", []byte(payload)); err == nil || !strings.Contains(err.Error(), "invalid JSON-RPC request envelope") {
			t.Fatalf("ProxyRequest(%s) error = %v, want request envelope rejection", payload, err)
		}
	}
}

func TestManager_GIVENValidRequestEnvelopeEdgeCases_WHENProxied_THENPreservesIDs(t *testing.T) {
	t.Setenv("KTML_LSP_ENVELOPE_HELPER", "1")
	for _, payload := range []string{
		`{"jsonrpc":"2.0","id":"","method":"test","params":[]}`,
		`{"jsonrpc":"2.0","id":1.0,"method":"test","params":{}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			var request struct {
				ID json.RawMessage `json:"id"`
			}
			if err := json.Unmarshal([]byte(payload), &request); err != nil {
				t.Fatal(err)
			}
			response := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":null}`, request.ID)
			manager := GetManager(lspEnvelopeTestConfig(response))
			t.Cleanup(func() { _ = manager.Close() })
			actual, err := manager.ProxyRequest(context.Background(), "go", []byte(payload))
			if err != nil {
				t.Fatalf("ProxyRequest() error = %v", err)
			}
			if string(actual) != response {
				t.Fatalf("ProxyRequest() response = %s, want %s", actual, response)
			}
		})
	}
}

func TestLSPEnvelopeHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_ENVELOPE_HELPER") != "1" {
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
	response := os.Args[separator+1]
	request := classifyRPCMessage(requestPayload)
	backendResponse := classifyRPCMessage([]byte(response))
	if request.kind == rpcRequest && backendResponse.kind == rpcResponse {
		rewritten, rewriteErr := rewriteRPCMembers(backendResponse.object, map[string]json.RawMessage{"id": request.id})
		if rewriteErr != nil {
			os.Exit(4)
		}
		response = string(rewritten)
	}
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(response), response)
	os.Exit(0)
}

func lspEnvelopeTestConfig(response string) *config.Config {
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{
		os.Args[0], "-test.run=^TestLSPEnvelopeHelperProcess$", "--", response,
	}}
	return cfg
}
