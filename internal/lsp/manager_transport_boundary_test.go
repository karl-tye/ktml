package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

func TestExternalServer_GIVENResponseWinsBeforeTimeoutCleanupAndSameIDReuse_THENOldTimeoutCannotStopNewOwner(t *testing.T) {
	old := &pendingRequest{result: make(chan proxyResult, 1), clientID: json.RawMessage("1"), clientKey: "n:1e0"}
	server := &ExternalServer{
		requests:       map[string]*pendingRequest{"n:1e0": old},
		clientRequests: map[string]string{"n:1e0": "n:1e0"},
		accepting:      true,
	}
	response := classifyRPCMessage([]byte(`{"jsonrpc":"2.0","id":1,"result":"old"}`))
	server.deliverResponse(response)

	newer := &pendingRequest{result: make(chan proxyResult, 1), clientID: json.RawMessage("1"), clientKey: "n:1e0"}
	server.mu.Lock()
	server.requests["n:2e0"] = newer
	server.clientRequests["n:1e0"] = "n:2e0"
	server.mu.Unlock()

	if server.retireRequest("n:1e0", old, true) {
		t.Fatal("stale timeout cleanup reclaimed response-owned request")
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if !server.accepting || server.requests["n:2e0"] != newer {
		t.Fatal("stale timeout stopped or removed the newer same-ID request")
	}
}

func TestExternalServer_GIVENTimeoutWinsOwnership_THENServerIsNonAcceptingBeforeRetirement(t *testing.T) {
	request := &pendingRequest{result: make(chan proxyResult, 1), clientID: json.RawMessage("1"), clientKey: "n:1e0"}
	server := &ExternalServer{
		requests:       map[string]*pendingRequest{"n:1e0": request},
		clientRequests: map[string]string{"n:1e0": "n:1e0"},
		accepting:      true,
	}
	if !server.retireRequest("n:1e0", request, true) {
		t.Fatal("timeout did not win request ownership")
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.accepting || len(server.requests) != 0 || len(server.clientRequests) != 0 {
		t.Fatal("timeout released ownership without atomically making the server non-accepting")
	}
}

func TestExternalServer_GIVENSimultaneousTimeoutAndResponse_THENExactlyOneOwnsRequest(t *testing.T) {
	for range 1_000 {
		request := &pendingRequest{result: make(chan proxyResult, 1), clientID: json.RawMessage("1"), clientKey: "n:1e0"}
		server := &ExternalServer{
			requests:       map[string]*pendingRequest{"n:1e0": request},
			clientRequests: map[string]string{"n:1e0": "n:1e0"},
			accepting:      true,
		}
		response := classifyRPCMessage([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		start := make(chan struct{})
		timeoutWon := make(chan bool, 1)
		delivered := make(chan struct{}, 1)
		go func() {
			<-start
			timeoutWon <- server.retireRequest("n:1e0", request, true)
		}()
		go func() {
			<-start
			server.deliverResponse(response)
			delivered <- struct{}{}
		}()
		close(start)
		won := <-timeoutWon
		<-delivered
		select {
		case <-request.result:
			if won {
				t.Fatal("timeout and response both won ownership")
			}
		default:
			if !won {
				t.Fatal("neither timeout nor response won ownership")
			}
		}
		server.mu.Lock()
		if won && server.accepting {
			server.mu.Unlock()
			t.Fatal("timeout winner left server accepting")
		}
		server.mu.Unlock()
	}
}

func TestExternalServer_GIVENCancellationChurn_THENRetirementMemoryRemainsBounded(t *testing.T) {
	server := &ExternalServer{
		requests:       make(map[string]*pendingRequest),
		clientRequests: make(map[string]string),
		accepting:      true,
	}
	for id := 1; id <= 10_000; id++ {
		transportKey := fmt.Sprintf("n:%de0", id)
		clientKey := fmt.Sprintf("s:client-%d", id)
		request := &pendingRequest{result: make(chan proxyResult, 1), clientID: json.RawMessage(strconv.Quote(clientKey)), clientKey: clientKey}
		server.requests[transportKey] = request
		server.clientRequests[clientKey] = transportKey
		if !server.retireRequest(transportKey, request, false) {
			t.Fatalf("retire transport %d failed", id)
		}
	}
	if len(server.requests) != 0 || len(server.clientRequests) != 0 || !server.accepting {
		t.Fatalf("retirement state = requests:%d clients:%d accepting:%v", len(server.requests), len(server.clientRequests), server.accepting)
	}
}

func TestManager_GIVENDuplicateStaleBackendResponseAndReusedClientID_THENOnlyTransportIDResponseResolvesNewRequest(t *testing.T) {
	t.Setenv("KTML_LSP_TRANSPORT_HELPER", "1")
	manager := GetManager(lspTransportTestConfig("duplicate", ""))
	t.Cleanup(func() { _ = manager.Close() })

	first, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":"same","method":"first"}`))
	if err != nil || string(first) != `{"jsonrpc":"2.0","id":"same","result":"first"}` {
		t.Fatalf("first response = %s, %v", first, err)
	}
	second, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":"same","method":"second"}`))
	if err != nil || string(second) != `{"jsonrpc":"2.0","id":"same","result":"second"}` {
		t.Fatalf("second response = %s, %v", second, err)
	}
}

func TestManager_GIVENCancellationRequestForClientID_THENBackendReceivesTransportID(t *testing.T) {
	t.Setenv("KTML_LSP_TRANSPORT_HELPER", "1")
	ready := filepath.Join(t.TempDir(), "ready")
	manager := GetManager(lspTransportTestConfig("cancel", ready))
	t.Cleanup(func() { _ = manager.Close() })

	firstResult := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":"original","method":"work"}`))
		firstResult <- err
	}()
	waitForHelperPID(t, ready)
	cancelResponse, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":"original"}}`))
	if err != nil || cancelResponse != nil {
		t.Fatalf("cancellation response = %s, %v", cancelResponse, err)
	}
	select {
	case err := <-firstResult:
		if err != nil {
			t.Fatalf("original request error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("original request remained blocked")
	}
}

func TestManager_GIVENStaleCancellationWithoutServer_THENIgnoresItWithoutLaunching(t *testing.T) {
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{"missing-lsp-executable"}}
	manager := GetManager(cfg)
	t.Cleanup(func() { _ = manager.Close() })
	response, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":1}}`))
	if err != nil || response != nil {
		t.Fatalf("stale cancellation response = %s, %v", response, err)
	}
	manager.core.mu.Lock()
	defer manager.core.mu.Unlock()
	if len(manager.core.servers) != 0 {
		t.Fatal("stale cancellation launched a backend server")
	}
}

func TestLSPTransportBoundaryHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_TRANSPORT_HELPER") != "1" {
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
	first := mustReadTransportRequest(reader)
	switch mode {
	case "duplicate":
		writeTransportResponse(first.ID, `"first"`)
		writeTransportResponse(first.ID, `"stale-duplicate"`)
		second := mustReadTransportRequest(reader)
		if string(first.ID) == string(second.ID) {
			os.Exit(3)
		}
		writeTransportResponse(second.ID, `"second"`)
	case "cancel":
		if err := os.WriteFile(value, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(4)
		}
		cancel := mustReadTransportRequest(reader)
		var params struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(cancel.Params, &params) != nil || string(params.ID) != string(first.ID) {
			os.Exit(5)
		}
		writeTransportResponse(first.ID, `"finished"`)
	default:
		os.Exit(6)
	}
	os.Exit(0)
}

type transportTestRequest struct {
	ID     json.RawMessage `json:"id"`
	Params json.RawMessage `json:"params"`
}

func mustReadTransportRequest(reader *bufio.Reader) transportTestRequest {
	payload, err := readLSPManagerTestFrame(reader)
	if err != nil {
		os.Exit(7)
	}
	var request transportTestRequest
	if json.Unmarshal(payload, &request) != nil {
		os.Exit(8)
	}
	return request
}

func writeTransportResponse(id json.RawMessage, result string) {
	payload := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, id, result)
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
}

func lspTransportTestConfig(mode, value string) *config.Config {
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{
		os.Args[0], "-test.run=^TestLSPTransportBoundaryHelperProcess$", "--", mode, value,
	}}
	return cfg
}
