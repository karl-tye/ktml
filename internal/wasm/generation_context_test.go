package wasm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type contextWaitingProxy struct{}

func (contextWaitingProxy) ProxyRequest(ctx context.Context, _ string, _ []byte) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestPlugin_GIVENProxyWaits_WHENGenerateContextDeadlineExpires_THENFailsWithoutPublication(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	ctx := context.Background()
	plugin, err := NewPluginWithProxy(ctx, buildProxyOutputPlugin(t), nil, nil, contextWaitingProxy{})
	if err != nil {
		t.Fatalf("NewPluginWithProxy() error = %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(context.Background()) })

	requestCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = plugin.GenerateContext(requestCtx, "page.ktml")

	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("GenerateContext() error = %v, want caller deadline", err)
	}
	if _, statErr := os.Stat("page.out"); !os.IsNotExist(statErr) {
		t.Fatalf("output published after cancellation: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(".ktml", "state.json")); !os.IsNotExist(statErr) {
		t.Fatalf("build state published after cancellation: %v", statErr)
	}
}
