package wasm

import (
	"context"
	"sync"
	"testing"
)

type namespacingProxy struct {
	mu         sync.Mutex
	namespaces []*recordingNamespace
	direct     int
}

type recordingNamespace struct {
	calls int
}

func (p *namespacingProxy) ProxyRequest(context.Context, string, []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.direct++
	return nil, nil
}

func (p *namespacingProxy) NewLSPNamespace() LSPProxy {
	p.mu.Lock()
	defer p.mu.Unlock()
	namespace := &recordingNamespace{}
	p.namespaces = append(p.namespaces, namespace)
	return namespace
}

func (n *recordingNamespace) ProxyRequest(context.Context, string, []byte) ([]byte, error) {
	n.calls++
	return nil, nil
}

func TestPlugin_GIVENSharedNamespacingProxy_WHENTwoPluginsProxy_THENEachUsesItsOwnNamespace(t *testing.T) {
	ctx := context.Background()
	proxy := &namespacingProxy{}
	guest := buildCancellationProxyGuest(t)
	for range 2 {
		plugin, err := NewPluginWithProxy(ctx, guest, nil, nil, proxy)
		if err != nil {
			t.Fatalf("NewPluginWithProxy() error = %v", err)
		}
		t.Cleanup(func() { _ = plugin.Close(ctx) })
		if _, err := plugin.ExecuteLSP(ctx, "$/cancelRequest", []byte(`{"id":"client"}`)); err != nil {
			t.Fatalf("ExecuteLSP() error = %v", err)
		}
	}
	if proxy.direct != 0 || len(proxy.namespaces) != 2 {
		t.Fatalf("direct calls = %d, namespaces = %d; want 0 and 2", proxy.direct, len(proxy.namespaces))
	}
	for index, namespace := range proxy.namespaces {
		if namespace.calls != 1 {
			t.Errorf("namespace %d calls = %d, want 1", index, namespace.calls)
		}
	}
}
