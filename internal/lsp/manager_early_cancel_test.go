package lsp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManager_GIVENCallerCancelsBeforeRegistration_THENOnlyThatRequestFailsAndBackendKeepsServing(t *testing.T) {
	t.Setenv("KTML_LSP_NAMESPACE_HELPER", "1")
	manager := GetManager(lspNamespaceTestConfig("echo", ""))
	t.Cleanup(func() { _ = manager.Close() })
	if response, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"warm"}`)); err != nil || string(response) != `{"jsonrpc":"2.0","id":1,"result":"warm"}` {
		t.Fatalf("warm-up response = %s, %v", response, err)
	}
	manager.core.mu.Lock()
	server := manager.core.servers["go"]
	manager.core.mu.Unlock()

	// Holding writeMu keeps the request from registering, so cancellation is
	// guaranteed to arrive first.
	server.writeMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(ctx, "go", []byte(`{"jsonrpc":"2.0","id":2,"method":"early"}`))
		cancelled <- err
	}()
	var err error
	select {
	case err = <-cancelled:
		server.writeMu.Unlock()
	case <-time.After(time.Second):
		server.writeMu.Unlock()
		<-cancelled
		t.Fatal("early-cancelled request waited behind the shared backend writer")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ProxyRequest() error = %v, want context.Canceled", err)
	}

	server.mu.Lock()
	accepting := server.accepting
	server.mu.Unlock()
	manager.core.mu.Lock()
	current := manager.core.servers["go"]
	manager.core.mu.Unlock()
	if !accepting || current != server {
		t.Fatalf("shared backend was retired by an early caller cancellation (accepting=%v, same=%v)", accepting, current == server)
	}
	if response, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":3,"method":"after"}`)); err != nil || string(response) != `{"jsonrpc":"2.0","id":3,"result":"after"}` {
		t.Fatalf("follow-up response = %s, %v", response, err)
	}
}
