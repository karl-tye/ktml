//go:build unix

package lsp

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var signalProcessGroup = func(groupID int, signal syscall.Signal) error {
	return syscall.Kill(-groupID, signal)
}

type processTree struct {
	cmd                 *exec.Cmd
	groupID             int
	cleanupMu           sync.Mutex
	groupSignalComplete bool
	groupSignalErr      error
	waitOnce            sync.Once
	waitDone            chan struct{}
}

func prepareProcessTree(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}

func trackProcessTree(cmd *exec.Cmd) (*processTree, error) {
	if cmd.Process == nil {
		return nil, fmt.Errorf("LSP process did not start")
	}
	groupID, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		rollbackStartedProcess(cmd)
		return nil, fmt.Errorf("verify LSP process group: %w", err)
	}
	if groupID != cmd.Process.Pid {
		rollbackStartedProcess(cmd)
		return nil, fmt.Errorf("LSP process %d joined unexpected process group %d", cmd.Process.Pid, groupID)
	}
	sessionID, err := unix.Getsid(cmd.Process.Pid)
	if err != nil || sessionID != cmd.Process.Pid {
		rollbackStartedProcess(cmd)
		if err != nil {
			return nil, fmt.Errorf("verify LSP process session: %w", err)
		}
		return nil, fmt.Errorf("LSP process %d joined unexpected session %d", cmd.Process.Pid, sessionID)
	}
	return &processTree{cmd: cmd, groupID: groupID, waitDone: make(chan struct{})}, nil
}

func rollbackStartedProcess(cmd *exec.Cmd) {
	// Start already applied Setsid, so the unreaped child PID safely names the
	// new session group even when post-start verification races with its exit.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

func terminateProcessTree(tree *processTree, deadline time.Time) error {
	if tree == nil || tree.cmd.Process == nil {
		return nil
	}
	if !time.Now().Before(deadline) {
		return fmt.Errorf("LSP process-tree cleanup exceeded its deadline before signaling")
	}
	groupErr := signalProcessGroup(tree.groupID, syscall.SIGKILL)
	if errors.Is(groupErr, syscall.ESRCH) {
		return nil
	}
	if groupErr != nil {
		return fmt.Errorf("terminate isolated LSP process group %d: %w", tree.groupID, groupErr)
	}
	return nil
}

func cleanupProcessTree(tree *processTree) error {
	return cleanupProcessTreeBefore(tree, time.Now().Add(processTreeCleanupTimeout))
}

func cleanupProcessTreeBefore(tree *processTree, deadline time.Time) error {
	if tree == nil || tree.cmd.Process == nil {
		return nil
	}
	tree.cleanupMu.Lock()
	defer tree.cleanupMu.Unlock()
	if !time.Now().Before(deadline) {
		return fmt.Errorf("LSP process-tree cleanup exceeded its deadline")
	}
	if !tree.groupSignalComplete && tree.groupSignalErr == nil {
		if err := terminateProcessTree(tree, deadline); err != nil {
			tree.groupSignalErr = err
		} else {
			tree.groupSignalComplete = true
		}
	}
	if tree.groupSignalErr != nil {
		// Reap the leader so a failed cleanup cannot leave a zombie. The retained
		// error permanently disables group signaling: after Wait, this numeric
		// PGID may be reused and must never become a destructive retry target.
		parentErr := tree.cmd.Process.Kill()
		if errors.Is(parentErr, os.ErrProcessDone) || errors.Is(parentErr, syscall.ESRCH) {
			parentErr = nil
		}
		waitErr := waitForParentExit(tree, deadline)
		probeErr := waitForProcessGroupTermination(tree.groupID, deadline)
		if waitErr == nil && probeErr == nil {
			return nil
		}
		return errors.Join(tree.groupSignalErr, parentErr, waitErr, probeErr)
	}

	// Setsid makes the child both session and process-group leader. While it is
	// unreaped, its PID/SID/PGID cannot be reused, and a process outside the
	// isolated session cannot join the group. Destructive signaling is therefore
	// safe only before this Wait; descendants that remain in the inherited group
	// are covered atomically by the group signal without process enumeration.
	waitErr := waitForParentExit(tree, deadline)
	probeErr := waitForProcessGroupTermination(tree.groupID, deadline)
	if probeErr == nil {
		return waitErr
	}
	return errors.Join(waitErr, probeErr)
}

func waitForParentExit(tree *processTree, deadline time.Time) error {
	tree.waitOnce.Do(func() {
		go func() {
			_ = tree.cmd.Wait()
			close(tree.waitDone)
		}()
	})
	timer := time.NewTimer(max(time.Until(deadline), 0))
	defer timer.Stop()
	select {
	case <-tree.waitDone:
		return nil
	case <-timer.C:
		return fmt.Errorf("LSP parent process %d remained unreaped after cleanup deadline", tree.cmd.Process.Pid)
	}
}

func retryProcessTreeCleanup(tree *processTree) error {
	return retryProcessTreeCleanupBefore(tree, time.Now().Add(processTreeCleanupTimeout))
}

func retryProcessTreeCleanupBefore(tree *processTree, deadline time.Time) error {
	return cleanupProcessTreeBefore(tree, deadline)
}

func waitForProcessGroupTermination(groupID int, deadline time.Time) error {
	for {
		err := signalProcessGroup(groupID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("probe isolated LSP process group %d: %w", groupID, err)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("LSP process group %d remained alive after cleanup deadline", groupID)
		}
		time.Sleep(time.Millisecond)
	}
}
