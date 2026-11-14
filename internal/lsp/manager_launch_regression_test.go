package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

func TestManager_GIVENCommandUnresolvedAtAcquisition_WHENPATHLaterProvidesIt_THENLaunchStillFails(t *testing.T) {
	t.Setenv("KTML_LSP_HELPER", "1")
	missingPath := t.TempDir()
	availablePath := t.TempDir()
	const commandName = "ktml-lsp-late-command"
	t.Setenv("PATH", missingPath)
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{
		commandName, "-test.run=^TestLSPManagerHelperProcess$", "--", "reply", "late-path",
	}}
	manager := GetManager(cfg)
	t.Cleanup(func() { _ = manager.Close() })

	if err := os.Symlink(os.Args[0], filepath.Join(availablePath, commandName)); err != nil {
		t.Fatalf("create late PATH command: %v", err)
	}
	t.Setenv("PATH", availablePath)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := manager.ProxyRequest(ctx, "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("ProxyRequest() response = %s, error = %v, want acquisition-time executable resolution failure", response, err)
	}
}
