package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/dev/livereload"
	"github.com/karl-tye/ktml/internal/dev/runner"
	"github.com/karl-tye/ktml/internal/dev/watcher"
)

type restartBatchFixture struct {
	hub      *Hub
	events   <-chan string
	listen   chan watcher.Event
	startLog string
	batches  chan Batch
	starts   chan int
}

func newRestartBatchFixture(t *testing.T, sources map[string]string) *restartBatchFixture {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	for path, source := range sources {
		writeHubSource(t, path, source)
	}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(target.Close)
	liveReload := livereload.NewServer(target.URL)
	events, closeSSE := openHubSSE(t, liveReload)
	t.Cleanup(closeSSE)

	f := &restartBatchFixture{events: events, listen: make(chan watcher.Event, 1), batches: make(chan Batch, 4), startLog: filepath.Join(dir, "runner-starts")}
	process := runner.New([]string{"sh", "-c", `echo started >> "$1"; exec sleep 30`, "runner", f.startLog})
	ctx, cancel := context.WithCancel(context.Background())
	if err := process.Start(ctx); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(func() { _ = process.Stop() })
	waitForRunnerStarts(t, f.startLog, 1)

	f.hub = NewHub(liveReload, process, 0, func(batch Batch) error {
		// The restart must build the fresh output: generation precedes it.
		if starts := runnerStarts(t, f.startLog); starts != 1 {
			t.Errorf("generation ran after %d runner starts, want before the restart", starts)
		}
		f.batches <- batch
		return nil
	})
	for path := range sources {
		if filepath.Ext(path) != ".ktml" {
			continue
		}
		if err := f.hub.TrackFile(path); err != nil {
			t.Fatalf("TrackFile(%s) error = %v", path, err)
		}
	}
	done := make(chan struct{})
	go func() {
		f.hub.Listen(ctx, f.listen)
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return f
}

func TestHub_GIVENRestartBatchWithKTMLChange_WHENListened_THENGeneratesBeforeRestartAndReloadsOnce(t *testing.T) {
	f := newRestartBatchFixture(t, map[string]string{
		"page.ktml": `Page { div { "old" } }`,
		"main.go":   "package main\n",
	})
	baseline := f.hub.asts["page.ktml"]
	writeHubSource(t, "page.ktml", `Page { div { "new" } }`)
	writeHubSource(t, "main.go", "package main\n\n// changed\n")

	f.listen <- watcher.Event{Type: watcher.EventRestart, Files: []string{"page.ktml", "main.go"}}

	select {
	case batch := <-f.batches:
		if !slices.Equal(batch.Generate, []string{"page.ktml"}) {
			t.Errorf("batch generates %v, want the changed KTML source", batch.Generate)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the KTML source in a restart batch was not generated")
	}
	if event := nextEvent(t, f.events); event != "reload" {
		t.Fatalf("SSE event = %q, want one reload", event)
	}
	waitForRunnerStarts(t, f.startLog, 2)
	select {
	case event := <-f.events:
		t.Fatalf("published a second SSE event %q", event)
	case <-time.After(100 * time.Millisecond):
	}
	if starts := runnerStarts(t, f.startLog); starts != 2 {
		t.Errorf("runner starts = %d, want exactly one restart", starts)
	}
	if f.hub.asts["page.ktml"] == baseline {
		t.Error("restart batch did not commit the new KTML baseline")
	}
	if len(f.hub.pending) != 0 {
		t.Errorf("pending = %v, want none", f.hub.pending)
	}
}

func TestHub_GIVENRestartBatchWithInvalidKTML_WHENListened_THENRestartsAndKeepsKTMLPending(t *testing.T) {
	f := newRestartBatchFixture(t, map[string]string{
		"page.ktml": `Page { div { "old" } }`,
	})
	baseline := f.hub.asts["page.ktml"]
	writeHubSource(t, "page.ktml", `Page { div { "unterminated }`)

	f.listen <- watcher.Event{Type: watcher.EventRestart, Files: []string{"page.ktml", "main.go"}}

	if event := nextEvent(t, f.events); event != "reload" {
		t.Fatalf("SSE event = %q, want the restart's reload", event)
	}
	waitForRunnerStarts(t, f.startLog, 2)
	select {
	case batch := <-f.batches:
		t.Fatalf("generated %+v from an invalid source", batch)
	default:
	}
	if f.hub.asts["page.ktml"] != baseline {
		t.Error("invalid source replaced the baseline")
	}
	if !f.hub.pending["page.ktml"] {
		t.Errorf("pending = %v, want the invalid source retried with the next change", f.hub.pending)
	}
}

func TestHub_GIVENRestartBatchWithoutKTML_WHENListened_THENRestartsWithoutGenerating(t *testing.T) {
	f := newRestartBatchFixture(t, map[string]string{
		"page.ktml": `Page { div }`,
	})

	f.listen <- watcher.Event{Type: watcher.EventRestart, Files: []string{"main.go"}}

	if event := nextEvent(t, f.events); event != "reload" {
		t.Fatalf("SSE event = %q, want reload", event)
	}
	waitForRunnerStarts(t, f.startLog, 2)
	select {
	case batch := <-f.batches:
		t.Fatalf("generated %+v without a KTML change", batch)
	default:
	}
}
