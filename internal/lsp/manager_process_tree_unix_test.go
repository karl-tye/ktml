//go:build unix

package lsp

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
	"golang.org/x/sys/unix"
)

func TestManager_GIVENExternalServer_WHENStarted_THENOwnsDedicatedProcessGroup(t *testing.T) {
	t.Setenv("KTML_LSP_TREE_HELPER", "1")
	manager := GetManager(lspProcessTreeTestConfig("group-reply", t.TempDir()))
	t.Cleanup(func() { _ = manager.Close() })

	response, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if err != nil {
		t.Fatalf("ProxyRequest() error = %v", err)
	}
	if string(response) != `{"jsonrpc":"2.0","id":1,"result":true}` {
		t.Fatalf("response = %s, want dedicated process group", response)
	}
}

func TestManager_GIVENExternalServerSpawnsChild_WHENClosed_THENTerminatesCompleteProcessTree(t *testing.T) {
	t.Setenv("KTML_LSP_TREE_HELPER", "1")
	dir := t.TempDir()
	manager := GetManager(lspProcessTreeTestConfig("tree-silent", dir))
	parentPID := 0
	childPID := 0
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600)
		_ = manager.Close()
		if parentPID != 0 {
			waitForProcessExit(t, parentPID)
		}
		if childPID != 0 {
			waitForProcessExit(t, childPID)
		}
	})
	result := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
		result <- err
	}()
	parentPID = waitForHelperPID(t, filepath.Join(dir, "parent-pid"))
	childPID = waitForHelperPID(t, filepath.Join(dir, "child-pid"))

	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("pending request succeeded after manager Close")
		}
	case <-time.After(time.Second):
		t.Fatal("pending request remained blocked after manager Close")
	}
	waitForProcessExit(t, parentPID)
	waitForProcessExit(t, childPID)
}

func TestManager_GIVENExternalServerTreeNeverReplies_WHENProxyDeadlineExpires_THENTerminatesCompleteProcessTree(t *testing.T) {
	t.Setenv("KTML_LSP_TREE_HELPER", "1")
	dir := t.TempDir()
	manager := GetManager(lspProcessTreeTestConfig("tree-silent", dir))
	manager.requestTimeout = 50 * time.Millisecond
	parentPID := 0
	childPID := 0
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600)
		_ = manager.Close()
		if parentPID != 0 {
			waitForProcessExit(t, parentPID)
		}
		if childPID != 0 {
			waitForProcessExit(t, childPID)
		}
	})
	result := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
		result <- err
	}()
	parentPID = waitForHelperPID(t, filepath.Join(dir, "parent-pid"))
	childPID = waitForHelperPID(t, filepath.Join(dir, "child-pid"))

	select {
	case err := <-result:
		if err != context.DeadlineExceeded {
			t.Fatalf("ProxyRequest() error = %v, want proxy deadline", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ProxyRequest() ignored proxy deadline")
	}
	waitForProcessExit(t, parentPID)
	waitForProcessExit(t, childPID)
}

func TestGetManager_GIVENReplacementOfExternalServerTree_THENTerminatesCompleteObsoleteTree(t *testing.T) {
	t.Setenv("KTML_LSP_TREE_HELPER", "1")
	dir := t.TempDir()
	manager := GetManager(lspProcessTreeTestConfig("tree-silent", dir))
	parentPID := 0
	childPID := 0
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600)
		_ = manager.Close()
		if parentPID != 0 {
			waitForProcessExit(t, parentPID)
		}
		if childPID != 0 {
			waitForProcessExit(t, childPID)
		}
	})
	result := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
		result <- err
	}()
	parentPID = waitForHelperPID(t, filepath.Join(dir, "parent-pid"))
	childPID = waitForHelperPID(t, filepath.Join(dir, "child-pid"))

	replacement := GetManager(lspProcessTreeTestConfig("group-reply", t.TempDir()))
	t.Cleanup(func() { _ = replacement.Close() })
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("pending request succeeded after manager replacement")
		}
	case <-time.After(time.Second):
		t.Fatal("pending request remained blocked after manager replacement")
	}
	waitForProcessExit(t, parentPID)
	waitForProcessExit(t, childPID)
}

func TestManager_GIVENExternalServerWrapperEOF_WHENChildRemains_THENTerminatesCompleteProcessTree(t *testing.T) {
	t.Setenv("KTML_LSP_TREE_HELPER", "1")
	dir := t.TempDir()
	manager := GetManager(lspProcessTreeTestConfig("tree-eof", dir))
	parentPID := 0
	childPID := 0
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600)
		_ = manager.Close()
		if parentPID != 0 {
			waitForProcessExit(t, parentPID)
		}
		if childPID != 0 {
			waitForProcessExit(t, childPID)
		}
	})
	result := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
		result <- err
	}()
	parentPID = waitForHelperPID(t, filepath.Join(dir, "parent-pid"))
	childPID = waitForHelperPID(t, filepath.Join(dir, "child-pid"))

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("ProxyRequest() succeeded after wrapper EOF")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending request remained blocked after wrapper EOF")
	}
	waitForProcessExit(t, parentPID)
	waitForProcessExit(t, childPID)
}

func TestLSPProcessTreeHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_TREE_HELPER") != "1" {
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
	mode := os.Args[separator+1]
	value := os.Args[separator+2]
	if mode == "child" {
		if err := publishHelperPID(value); err != nil {
			os.Exit(3)
		}
		waitForHelperFile(filepath.Join(filepath.Dir(value), "stop-child"))
		return
	}
	payload, err := readLSPManagerTestFrame(bufio.NewReader(os.Stdin))
	if err != nil {
		os.Exit(4)
	}
	switch mode {
	case "group-reply":
		sessionID, _ := unix.Getsid(0)
		result := syscall.Getpgrp() == os.Getpid() && sessionID == os.Getpid()
		if result {
			writeLSPManagerTestResponse(payload, `"result":true`)
		} else {
			writeLSPManagerTestResponse(payload, `"result":false`)
		}
	case "tree-silent", "tree-eof":
		if syscall.Getpgrp() != os.Getpid() {
			os.Exit(5)
		}
		if err := publishHelperPID(filepath.Join(value, "parent-pid")); err != nil {
			os.Exit(6)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestLSPProcessTreeHelperProcess$", "--", "child", filepath.Join(value, "child-pid"))
		child.Env = os.Environ()
		if err := child.Start(); err != nil {
			os.Exit(7)
		}
		_ = child.Process.Release()
		if mode == "tree-eof" {
			childPIDPath := filepath.Join(value, "child-pid")
			for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
				if _, err := os.Stat(childPIDPath); err == nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
			os.Exit(0)
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
	default:
		os.Exit(8)
	}
	os.Exit(0)
}

func lspProcessTreeTestConfig(mode, value string) *config.Config {
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{
		os.Args[0], "-test.run=^TestLSPProcessTreeHelperProcess$", "--", mode, value,
	}}
	return cfg
}
