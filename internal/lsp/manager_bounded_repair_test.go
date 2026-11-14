package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

func TestExternalServer_GIVENResponseOwnsRequestDuringWriteTimeout_THENReturnsResponseWithoutFailingServer(t *testing.T) {
	writer := &responseOwningWriter{release: make(chan struct{})}
	server := &ExternalServer{
		manager:         &managerCore{servers: make(map[string]*ExternalServer), stopping: make(map[*ExternalServer]error)},
		target:          "test",
		stdin:           writer,
		stdout:          io.NopCloser(bytes.NewReader(nil)),
		requests:        make(map[string]*pendingRequest),
		clientRequests:  make(map[string]string),
		transportPrefix: "ktml-test",
		accepting:       true,
		stopDone:        make(chan struct{}),
		stopWait:        time.Second,
	}
	server.manager.servers[server.target] = server
	writer.server = server

	response, err := server.sendRequest(context.Background(), "", classifyRPCMessage([]byte(`{"jsonrpc":"2.0","id":"same","method":"work"}`)), 20*time.Millisecond)
	if err != nil || string(response) != `{"jsonrpc":"2.0","id":"same","result":"owned"}` {
		writer.unblockWrite()
		t.Fatalf("first sendRequest() = %s, %v", response, err)
	}
	writer.unblockWrite()

	response, err = server.sendRequest(context.Background(), "", classifyRPCMessage([]byte(`{"jsonrpc":"2.0","id":"same","method":"newer"}`)), time.Second)
	if err != nil || string(response) != `{"jsonrpc":"2.0","id":"same","result":"owned"}` {
		t.Fatalf("newer sendRequest() = %s, %v", response, err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if !server.accepting || server.requests == nil {
		t.Fatal("response-owned write timeout failed the server")
	}
}

type responseOwningWriter struct {
	server  *ExternalServer
	release chan struct{}
	block   sync.Once
	unblock sync.Once
}

func (w *responseOwningWriter) Write(payload []byte) (int, error) {
	if bytes.HasPrefix(payload, []byte("Content-Length:")) {
		return len(payload), nil
	}
	request := classifyRPCMessage(payload)
	response := classifyRPCMessage([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"owned"}`, request.id)))
	w.server.deliverResponse(response)
	w.block.Do(func() { <-w.release })
	return len(payload), nil
}

func (w *responseOwningWriter) Close() error {
	w.unblockWrite()
	return nil
}

func (w *responseOwningWriter) unblockWrite() {
	w.unblock.Do(func() { close(w.release) })
}

func TestExternalServer_GIVENTransportIDCollisionNearExhaustion_THENDoesNotReuseID(t *testing.T) {
	maximum := ^uint64(0)
	collidingID := "ktml-test-" + strconv.FormatUint(maximum, 10)
	server := &ExternalServer{
		target:          "test",
		stdin:           nopWriteCloser{Writer: io.Discard},
		requests:        map[string]*pendingRequest{"s:" + collidingID: {}},
		clientRequests:  make(map[string]string),
		transportPrefix: "ktml-test",
		nextTransportID: maximum - 1,
		accepting:       true,
	}
	_, err := server.sendRequest(context.Background(), "", classifyRPCMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"work"}`)), time.Second)
	if err == nil || !strings.Contains(err.Error(), "ID space exhausted") {
		t.Fatalf("sendRequest() error = %v, want bounded exhaustion", err)
	}
	if len(server.requests) != 1 {
		t.Fatalf("requests = %d, collision entry was overwritten", len(server.requests))
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestManager_GIVENConcurrentRequestAndCancellation_THENBackendReceivesRequestFirst(t *testing.T) {
	t.Setenv("KTML_LSP_REPAIR_HELPER", "1")
	manager := GetManager(lspRepairTestConfig("ordering"))
	t.Cleanup(func() { _ = manager.Close() })
	result := make(chan error, 1)
	go func() {
		response, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":"client","method":"work"}`))
		if err == nil && string(response) != `{"jsonrpc":"2.0","id":"client","result":"finished"}` {
			err = fmt.Errorf("response = %s", response)
		}
		result <- err
	}()
	waitForActiveClientRequest(t, manager, "go", "s:client")
	if _, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":"client"}}`)); err != nil {
		t.Fatalf("cancellation error = %v", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("request error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not complete")
	}
}

func TestManager_GIVENRequests_THENBackendReceivesUniqueStringTransportIDs(t *testing.T) {
	t.Setenv("KTML_LSP_REPAIR_HELPER", "1")
	manager := GetManager(lspRepairTestConfig("string-ids"))
	t.Cleanup(func() { _ = manager.Close() })
	for _, id := range []string{`90071992547409930000000000000000000000`, `"\u0063lient"`} {
		payload := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"work"}`, id))
		response, err := manager.ProxyRequest(context.Background(), "go", payload)
		if err != nil {
			t.Fatalf("ProxyRequest(%s) error = %v", id, err)
		}
		message := classifyRPCMessage(response)
		if message.err != nil || string(message.id) != id {
			t.Fatalf("response = %s, want exact ID %s: %v", response, id, message.err)
		}
	}
}

func TestManager_GIVENMalformedBackendJSON_THENSendsParseErrorAndContinues(t *testing.T) {
	t.Setenv("KTML_LSP_REPAIR_HELPER", "1")
	manager := GetManager(lspRepairTestConfig("malformed"))
	t.Cleanup(func() { _ = manager.Close() })
	response, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":"client","method":"work"}`))
	if err != nil || string(response) != `{"jsonrpc":"2.0","id":"client","result":"recovered"}` {
		t.Fatalf("ProxyRequest() = %s, %v", response, err)
	}
}

func TestLSPBoundedRepairHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_REPAIR_HELPER") != "1" {
		return
	}
	separator := 0
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator == 0 || len(os.Args) <= separator+1 {
		os.Exit(2)
	}
	reader := bufio.NewReader(os.Stdin)
	switch os.Args[separator+1] {
	case "ordering":
		request := mustReadRepairRequest(reader)
		if request.Method != "work" {
			os.Exit(3)
		}
		cancellation := mustReadRepairRequest(reader)
		if cancellation.Method != "$/cancelRequest" {
			os.Exit(4)
		}
		var params struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(cancellation.Params, &params) != nil || string(params.ID) != string(request.ID) {
			os.Exit(5)
		}
		writeRepairResponse(request.ID, `"finished"`)
	case "string-ids":
		var previous string
		for range 2 {
			request := mustReadRepairRequest(reader)
			var id string
			if json.Unmarshal(request.ID, &id) != nil || !strings.HasPrefix(id, "ktml-") || id == previous {
				os.Exit(6)
			}
			previous = id
			writeRepairResponse(request.ID, `null`)
		}
	case "malformed":
		request := mustReadRepairRequest(reader)
		writeRepairFrame([]byte(`{"jsonrpc":`))
		parseError, err := readLSPManagerTestFrame(reader)
		if err != nil {
			os.Exit(7)
		}
		message := classifyRPCMessage(parseError)
		if message.err != nil || message.kind != rpcResponse || string(message.id) != "null" {
			os.Exit(8)
		}
		var envelope struct {
			Error struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(parseError, &envelope) != nil || envelope.Error.Code != -32700 {
			os.Exit(9)
		}
		writeRepairResponse(request.ID, `"recovered"`)
	default:
		os.Exit(10)
	}
	os.Exit(0)
}

type repairRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func mustReadRepairRequest(reader *bufio.Reader) repairRequest {
	payload, err := readLSPManagerTestFrame(reader)
	if err != nil {
		os.Exit(11)
	}
	var request repairRequest
	if json.Unmarshal(payload, &request) != nil {
		os.Exit(12)
	}
	return request
}

func writeRepairResponse(id json.RawMessage, result string) {
	writeRepairFrame([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, id, result)))
}

func writeRepairFrame(payload []byte) {
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n", len(payload))
	_, _ = os.Stdout.Write(payload)
}

func lspRepairTestConfig(mode string) *config.Config {
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{os.Args[0], "-test.run=^TestLSPBoundedRepairHelperProcess$", "--", mode}}
	return cfg
}

func waitForActiveClientRequest(t *testing.T, manager *Manager, target, clientKey string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		manager.core.mu.Lock()
		server := manager.core.servers[target]
		manager.core.mu.Unlock()
		if server != nil {
			server.mu.Lock()
			_, active := server.clientRequests[manager.namespace+clientKey]
			server.mu.Unlock()
			if active {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("request did not become active")
}
