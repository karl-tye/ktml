package dev

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/dev/livereload"
	"github.com/karl-tye/ktml/internal/dev/runner"
	"github.com/karl-tye/ktml/internal/dev/watcher"
)

func TestHub_GIVENSemanticInvalidEdit_WHENReloaded_THENPreservesLastValidASTAndSkipsGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "components.ktml")
	if err := os.WriteFile(path, []byte(`Card { div { "valid" } }`), 0o600); err != nil {
		t.Fatalf("write valid source: %v", err)
	}
	generated := make(chan string, 2)
	hub := NewHub(nil, nil, 0, func(batch Batch) error {
		for _, path := range batch.Generate {
			generated <- path
		}
		return nil
	})
	if err := hub.TrackFile(path); err != nil {
		t.Fatalf("TrackFile() error = %v", err)
	}
	cleanPath := filepath.Clean(path)
	validDocument := hub.asts[cleanPath]
	if err := os.WriteFile(path, []byte(`Card { Missing }`), 0o600); err != nil {
		t.Fatalf("write semantic-invalid source: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan watcher.Event, 1)
	done := make(chan struct{})
	go func() {
		hub.Listen(ctx, events)
		close(done)
	}()
	events <- watcher.Event{Type: watcher.EventReload, Files: []string{path}}
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	select {
	case file := <-generated:
		t.Fatalf("generated semantic-invalid source %q", file)
	default:
	}
	if got := hub.asts[cleanPath]; got != validDocument {
		t.Error("semantic-invalid edit replaced the last valid AST")
	}
}

func TestHub_GIVENGenerationFailure_WHENReloaded_THENPreservesLastPublishedAST(t *testing.T) {
	path := filepath.Join(t.TempDir(), "components.ktml")
	if err := os.WriteFile(path, []byte(`Card { div { "old" } }`), 0o600); err != nil {
		t.Fatalf("write initial source: %v", err)
	}
	hub := NewHub(nil, nil, 0, func(Batch) error { return errors.New("generation failed") })
	if err := hub.TrackFile(path); err != nil {
		t.Fatalf("TrackFile() error = %v", err)
	}
	cleanPath := filepath.Clean(path)
	initialDocument := hub.asts[cleanPath]
	if err := os.WriteFile(path, []byte(`Card { div { "new" } }`), 0o600); err != nil {
		t.Fatalf("write changed source: %v", err)
	}

	listenForOneReload(t, hub, path)

	if got := hub.asts[cleanPath]; got != initialDocument {
		t.Error("generation failure replaced the last published AST")
	}
}

func TestHub_GIVENRerenderAndReloadPreparationFailure_WHENReloaded_THENPreservesLastPublishedAST(t *testing.T) {
	path := filepath.Join(t.TempDir(), "components.ktml")
	if err := os.WriteFile(path, []byte(`Card { div { "old" } }`), 0o600); err != nil {
		t.Fatalf("write initial source: %v", err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	hub := NewHub(livereload.NewServer(target.URL), runner.New(nil), 0, func(Batch) error { return nil })
	if err := hub.TrackFile(path); err != nil {
		t.Fatalf("TrackFile() error = %v", err)
	}
	cleanPath := filepath.Clean(path)
	initialDocument := hub.asts[cleanPath]
	if err := os.WriteFile(path, []byte(`Card { div { "new" } }`), 0o600); err != nil {
		t.Fatalf("write changed source: %v", err)
	}

	listenForOneReload(t, hub, path)

	if got := hub.asts[cleanPath]; got != initialDocument {
		t.Error("failed rerender/reload preparation replaced the last published AST")
	}
}

func listenForOneReload(t *testing.T, hub *Hub, path string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan watcher.Event, 1)
	done := make(chan struct{})
	go func() {
		hub.Listen(ctx, events)
		close(done)
	}()
	events <- watcher.Event{Type: watcher.EventReload, Files: []string{path}}
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
}
