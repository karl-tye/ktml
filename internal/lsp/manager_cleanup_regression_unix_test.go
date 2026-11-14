//go:build unix

package lsp

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

func TestManager_GIVENAliveParentAndGroupSignalEPERM_WHENClosed_THENCompleteCleanupIsDeadlineBounded(t *testing.T) {
	t.Setenv("KTML_LSP_STUBBORN_TREE_HELPER", "1")
	dir := t.TempDir()
	manager := GetManager(lspStubbornTreeTestConfig("parent", dir))
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
	assertProcessGroupMember(t, childPID, parentPID)
	if !processExists(parentPID) || !processExists(childPID) {
		t.Fatal("helper process tree was not alive before cleanup")
	}

	processTreeCleanupTimeout = 50 * time.Millisecond
	signalProcessGroup = func(int, syscall.Signal) error { return syscall.EPERM }
	started := time.Now()
	closeResult := make(chan error, 1)
	go func() { closeResult <- manager.Close() }()

	var err error
	select {
	case err = <-closeResult:
	case <-time.After(500 * time.Millisecond):
		signalProcessGroup = originalSignal
		_ = os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600)
		err = <-closeResult
		t.Fatalf("Close() remained blocked after complete cleanup deadline: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "process group") || !strings.Contains(err.Error(), "remained alive") {
		t.Fatalf("Close() error = %v, want live process-group cleanup error", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Close() took %v after cleanup deadline", elapsed)
	}
	assertProcessGone(t, parentPID)
	if !processExists(childPID) {
		t.Fatal("simulated inaccessible process group did not remain alive")
	}
	globalManager.Lock()
	current := globalManager.current
	globalManager.Unlock()
	if current != manager.core {
		t.Fatal("Close() removed the cleanup-failed manager from the replacement barrier")
	}
	select {
	case err := <-requestErr:
		if err == nil {
			t.Fatal("pending request succeeded after bounded cleanup")
		}
	case <-time.After(time.Second):
		t.Fatal("pending request remained blocked after cleanup deadline")
	}

	signalProcessGroup = originalSignal
	if err := os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600); err != nil {
		t.Fatalf("stop helper child: %v", err)
	}
	waitForProcessExit(t, childPID)
	parentPID = 0
}

func TestGetManager_GIVENObsoleteProcessGroupSurvivesCleanup_WHENReplacing_THENKeepsObsoleteAndReturnsActionableError(t *testing.T) {
	t.Setenv("KTML_LSP_STUBBORN_TREE_HELPER", "1")
	obsoleteDir := t.TempDir()
	manager := GetManager(lspStubbornTreeTestConfig("parent", obsoleteDir))
	parentPID := 0
	childPID := 0
	var replacement *Manager
	originalSignal := signalProcessGroup
	originalTimeout := processTreeCleanupTimeout
	defer func() {
		signalProcessGroup = originalSignal
		processTreeCleanupTimeout = originalTimeout
		if replacement != nil {
			_ = replacement.Close()
		}
		_ = os.WriteFile(filepath.Join(obsoleteDir, "stop-child"), nil, 0o600)
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
	parentPID = waitForAtomicHelperPID(t, filepath.Join(obsoleteDir, "parent-pid"))
	childPID = waitForAtomicHelperPID(t, filepath.Join(obsoleteDir, "child-pid"))
	assertProcessGroupMember(t, childPID, parentPID)

	replacementDir := t.TempDir()
	replacementConfig := lspStubbornTreeTestConfig("replacement", replacementDir)
	processTreeCleanupTimeout = 50 * time.Millisecond
	signalProcessGroup = func(int, syscall.Signal) error { return syscall.EPERM }
	replacement = GetManager(replacementConfig)

	globalManager.Lock()
	current := globalManager.current
	globalManager.Unlock()
	if current != manager.core {
		t.Fatal("GetManager() discarded the last manager after obsolete process-tree cleanup failed")
	}
	response, err := replacement.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":2,"method":"test"}`))
	if err == nil || !strings.Contains(err.Error(), "replace LSP manager") || !strings.Contains(err.Error(), "remained alive") {
		t.Fatalf("replacement ProxyRequest() response = %s, error = %v, want actionable obsolete cleanup failure", response, err)
	}
	if _, err := os.Stat(filepath.Join(replacementDir, "replacement-pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement process launched while obsolete group remained alive: %v", err)
	}
	if !processExists(childPID) {
		t.Fatal("obsolete process group was not alive when replacement was rejected")
	}
	select {
	case err := <-requestErr:
		if err == nil {
			t.Fatal("obsolete pending request succeeded during failed replacement")
		}
	case <-time.After(time.Second):
		t.Fatal("obsolete pending request remained blocked after cleanup deadline")
	}

	signalProcessGroup = originalSignal
	processTreeCleanupTimeout = originalTimeout
	if err := os.WriteFile(filepath.Join(obsoleteDir, "stop-child"), nil, 0o600); err != nil {
		t.Fatalf("stop obsolete helper child: %v", err)
	}
	waitForProcessExit(t, childPID)
	recovered := GetManager(replacementConfig)
	t.Cleanup(func() { _ = recovered.Close() })
	response, err = recovered.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":3,"method":"test"}`))
	if err != nil {
		t.Fatalf("replacement after obsolete cleanup recovery error = %v", err)
	}
	if string(response) != `{"jsonrpc":"2.0","id":3,"result":"replacement"}` {
		t.Fatalf("replacement response = %s", response)
	}
	assertProcessGone(t, childPID)
	parentPID = 0
}

func TestLSPStubbornTreeHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_STUBBORN_TREE_HELPER") != "1" {
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
	if mode == "replacement" {
		payload, err := readLSPManagerTestFrame(bufio.NewReader(os.Stdin))
		if err != nil {
			os.Exit(4)
		}
		if err := publishHelperPID(filepath.Join(value, "replacement-pid")); err != nil {
			os.Exit(5)
		}
		writeLSPManagerTestResponse(payload, `"result":"replacement"`)
		for {
			time.Sleep(time.Hour)
		}
	}
	if mode != "parent" {
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
	child := exec.Command(os.Args[0], "-test.run=^TestLSPStubbornTreeHelperProcess$", "--", "child", filepath.Join(value, "child-pid"))
	child.Env = os.Environ()
	if err := child.Start(); err != nil {
		os.Exit(8)
	}
	_ = child.Process.Release()
	for {
		time.Sleep(time.Hour)
	}
}

func lspStubbornTreeTestConfig(mode, dir string) *config.Config {
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{
		os.Args[0], "-test.run=^TestLSPStubbornTreeHelperProcess$", "--", mode, dir,
	}}
	return cfg
}
