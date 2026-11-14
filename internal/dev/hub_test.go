package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/dev/livereload"
	"github.com/karl-tye/ktml/internal/dev/runner"
	"github.com/karl-tye/ktml/internal/dev/watcher"
)

func TestNewHub(t *testing.T) {
	lrServer := livereload.NewServer("http://localhost:8080")
	r := runner.New([]string{"sleep", "1"})
	port := 8080
	callback := func(Batch) error { return nil }

	hub := NewHub(lrServer, r, port, callback)

	if hub == nil {
		t.Fatalf("expected hub to not be nil")
	}
	if hub.lrServer != lrServer {
		t.Errorf("expected lrServer %v, got %v", lrServer, hub.lrServer)
	}
	if hub.runner != r {
		t.Errorf("expected runner %v, got %v", r, hub.runner)
	}
	if hub.targetPort != port {
		t.Errorf("expected port %v, got %v", port, hub.targetPort)
	}
	if hub.generateCallback == nil {
		t.Errorf("expected callback to be set")
	}
}

func TestTrackFile_GIVENValidKTML_WHENTracked_THENSeedsAST(t *testing.T) {
	path := filepath.Join(t.TempDir(), "components.ktml")
	if err := os.WriteFile(path, []byte(`Count [value: string] { span { "{value}" } }`), 0o600); err != nil {
		t.Fatalf("write KTML fixture: %v", err)
	}
	hub := NewHub(nil, nil, 0, nil)

	if err := hub.TrackFile(path); err != nil {
		t.Fatalf("TrackFile() error = %v", err)
	}
	if _, ok := hub.asts[filepath.Clean(path)]; !ok {
		t.Errorf("AST cache does not contain %s", path)
	}
}

func TestTrackFiles_GIVENALaterInvalidDocument_WHENTracked_THENSeedsTheDocumentsBeforeItAndReportsIt(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, file := range []struct{ name, source string }{
		{"a.ktml", `A { p "a" }`},
		{"b.ktml", `B { Missing }`},
		{"c.ktml", `C { p "c" }`},
	} {
		path := filepath.Join(dir, file.name)
		if err := os.WriteFile(path, []byte(file.source), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	hub := NewHub(nil, nil, 0, nil)

	err := hub.TrackFiles(paths)

	if err == nil || !strings.Contains(err.Error(), "b.ktml") {
		t.Fatalf("TrackFiles() error = %v, want the b.ktml failure", err)
	}
	if _, ok := hub.asts[paths[0]]; !ok {
		t.Error("a.ktml, before the failure, was not seeded")
	}
	if _, ok := hub.asts[paths[2]]; ok {
		t.Error("c.ktml, after the failure, was seeded")
	}
}

func TestHub_Listen_ExtractsFilePath(t *testing.T) {
	tempDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer os.Chdir(originalWd)
	os.Chdir(tempDir)

	os.WriteFile("test.ktml", []byte("div {}"), 0644)

	lrServer := livereload.NewServer("http://localhost:8080")
	r := runner.New([]string{"sleep", "10"})
	defer r.Stop()

	pathChan := make(chan string, 1)
	callback := func(batch Batch) error {
		for _, path := range batch.Generate {
			pathChan <- path
		}
		return nil
	}

	hub := NewHub(lrServer, r, 8080, callback)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan watcher.Event, 1)

	go hub.Listen(ctx, events)

	events <- watcher.Event{
		Type:  watcher.EventReload,
		Files: []string{"test.ktml"},
	}

	select {
	case receivedPath := <-pathChan:
		if receivedPath != "test.ktml" {
			t.Errorf("expected generated callback to receive 'test.ktml', got '%s'", receivedPath)
		}
	case <-time.After(1 * time.Second):
		t.Errorf("timeout waiting for callback")
	}
}

func TestLivereload_GIVENPageWithPropCacheTemplate_WHENProxied_THENKeepsNoCopyAndServesNoCacheRoute(t *testing.T) {
	// GIVEN a page that still embeds an old browser-visible prop cache
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head></head><body><template class="__ktml_cache" data-ktml-id="ktml-test1" data-ktml-component="App">{ "UserName":"Ada" }</template></body></html>`))
	}))
	defer targetServer.Close()

	lrServer := livereload.NewServer(targetServer.URL)
	proxyServer := httptest.NewServer(lrServer)
	defer proxyServer.Close()

	// WHEN it is proxied and the old cache route is called
	resp, err := http.Get(proxyServer.URL)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	resp.Body.Close()
	cache, err := http.Post(proxyServer.URL+"/__ktml/cache", "application/json", strings.NewReader(`{"App":{}}`))
	if err != nil {
		t.Fatalf("POST /__ktml/cache: %v", err)
	}
	cache.Body.Close()

	// THEN the dev server has no cache route: props live only in the app
	if cache.StatusCode != http.StatusNotFound {
		t.Errorf("POST /__ktml/cache status = %d, want 404", cache.StatusCode)
	}
}

func TestHub_Listen_GIVENDeletedSource_WHENReloaded_THENBatchRemovesIt(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	lrServer := livereload.NewServer("http://localhost:8080")
	batches := make(chan Batch, 1)
	hub := NewHub(lrServer, nil, 8080, func(batch Batch) error {
		batches <- batch
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan watcher.Event, 1)

	go hub.Listen(ctx, events)

	events <- watcher.Event{
		Type:  watcher.EventReload,
		Files: []string{"deleted.ktml"},
	}

	select {
	case batch := <-batches:
		if len(batch.Generate) != 0 || len(batch.Remove) != 1 || batch.Remove[0] != "deleted.ktml" {
			t.Errorf("batch = %+v, want only deleted.ktml removed", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("deleted source was not published as a batch removal")
	}
}
