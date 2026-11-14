//go:build unix

package lsp

import (
	"errors"
	"io"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

type errorCloser struct {
	err error
}

func (closer errorCloser) Write(p []byte) (int, error) { return len(p), nil }
func (closer errorCloser) Close() error                { return closer.err }

func TestCleanupProcessTree_RetainsSessionAnchorUntilGroupSignalAndReapsAfterward(t *testing.T) {
	tree := startAnchoredProcess(t, "sleep 60")
	originalSignal := signalProcessGroup
	t.Cleanup(func() { signalProcessGroup = originalSignal })
	signalProcessGroup = func(groupID int, signal syscall.Signal) error {
		if signal == syscall.SIGKILL {
			select {
			case <-tree.waitDone:
				t.Fatal("session anchor was reaped before destructive group signaling")
			default:
			}
		}
		return originalSignal(groupID, signal)
	}

	if err := cleanupProcessTree(tree); err != nil {
		t.Fatalf("cleanupProcessTree() error = %v", err)
	}
	select {
	case <-tree.waitDone:
	default:
		t.Fatal("cleanupProcessTree() returned before reaping the session anchor")
	}
}

func TestRetryProcessTreeCleanup_AfterAnchorReapNeverDestructivelySignalsReusedGroup(t *testing.T) {
	tree := startAnchoredProcess(t, "sleep 60")
	if err := cleanupProcessTree(tree); err != nil {
		t.Fatalf("initial cleanup error = %v", err)
	}

	unrelated := exec.Command("sleep", "60")
	if err := unrelated.Start(); err != nil {
		t.Fatalf("start unrelated process: %v", err)
	}
	t.Cleanup(func() {
		_ = unrelated.Process.Kill()
		_ = unrelated.Wait()
	})
	tree.groupID = unrelated.Process.Pid

	originalSignal := signalProcessGroup
	t.Cleanup(func() { signalProcessGroup = originalSignal })
	destructiveSignals := 0
	signalProcessGroup = func(groupID int, signal syscall.Signal) error {
		if signal != 0 {
			destructiveSignals++
		}
		return syscall.ESRCH
	}

	if err := retryProcessTreeCleanup(tree); err != nil {
		t.Fatalf("retryProcessTreeCleanup() error = %v", err)
	}
	if destructiveSignals != 0 {
		t.Fatalf("retry sent %d destructive signals after the anchor was reaped", destructiveSignals)
	}
	if err := unrelated.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("unrelated process was disturbed: %v", err)
	}
}

func TestWaitForProcessGroupTermination_FinalProbeWinsAtDeadline(t *testing.T) {
	originalSignal := signalProcessGroup
	t.Cleanup(func() { signalProcessGroup = originalSignal })
	signalProcessGroup = func(int, syscall.Signal) error { return syscall.ESRCH }

	if err := waitForProcessGroupTermination(1, time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("final successful probe returned stale deadline error: %v", err)
	}
}

func TestExternalServerStop_PropagatesPipeCloseFailuresAfterReaping(t *testing.T) {
	tree := startAnchoredProcess(t, "sleep 60")
	closeErr := errors.New("close failed")
	core := &managerCore{stopping: make(map[*ExternalServer]error)}
	server := &ExternalServer{
		manager:  core,
		tree:     tree,
		stdin:    errorCloser{err: closeErr},
		stdout:   io.NopCloser(&zeroReader{}),
		requests: make(map[string]*pendingRequest),
		stopDone: make(chan struct{}),
	}
	core.stopping[server] = nil

	server.stop(errors.New("stopped"), time.Now().Add(time.Second))
	if !errors.Is(server.stopErr, closeErr) {
		t.Fatalf("stop error = %v, want pipe close failure", server.stopErr)
	}
	select {
	case <-tree.waitDone:
	default:
		t.Fatal("close failure prevented session anchor reap")
	}
}

type zeroReader struct{}

func (*zeroReader) Read([]byte) (int, error) { return 0, io.EOF }

func startAnchoredProcess(t *testing.T, script string) *processTree {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	if err := prepareProcessTree(cmd); err != nil {
		t.Fatalf("prepare helper process: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	tree, err := trackProcessTree(cmd)
	if err != nil {
		t.Fatalf("track helper process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = waitForParentExit(tree, time.Now().Add(time.Second))
	})
	return tree
}
