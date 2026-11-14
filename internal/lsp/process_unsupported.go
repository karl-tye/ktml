//go:build !unix

package lsp

import (
	"fmt"
	"os/exec"
	"time"
)

type processTree struct {
	cmd *exec.Cmd
}

func prepareProcessTree(*exec.Cmd) error {
	return fmt.Errorf("LSP process-tree control is unavailable on this platform")
}

func trackProcessTree(cmd *exec.Cmd) (*processTree, error) {
	return &processTree{cmd: cmd}, nil
}

func terminateProcessTree(*processTree, time.Time) error {
	return fmt.Errorf("LSP process-tree control is unavailable on this platform")
}

func cleanupProcessTree(*processTree) error {
	return fmt.Errorf("LSP process-tree control is unavailable on this platform")
}

func cleanupProcessTreeBefore(*processTree, time.Time) error {
	return fmt.Errorf("LSP process-tree control is unavailable on this platform")
}

func retryProcessTreeCleanup(*processTree) error {
	return fmt.Errorf("LSP process-tree control is unavailable on this platform")
}

func retryProcessTreeCleanupBefore(*processTree, time.Time) error {
	return fmt.Errorf("LSP process-tree control is unavailable on this platform")
}
