package dev

import (
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/dev/watcher"
)

func TestHub_GIVENAnAssetOnlyBatch_WHENListened_THENTellsPagesToReloadAssetsWithoutGenerating(t *testing.T) {
	f := newRestartBatchFixture(t, map[string]string{
		"page.ktml": `Page { div }`,
		"site.css":  `body { color: red }`,
	})

	f.listen <- watcher.Event{Type: watcher.EventReload, Assets: []string{"static/site.css"}}

	if event := nextEvent(t, f.events); event != "assets" {
		t.Fatalf("SSE event = %q, want assets", event)
	}
	select {
	case batch := <-f.batches:
		t.Fatalf("generated %+v for an asset", batch)
	case <-time.After(50 * time.Millisecond):
	}
	if starts := runnerStarts(t, f.startLog); starts != 1 {
		t.Errorf("runner starts = %d, want no restart for an asset", starts)
	}
}

func TestHub_GIVENARestartBatchWithAnAsset_WHENListened_THENTheFullReloadCoversIt(t *testing.T) {
	f := newRestartBatchFixture(t, map[string]string{
		"page.ktml": `Page { div }`,
	})

	f.listen <- watcher.Event{Type: watcher.EventRestart, Files: []string{"main.go"}, Assets: []string{"static/site.css"}}

	if event := nextEvent(t, f.events); event != "reload" {
		t.Fatalf("SSE event = %q, want reload", event)
	}
	select {
	case event := <-f.events:
		t.Fatalf("published %q after the full reload", event)
	case <-time.After(100 * time.Millisecond):
	}
}
