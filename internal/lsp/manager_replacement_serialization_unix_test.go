//go:build unix

package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetManager_GIVENReplacementCleanupInProgress_WHENConcurrentCallersRequestReplacement_THENNeitherReturnsEarly(t *testing.T) {
	t.Setenv("KTML_LSP_CLEANUP_HELPER", "1")
	dir := t.TempDir()
	manager := GetManager(lspCleanupCompletionTestConfig(dir))
	parentPID := 0
	childPID := 0
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600)
		_ = os.WriteFile(filepath.Join(dir, "release"), nil, 0o600)
		_ = manager.Close()
		if parentPID != 0 {
			waitForProcessExit(t, parentPID)
		}
		if childPID != 0 {
			waitForProcessExit(t, childPID)
		}
	})

	requestErr := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
		requestErr <- err
	}()
	parentPID = waitForAtomicHelperPID(t, filepath.Join(dir, "parent-pid"))
	childPID = waitForAtomicHelperPID(t, filepath.Join(dir, "child-pid"))

	manager.core.mu.Lock()
	server := manager.core.servers["go"]
	manager.core.mu.Unlock()
	if server == nil {
		t.Fatal("external server was removed before the replacement race")
	}
	server.mu.Lock()
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o600); err != nil {
		server.mu.Unlock()
		t.Fatalf("release helper EOF: %v", err)
	}
	if !waitForServerRemoval(manager.core, "go") {
		server.mu.Unlock()
		t.Fatal("server go was not removed after EOF")
	}

	replacementConfig := lspProcessTreeTestConfig("group-reply", t.TempDir())
	results := make(chan *Manager, 2)
	go func() { results <- GetManager(replacementConfig) }()
	time.Sleep(10 * time.Millisecond)
	go func() { results <- GetManager(replacementConfig) }()

	select {
	case replacement := <-results:
		server.mu.Unlock()
		_ = replacement.Close()
		t.Fatal("GetManager() returned replacement while obsolete cleanup was blocked")
	case <-time.After(50 * time.Millisecond):
	}
	server.mu.Unlock()

	first := <-results
	second := <-results
	t.Cleanup(func() {
		_ = first.Close()
		_ = second.Close()
	})
	if first.core != second.core {
		t.Fatal("concurrent callers received different replacement cores")
	}
	select {
	case err := <-requestErr:
		if err == nil {
			t.Fatal("obsolete pending request succeeded during replacement")
		}
	case <-time.After(time.Second):
		t.Fatal("replacement returned before obsolete pending request failed")
	}
	assertProcessGone(t, parentPID)
	assertProcessGone(t, childPID)
	assertProcessGroupGone(t, parentPID)
}
