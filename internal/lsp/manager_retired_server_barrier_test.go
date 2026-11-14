package lsp

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestManager_GIVENServerRetiredBeforeCleanupStarts_WHENRequestedAgain_THENWaitsForCleanupInsteadOfFailingDeadline(t *testing.T) {
	core := newManagerCore(nil)
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer stdoutWriter.Close()
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer stdinReader.Close()
	// A terminal timeout has made the server non-accepting, but its fail call
	// has not yet started cleanup, so stopBy is still unset.
	server := &ExternalServer{
		manager:        core,
		target:         "go",
		stdin:          stdinWriter,
		stdout:         stdoutReader,
		requests:       make(map[string]*pendingRequest),
		clientRequests: make(map[string]string),
		accepting:      false,
		stopDone:       make(chan struct{}),
		stopWait:       time.Second,
	}
	core.servers["go"] = server
	manager := newManagerLease(core)

	result := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("ProxyRequest() returned before retired server cleanup started: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := server.fail(errors.New("request timed out")); err != nil {
		t.Fatalf("fail() error = %v", err)
	}
	select {
	case err := <-result:
		if err == nil || strings.Contains(err.Error(), "exceeded its deadline") || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("ProxyRequest() error = %v, want relaunch attempt after cleanup", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ProxyRequest() remained blocked after retired server cleanup completed")
	}
}
