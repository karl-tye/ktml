package dev

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/dev/watcher"
)

func TestHub_GIVENInvalidEdit_WHENReloaded_THENPreservesLastValidASTAndSkipsGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "components.ktml")
	if err := os.WriteFile(path, []byte(`Card { div }`), 0o600); err != nil {
		t.Fatalf("write valid source: %v", err)
	}
	generated := make(chan string, 1)
	hub := NewHub(nil, nil, 0, func(batch Batch) error {
		for _, path := range batch.Generate {
			generated <- path
		}
		return nil
	})
	if err := hub.TrackFile(path); err != nil {
		t.Fatalf("TrackFile() error = %v", err)
	}
	validDocument := hub.asts[filepath.Clean(path)]
	if err := os.WriteFile(path, []byte(`Card { div`), 0o600); err != nil {
		t.Fatalf("write invalid source: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan watcher.Event, 1)
	go hub.Listen(ctx, events)
	events <- watcher.Event{Type: watcher.EventReload, Files: []string{path}}
	t.Cleanup(cancel)

	select {
	case file := <-generated:
		t.Fatalf("generated invalid source %q", file)
	case <-time.After(100 * time.Millisecond):
	}
	if got := hub.asts[filepath.Clean(path)]; got != validDocument {
		t.Error("invalid edit replaced the last valid AST")
	}
}
