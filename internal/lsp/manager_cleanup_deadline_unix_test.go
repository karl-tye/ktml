//go:build unix

package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestManager_GIVENProcessGroupRemainsAlive_WHENClosed_THENReturnsCleanupErrorWithoutDeadlock(t *testing.T) {
	t.Setenv("KTML_LSP_TREE_HELPER", "1")
	dir := t.TempDir()
	manager := GetManager(lspProcessTreeTestConfig("tree-silent", dir))
	parentPID := 0
	childPID := 0
	originalSignal := signalProcessGroup
	originalTimeout := processTreeCleanupTimeout
	defer func() {
		signalProcessGroup = originalSignal
		processTreeCleanupTimeout = originalTimeout
		_ = os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600)
		_ = manager.Close()
		if parentPID != 0 {
			waitForProcessExit(t, parentPID)
		}
		if childPID != 0 {
			waitForProcessExit(t, childPID)
		}
	}()

	requestErr := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
		requestErr <- err
	}()
	parentPID = waitForAtomicHelperPID(t, filepath.Join(dir, "parent-pid"))
	childPID = waitForAtomicHelperPID(t, filepath.Join(dir, "child-pid"))

	processTreeCleanupTimeout = 50 * time.Millisecond
	signalProcessGroup = func(groupID int, signal syscall.Signal) error {
		if signal == 0 {
			return nil
		}
		if err := syscall.Kill(groupID, signal); errors.Is(err, syscall.ESRCH) {
			return nil
		} else {
			return err
		}
	}

	started := time.Now()
	err := manager.Close()
	if err == nil || !strings.Contains(err.Error(), "process group") || !strings.Contains(err.Error(), "remained alive") {
		t.Fatalf("Close() error = %v, want live process-group cleanup error", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Close() took %v after cleanup deadline", elapsed)
	}
	select {
	case err := <-requestErr:
		if err == nil {
			t.Fatal("pending request succeeded after bounded cleanup")
		}
	case <-time.After(time.Second):
		t.Fatal("pending request remained blocked after cleanup deadline")
	}
	if !processExists(childPID) {
		t.Fatal("cleanup reported failure without the simulated descendant remaining alive")
	}
	select {
	case <-manager.core.cleanupDone:
	default:
		t.Fatal("manager cleanup completion channel remained open after cleanup deadline")
	}

	signalProcessGroup = originalSignal
	processTreeCleanupTimeout = originalTimeout
	if err := os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600); err != nil {
		t.Fatalf("stop helper child: %v", err)
	}
	waitForProcessExit(t, childPID)
}
