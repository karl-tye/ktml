//go:build unix

package lsp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

func TestManager_GIVENConcurrentServerEOF_WHENClosed_THENWaitsForCompleteCleanup(t *testing.T) {
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
	assertProcessGroupMember(t, childPID, parentPID)

	manager.core.mu.Lock()
	server := manager.core.servers["go"]
	manager.core.mu.Unlock()
	if server == nil {
		t.Fatal("external server was removed before the EOF race")
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

	closeResult := make(chan error, 1)
	go func() { closeResult <- manager.Close() }()
	select {
	case err := <-closeResult:
		parentAlive := processExists(parentPID)
		childAlive := processExists(childPID)
		server.mu.Unlock()
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if parentAlive || childAlive {
			t.Fatalf("Close() returned during EOF cleanup with process tree alive: parent=%t child=%t", parentAlive, childAlive)
		}
	case <-time.After(50 * time.Millisecond):
		server.mu.Unlock()
		if err := <-closeResult; err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}

	select {
	case err := <-requestErr:
		if err == nil {
			t.Fatal("pending request succeeded during server EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("Close() returned before the pending request was failed")
	}
	assertProcessGone(t, parentPID)
	assertProcessGroupGone(t, parentPID)
}

func TestGetManager_GIVENConcurrentObsoleteServerEOF_WHENConfigurationChanges_THENWaitsForCompleteCleanup(t *testing.T) {
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
	assertProcessGroupMember(t, childPID, parentPID)

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

	replacementResult := make(chan *Manager, 1)
	go func() { replacementResult <- GetManager(config.Default()) }()
	var replacement *Manager
	select {
	case replacement = <-replacementResult:
		parentAlive := processExists(parentPID)
		childAlive := processExists(childPID)
		server.mu.Unlock()
		_ = replacement.Close()
		if parentAlive || childAlive {
			t.Fatalf("GetManager() returned during obsolete EOF cleanup with process tree alive: parent=%t child=%t", parentAlive, childAlive)
		}
	case <-time.After(50 * time.Millisecond):
		server.mu.Unlock()
		replacement = <-replacementResult
	}
	t.Cleanup(func() { _ = replacement.Close() })

	select {
	case err := <-requestErr:
		if err == nil {
			t.Fatal("pending request succeeded during obsolete server EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("GetManager() returned before the obsolete pending request was failed")
	}
	assertProcessGone(t, parentPID)
	assertProcessGroupGone(t, parentPID)
}

func TestLSPCleanupCompletionHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_CLEANUP_HELPER") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator == 0 || len(os.Args) < separator+3 {
		os.Exit(2)
	}
	mode, value := os.Args[separator+1], os.Args[separator+2]
	if mode == "child" {
		if err := publishHelperPID(value); err != nil {
			os.Exit(3)
		}
		waitForHelperFile(filepath.Join(filepath.Dir(value), "stop-child"))
		return
	}
	if mode != "controlled-eof" {
		os.Exit(4)
	}
	if _, err := readLSPManagerTestFrame(bufio.NewReader(os.Stdin)); err != nil {
		os.Exit(5)
	}
	if syscall.Getpgrp() != os.Getpid() {
		os.Exit(6)
	}
	if err := publishHelperPID(filepath.Join(value, "parent-pid")); err != nil {
		os.Exit(7)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestLSPCleanupCompletionHelperProcess$", "--", "child", filepath.Join(value, "child-pid"))
	child.Env = os.Environ()
	if err := child.Start(); err != nil {
		os.Exit(8)
	}
	_ = child.Process.Release()
	waitForHelperFile(filepath.Join(value, "child-pid"))
	waitForHelperFile(filepath.Join(value, "release"))
	_ = os.Stdout.Close()
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func lspCleanupCompletionTestConfig(dir string) *config.Config {
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{
		os.Args[0], "-test.run=^TestLSPCleanupCompletionHelperProcess$", "--", "controlled-eof", dir,
	}}
	return cfg
}

func publishHelperPID(path string) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".pid-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func waitForAtomicHelperPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(string(contents))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("helper did not atomically report its PID at %s", path)
	return 0
}

func waitForHelperFile(path string) {
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForServerRemoval(core *managerCore, target string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		core.mu.Lock()
		_, exists := core.servers[target]
		core.mu.Unlock()
		if !exists {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("helper process %d exists after cleanup returned: %v", pid, err)
	}
}

func assertProcessGroupMember(t *testing.T, pid, groupID int) {
	t.Helper()
	actualGroupID, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("get helper process %d group: %v", pid, err)
	}
	if actualGroupID != groupID {
		t.Fatalf("helper process %d group = %d, want %d", pid, actualGroupID, groupID)
	}
}

func assertProcessGroupGone(t *testing.T, groupID int) {
	t.Helper()
	if err := syscall.Kill(-groupID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("helper process group %d exists after cleanup returned: %v", groupID, err)
	}
}
