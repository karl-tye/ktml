package wasm

import (
	"context"
	"os"
	"testing"
)

type identifiedOutputProxy struct {
	content string
	calls   *int
}

func (p identifiedOutputProxy) ProxyRequest(context.Context, string, []byte) ([]byte, error) {
	*p.calls++
	return []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":"` + p.content + `","output_path":"page.out"}}`), nil
}

func (identifiedOutputProxy) GenerationCacheIdentity() any {
	return "stable-claimed-identity"
}

func TestPlugin_GIVENIdentifiedLSPProxy_WHENGeneratingUnchangedSourceTwice_THENBypassesGenerationCache(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	calls := 0
	proxy := identifiedOutputProxy{content: "proxied", calls: &calls}
	plugin, err := NewPluginWithOptions(context.Background(), buildProxyOutputPlugin(t), Options{Proxy: proxy, Outputs: anyOutputs})
	if err != nil {
		t.Fatalf("NewPluginWithProxy() error = %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(context.Background()) })

	for range 2 {
		if err := plugin.Generate("page.ktml"); err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
	}
	if calls != 2 {
		t.Fatalf("LSP proxy calls = %d, want generation cache bypassed", calls)
	}
}
