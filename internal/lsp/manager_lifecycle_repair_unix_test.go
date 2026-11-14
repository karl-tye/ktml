//go:build unix

package lsp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

func TestManager_GIVENSameTargetCleanupInProgress_WHENRequestedAgain_THENDoesNotOverlapProcesses(t *testing.T) {
	isolateGlobalLSPManager(t)
	t.Setenv("KTML_LSP_REPLACEMENT_EXCLUSION_HELPER", "1")
	dir := t.TempDir()
	manager := GetManager(lspReplacementExclusionTestConfig(dir))
	t.Cleanup(func() { _ = manager.Close() })

	originalSignal := signalProcessGroup
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	var oldPID atomic.Int64
	var startedOnce sync.Once
	var releaseOnce sync.Once
	signalProcessGroup = func(groupID int, signal syscall.Signal) error {
		if int64(groupID) == oldPID.Load() && signal == syscall.SIGKILL {
			startedOnce.Do(func() { close(cleanupStarted) })
			<-releaseCleanup
		}
		return originalSignal(groupID, signal)
	}
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseCleanup) })
		signalProcessGroup = originalSignal
	})

	firstRequest := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
		firstRequest <- err
	}()
	oldPID.Store(int64(waitForAtomicHelperPID(t, filepath.Join(dir, "old-pid"))))
	oldChildPID := waitForAtomicHelperPID(t, filepath.Join(dir, "old-child-pid"))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseCleanup) })
		signalProcessGroup = originalSignal
		_ = os.WriteFile(filepath.Join(dir, "stop-old-child"), nil, 0o600)
		_ = manager.Close()
		waitForProcessExit(t, int(oldPID.Load()))
		waitForProcessExit(t, oldChildPID)
		if contents, err := os.ReadFile(filepath.Join(dir, "new-pid")); err == nil {
			if newPID, parseErr := strconv.Atoi(string(contents)); parseErr == nil {
				waitForProcessExit(t, newPID)
			}
		}
	})

	if err := os.WriteFile(filepath.Join(dir, "release-old"), nil, 0o600); err != nil {
		t.Fatalf("release old helper stdout: %v", err)
	}
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("old server cleanup did not start")
	}

	secondRequest := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":2,"method":"test"}`))
		secondRequest <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_, overlapErr := os.Stat(filepath.Join(dir, "new-pid"))

	releaseOnce.Do(func() { close(releaseCleanup) })
	select {
	case err := <-firstRequest:
		if err == nil {
			t.Fatal("old request succeeded after server EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("old request remained blocked after cleanup completed")
	}
	select {
	case err := <-secondRequest:
		if err != nil {
			t.Fatalf("replacement request error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement request did not complete after old cleanup")
	}
	if overlapErr == nil {
		t.Fatal("replacement process started while obsolete same-target cleanup was incomplete")
	}
	if !errors.Is(overlapErr, os.ErrNotExist) {
		t.Fatalf("stat replacement process marker: %v", overlapErr)
	}
}

func TestManager_GIVENSameTargetCleanupFailed_WHENGroupLaterExits_THENRetriesBeforeRelaunch(t *testing.T) {
	isolateGlobalLSPManager(t)
	t.Setenv("KTML_LSP_REPLACEMENT_EXCLUSION_HELPER", "1")
	originalSignal := signalProcessGroup
	originalTimeout := processTreeCleanupTimeout
	processTreeCleanupTimeout = 25 * time.Millisecond
	signalProcessGroup = func(int, syscall.Signal) error { return syscall.EPERM }
	t.Cleanup(func() {
		signalProcessGroup = originalSignal
		processTreeCleanupTimeout = originalTimeout
	})

	dir := t.TempDir()
	manager := GetManager(lspReplacementExclusionTestConfig(dir))
	t.Cleanup(func() { _ = manager.Close() })
	firstRequest := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
		firstRequest <- err
	}()
	parentPID := waitForAtomicHelperPID(t, filepath.Join(dir, "old-pid"))
	childPID := waitForAtomicHelperPID(t, filepath.Join(dir, "old-child-pid"))
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "stop-old-child"), nil, 0o600)
		_ = manager.Close()
		waitForProcessExit(t, parentPID)
		waitForProcessExit(t, childPID)
		if contents, err := os.ReadFile(filepath.Join(dir, "new-pid")); err == nil {
			if newPID, parseErr := strconv.Atoi(string(contents)); parseErr == nil {
				waitForProcessExit(t, newPID)
			}
		}
	})
	if err := os.WriteFile(filepath.Join(dir, "release-old"), nil, 0o600); err != nil {
		t.Fatalf("release old helper stdout: %v", err)
	}
	select {
	case err := <-firstRequest:
		if err == nil {
			t.Fatal("old request succeeded after server EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("old request remained blocked after server EOF")
	}

	_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":2,"method":"test"}`))
	if err == nil {
		t.Fatal("same-target request succeeded while obsolete process group remained alive")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "new-pid")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("replacement launched after failed cleanup while old group survived: %v", statErr)
	}

	signalProcessGroup = originalSignal
	processTreeCleanupTimeout = originalTimeout
	if err := os.WriteFile(filepath.Join(dir, "stop-old-child"), nil, 0o600); err != nil {
		t.Fatalf("stop old helper child: %v", err)
	}
	waitForProcessExit(t, childPID)

	response, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":3,"method":"test"}`))
	if err != nil {
		t.Fatalf("same-target request after old group exit error = %v", err)
	}
	if string(response) != `{"jsonrpc":"2.0","id":3,"result":"replacement"}` {
		t.Fatalf("replacement response = %s", response)
	}
}

func TestAcquireManager_GIVENObsoleteCleanupFails_WHENReplacing_THENReturnsAcquisitionError(t *testing.T) {
	isolateGlobalLSPManager(t)
	t.Setenv("KTML_LSP_STUBBORN_TREE_HELPER", "1")
	dir := t.TempDir()
	manager := GetManager(lspStubbornTreeTestConfig("parent", dir))
	t.Cleanup(func() { _ = manager.Close() })

	requestResult := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
		requestResult <- err
	}()
	parentPID := waitForAtomicHelperPID(t, filepath.Join(dir, "parent-pid"))
	childPID := waitForAtomicHelperPID(t, filepath.Join(dir, "child-pid"))

	originalSignal := signalProcessGroup
	originalTimeout := processTreeCleanupTimeout
	processTreeCleanupTimeout = 25 * time.Millisecond
	signalProcessGroup = func(int, syscall.Signal) error { return syscall.EPERM }
	t.Cleanup(func() {
		signalProcessGroup = originalSignal
		processTreeCleanupTimeout = originalTimeout
		_ = os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600)
		_ = manager.Close()
		waitForProcessExit(t, parentPID)
		waitForProcessExit(t, childPID)
	})

	replacement, err := AcquireManager(lspStubbornTreeTestConfig("replacement", t.TempDir()))
	if replacement != nil {
		_ = replacement.Close()
		t.Fatal("AcquireManager() returned a manager after obsolete cleanup failed")
	}
	if err == nil || !strings.Contains(err.Error(), "obsolete process-tree cleanup failed") {
		t.Fatalf("AcquireManager() error = %v, want direct obsolete cleanup failure", err)
	}
	compatibilityManager := GetManager(lspStubbornTreeTestConfig("replacement", t.TempDir()))
	if identity := compatibilityManager.GenerationCacheIdentity(); identity != nil {
		t.Fatalf("failed GetManager() cache identity = %v, want cache bypass", identity)
	}
	if closeErr := compatibilityManager.Close(); closeErr == nil || !strings.Contains(closeErr.Error(), "obsolete process-tree cleanup failed") {
		t.Fatalf("failed GetManager().Close() error = %v, want acquisition failure", closeErr)
	}
	select {
	case requestErr := <-requestResult:
		if requestErr == nil {
			t.Fatal("obsolete request succeeded during failed replacement")
		}
	case <-time.After(time.Second):
		t.Fatal("obsolete request remained blocked after acquisition failed")
	}
}

func TestAcquireManager_GIVENConcurrentReplacementWaiters_WHENCleanupFails_THENSharesResultAndAllowsSafeLaterRetry(t *testing.T) {
	isolateGlobalLSPManager(t)
	t.Setenv("KTML_LSP_STUBBORN_TREE_HELPER", "1")
	dir := t.TempDir()
	manager := GetManager(lspStubbornTreeTestConfig("parent", dir))
	t.Cleanup(func() { _ = manager.Close() })

	go func() {
		_, _ = manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	}()
	parentPID := waitForAtomicHelperPID(t, filepath.Join(dir, "parent-pid"))
	childPID := waitForAtomicHelperPID(t, filepath.Join(dir, "child-pid"))

	originalSignal := signalProcessGroup
	originalTimeout := processTreeCleanupTimeout
	processTreeCleanupTimeout = 30 * time.Millisecond
	var probes atomic.Int32
	signalProcessGroup = func(_ int, signal syscall.Signal) error {
		if signal == 0 {
			probes.Add(1)
		}
		return syscall.EPERM
	}
	t.Cleanup(func() {
		signalProcessGroup = originalSignal
		processTreeCleanupTimeout = originalTimeout
		_ = os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600)
		_ = manager.Close()
		waitForProcessExit(t, parentPID)
		waitForProcessExit(t, childPID)
	})

	const callers = 5
	start := make(chan struct{})
	results := make(chan error, callers)
	replacementConfig := lspStubbornTreeTestConfig("replacement", t.TempDir())
	startedAt := time.Now()
	for range callers {
		go func() {
			<-start
			acquired, err := AcquireManager(replacementConfig)
			if acquired != nil {
				_ = acquired.Close()
			}
			results <- err
		}()
	}
	close(start)

	var sharedError string
	for range callers {
		select {
		case err := <-results:
			if err == nil {
				t.Fatal("concurrent replacement acquisition unexpectedly succeeded")
			}
			if sharedError == "" {
				sharedError = err.Error()
			} else if err.Error() != sharedError {
				t.Fatalf("concurrent replacement errors differ: %q != %q", err, sharedError)
			}
		case <-time.After(120 * time.Millisecond):
			t.Fatalf("concurrent callers serially paid cleanup deadlines after %v (%d probes)", time.Since(startedAt), probes.Load())
		}
	}
	if elapsed := time.Since(startedAt); elapsed > 100*time.Millisecond {
		t.Fatalf("concurrent callers serially paid cleanup deadlines: %v (%d probes)", elapsed, probes.Load())
	}

	signalProcessGroup = originalSignal
	processTreeCleanupTimeout = originalTimeout
	if err := os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600); err != nil {
		t.Fatalf("stop obsolete helper child: %v", err)
	}
	waitForProcessExit(t, childPID)

	recovered, err := AcquireManager(replacementConfig)
	if err != nil {
		t.Fatalf("AcquireManager() after obsolete group exit error = %v", err)
	}
	t.Cleanup(func() { _ = recovered.Close() })
}

func TestManager_GIVENMultipleProcessGroupsRemainAlive_WHENClosed_THENWholeCleanupUsesOneDeadline(t *testing.T) {
	isolateGlobalLSPManager(t)
	t.Setenv("KTML_LSP_STUBBORN_TREE_HELPER", "1")
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	cfg := config.Default()
	cfg.LSP["first"] = config.LSP{Command: []string{
		os.Args[0], "-test.run=^TestLSPStubbornTreeHelperProcess$", "--", "parent", firstDir,
	}}
	cfg.LSP["second"] = config.LSP{Command: []string{
		os.Args[0], "-test.run=^TestLSPStubbornTreeHelperProcess$", "--", "parent", secondDir,
	}}
	manager := GetManager(cfg)
	t.Cleanup(func() { _ = manager.Close() })

	for index, target := range []string{"first", "second"} {
		go func() {
			_, _ = manager.ProxyRequest(context.Background(), target, fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"method":"test"}`, index+1))
		}()
	}
	parentPIDs := []int{
		waitForAtomicHelperPID(t, filepath.Join(firstDir, "parent-pid")),
		waitForAtomicHelperPID(t, filepath.Join(secondDir, "parent-pid")),
	}
	childPIDs := []int{
		waitForAtomicHelperPID(t, filepath.Join(firstDir, "child-pid")),
		waitForAtomicHelperPID(t, filepath.Join(secondDir, "child-pid")),
	}

	originalSignal := signalProcessGroup
	originalTimeout := processTreeCleanupTimeout
	processTreeCleanupTimeout = 50 * time.Millisecond
	signalProcessGroup = func(int, syscall.Signal) error { return syscall.EPERM }
	t.Cleanup(func() {
		signalProcessGroup = originalSignal
		processTreeCleanupTimeout = originalTimeout
		for _, dir := range []string{firstDir, secondDir} {
			_ = os.WriteFile(filepath.Join(dir, "stop-child"), nil, 0o600)
		}
		_ = manager.Close()
		for _, pid := range append(parentPIDs, childPIDs...) {
			waitForProcessExit(t, pid)
		}
	})

	started := time.Now()
	err := manager.Close()
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("Close() succeeded while both obsolete groups remained alive")
	}
	if elapsed > 80*time.Millisecond {
		t.Fatalf("Close() serialized process cleanup deadlines: %v", elapsed)
	}
}

func TestCleanupProcessTree_GIVENParentWaitBlocksAfterExit_WHENDeadlineExpires_THENReturnsByDeadline(t *testing.T) {
	t.Setenv("KTML_LSP_BLOCKED_WAIT_HELPER", "1")
	originalTimeout := processTreeCleanupTimeout
	processTreeCleanupTimeout = 25 * time.Millisecond
	t.Cleanup(func() { processTreeCleanupTimeout = originalTimeout })

	writer := &blockingProcessWriter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLSPBlockedWaitHelperProcess$")
	cmd.Env = os.Environ()
	cmd.Stdout = writer
	if err := prepareProcessTree(cmd); err != nil {
		t.Fatalf("prepare helper process tree: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	tree, err := trackProcessTree(cmd)
	if err != nil {
		t.Fatalf("track helper process tree: %v", err)
	}
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("helper output did not reach the blocking writer")
	}

	result := make(chan error, 1)
	go func() { result <- cleanupProcessTree(tree) }()
	select {
	case err := <-result:
		close(writer.release)
		if err == nil {
			t.Fatal("cleanupProcessTree() succeeded while parent Wait remained blocked")
		}
	case <-time.After(250 * time.Millisecond):
		close(writer.release)
		err := <-result
		t.Fatalf("cleanupProcessTree() exceeded its complete deadline: %v", err)
	}
}

func TestRetryProcessTreeCleanup_GIVENOriginalParentWaitIsIncomplete_WHENWaitLaterCompletes_THENReusesWaiterAndRecovers(t *testing.T) {
	t.Setenv("KTML_LSP_BLOCKED_WAIT_HELPER", "1")
	originalTimeout := processTreeCleanupTimeout
	processTreeCleanupTimeout = 25 * time.Millisecond
	t.Cleanup(func() { processTreeCleanupTimeout = originalTimeout })

	writer := &blockingProcessWriter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLSPBlockedWaitHelperProcess$")
	cmd.Env = os.Environ()
	cmd.Stdout = writer
	if err := prepareProcessTree(cmd); err != nil {
		t.Fatalf("prepare helper process tree: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	tree, err := trackProcessTree(cmd)
	if err != nil {
		t.Fatalf("track helper process tree: %v", err)
	}
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("helper output did not reach the blocking writer")
	}

	if err := cleanupProcessTree(tree); err == nil {
		close(writer.release)
		t.Fatal("cleanupProcessTree() succeeded while parent Wait remained blocked")
	}
	if err := retryProcessTreeCleanup(tree); err == nil {
		close(writer.release)
		t.Fatal("retryProcessTreeCleanup() forgot the incomplete parent waiter")
	}
	close(writer.release)
	if err := retryProcessTreeCleanup(tree); err != nil {
		t.Fatalf("retryProcessTreeCleanup() after original waiter completed error = %v", err)
	}
}

type blockingProcessWriter struct {
	entered chan struct{}
	release chan struct{}
}

func (w *blockingProcessWriter) Write(p []byte) (int, error) {
	select {
	case <-w.entered:
	default:
		close(w.entered)
	}
	<-w.release
	return len(p), nil
}

func TestLSPBlockedWaitHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_BLOCKED_WAIT_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.Write([]byte("ready"))
	time.Sleep(time.Hour)
}

func TestLSPReplacementExclusionHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_REPLACEMENT_EXCLUSION_HELPER") != "1" {
		return
	}
	dir := os.Args[len(os.Args)-1]
	if len(os.Args) >= 2 && os.Args[len(os.Args)-2] == "child" {
		if publishHelperPID(filepath.Join(dir, "old-child-pid")) != nil {
			os.Exit(6)
		}
		waitForHelperFile(filepath.Join(dir, "stop-old-child"))
		return
	}
	payload, err := readLSPManagerTestFrame(bufio.NewReader(os.Stdin))
	if err != nil {
		os.Exit(2)
	}
	first, err := os.OpenFile(filepath.Join(dir, "started"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		_ = first.Close()
		child := exec.Command(os.Args[0], "-test.run=^TestLSPReplacementExclusionHelperProcess$", "--", "child", dir)
		child.Env = os.Environ()
		if child.Start() != nil {
			os.Exit(3)
		}
		_ = child.Process.Release()
		waitForHelperFile(filepath.Join(dir, "old-child-pid"))
		if publishHelperPID(filepath.Join(dir, "old-pid")) != nil {
			os.Exit(7)
		}
		waitForHelperFile(filepath.Join(dir, "release-old"))
		_ = os.Stdout.Close()
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	if !errors.Is(err, os.ErrExist) {
		os.Exit(4)
	}
	if publishHelperPID(filepath.Join(dir, "new-pid")) != nil {
		os.Exit(5)
	}
	writeLSPManagerTestResponse(payload, `"result":"replacement"`)
}

func lspReplacementExclusionTestConfig(dir string) *config.Config {
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{
		os.Args[0], "-test.run=^TestLSPReplacementExclusionHelperProcess$", "--", dir,
	}}
	return cfg
}

func isolateGlobalLSPManager(t *testing.T) {
	t.Helper()
	globalManager.Lock()
	if globalManager.transition != nil {
		globalManager.Unlock()
		t.Fatal("cannot isolate global LSP manager during replacement")
	}
	previous := globalManager.current
	globalManager.current = nil
	globalManager.Unlock()
	t.Cleanup(func() {
		globalManager.Lock()
		current := globalManager.current
		globalManager.current = previous
		globalManager.transition = nil
		globalManager.Unlock()
		if current != nil && current != previous {
			_ = current.closeNow()
		}
	})
}
