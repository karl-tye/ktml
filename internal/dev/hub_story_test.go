package dev

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/dev/watcher"
)

// Stories are Component Explorer fixtures: the application never renders
// them, so editing one must not regenerate, rebuild, or reload the app.
func TestHub_GIVENAStoryEdit_WHENReloaded_THENTheApplicationIsUntouched(t *testing.T) {
	dir := t.TempDir()
	card := filepath.Join(dir, "card.ktml")
	story := filepath.Join(dir, "card.story.ktml")
	if err := os.WriteFile(card, []byte("Card [title: string] {\n\tp \"{title}\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(story, []byte("import { Card } from \"./card.ktml\"\n\nstory Card.A [title = \"a\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	generated := make(chan Batch, 1)
	hub := NewHub(nil, nil, 0, func(batch Batch) error {
		generated <- batch
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	events := make(chan watcher.Event, 1)
	go hub.Listen(ctx, events)
	events <- watcher.Event{Type: watcher.EventReload, Files: []string{story}}
	select {
	case batch := <-generated:
		t.Fatalf("a story edit generated %+v", batch)
	case <-time.After(100 * time.Millisecond):
	}
	if len(hub.pending) != 0 {
		t.Errorf("pending = %v", hub.pending)
	}
}
