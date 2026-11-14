package wasm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/buildstate"
)

type generationIdentityProxy string

func (generationIdentityProxy) ProxyRequest(context.Context, string, []byte) ([]byte, error) {
	return []byte(`{"jsonrpc":"2.0","id":1,"result":null}`), nil
}

func (p generationIdentityProxy) GenerationCacheIdentity() any {
	return string(p)
}

func TestPlugin_GIVENLSPProxyIdentityChanges_WHENSourceIsUnchanged_THENInvalidatesCache(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	wasmBytes := buildIgnoringProxyErrorPlugin(t)
	ctx := context.Background()
	generate := func(identity generationIdentityProxy) {
		t.Helper()
		plugin, err := NewPluginWithOptions(ctx, wasmBytes, Options{Proxy: identity, Outputs: anyOutputs})
		if err != nil {
			t.Fatalf("NewPluginWithProxy() error = %v", err)
		}
		defer plugin.Close(ctx)
		if err := plugin.Generate("page.ktml"); err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
	}
	source, err := filepath.Abs("page.ktml")
	if err != nil {
		t.Fatalf("resolve source: %v", err)
	}
	statePath := filepath.Join(".ktml", "state.json")
	generate("first proxy")
	initial, err := buildstate.Load(statePath)
	if err != nil {
		t.Fatalf("load initial build state: %v", err)
	}
	initialHash := initial.Files[source].Hash

	generate("second proxy")
	changed, err := buildstate.Load(statePath)
	if err != nil {
		t.Fatalf("load changed build state: %v", err)
	}
	if changed.Files[source].Hash == initialHash {
		t.Fatal("changed LSP proxy identity reused the previous generation cache identity")
	}
}
