package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcher_ReloadEvent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "index.html")
	if err := os.WriteFile(file, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	w := New(dir, []string{"*.html"}, nil, 10*time.Millisecond)
	ctx := t.Context()

	events := w.Start(ctx)

	if err := os.WriteFile(file, []byte("change"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-events:
		if ev.Type != EventReload {
			t.Errorf("expected EventReload, got %v", ev.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for reload event")
	}
}

func TestWatcher_RestartEvent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	w := New(dir, nil, []string{"*.go"}, 10*time.Millisecond)
	ctx := t.Context()

	events := w.Start(ctx)

	if err := os.WriteFile(file, []byte("change"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-events:
		if ev.Type != EventRestart {
			t.Errorf("expected EventRestart, got %v", ev.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for restart event")
	}
}

func TestWatcher_DebounceReloadEvents(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "index.html")
	file2 := filepath.Join(dir, "about.html")
	if err := os.WriteFile(file1, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	w := New(dir, []string{"*.html"}, nil, 50*time.Millisecond)
	ctx := t.Context()

	events := w.Start(ctx)

	if err := os.WriteFile(file1, []byte("change1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("change2"), 0644); err != nil {
		t.Fatal(err)
	}

	eventCount := 0
	timeout := time.After(200 * time.Millisecond)

loop:
	for {
		select {
		case ev := <-events:
			if ev.Type == EventReload {
				eventCount++
			}
		case <-timeout:
			break loop
		}
	}

	if eventCount != 1 {
		t.Errorf("expected 1 reload event, got %d", eventCount)
	}
}

func TestWatcher_EscalateToRestart(t *testing.T) {
	dir := t.TempDir()
	htmlFile := filepath.Join(dir, "index.html")
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(htmlFile, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goFile, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	w := New(dir, []string{"*.html"}, []string{"*.go"}, 50*time.Millisecond)
	ctx := t.Context()

	events := w.Start(ctx)

	if err := os.WriteFile(htmlFile, []byte("change"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goFile, []byte("change"), 0644); err != nil {
		t.Fatal(err)
	}

	eventCount := 0
	hasRestart := false
	timeout := time.After(200 * time.Millisecond)

loop:
	for {
		select {
		case ev := <-events:
			eventCount++
			if ev.Type == EventRestart {
				hasRestart = true
			}
		case <-timeout:
			break loop
		}
	}

	if eventCount != 1 {
		t.Errorf("expected 1 event, got %d", eventCount)
	}
	if !hasRestart {
		t.Error("expected restart event escalation")
	}
}

func TestWatcher_NestedDirectories(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "nested", "deep")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(subDir, "index.html")
	if err := os.WriteFile(file, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	w := New(dir, []string{"*.html"}, nil, 10*time.Millisecond)
	ctx := t.Context()

	events := w.Start(ctx)

	if err := os.WriteFile(file, []byte("change"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-events:
		if ev.Type != EventReload {
			t.Errorf("expected EventReload, got %v", ev.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for reload event from nested directory")
	}
}

func TestWatcher_GIVENGeneratedGo_WHENWritten_THENDoesNotRestartTheBackend(t *testing.T) {
	dir := t.TempDir()
	w := New(dir, []string{"*.ktml"}, []string{"*.go"}, 10*time.Millisecond)
	events := w.Start(t.Context())
	for _, name := range []string{"page_ktml.go", "page_ktml_dev.go", "model_ktmlschema.go", "components.ktml.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package page"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case ev := <-events:
		t.Fatalf("generated Go produced %+v", ev)
	case <-time.After(150 * time.Millisecond):
	}
	if err := os.WriteFile(filepath.Join(dir, "handler.go"), []byte("package page"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Type != EventRestart {
			t.Errorf("handler change = %+v, want a restart", ev)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("handler change did not restart")
	}
}

func TestWatcher_GIVENDirectoryCreatedAfterStart_WHENFileWrittenInIt_THENReloadEvent(t *testing.T) {
	dir := t.TempDir()
	w := New(dir, []string{"*.ktml"}, nil, 10*time.Millisecond)
	events := w.Start(t.Context())

	// The file is written straight after the directories, before the watcher
	// can have added them, as ktml dev --capture-stories writes a new story.
	file := filepath.Join(dir, "captured", "internal", "web", "staff.story.ktml")
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("story"), 0644); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, events, file)

	// A later file in the new directory is reported too.
	later := filepath.Join(filepath.Dir(file), "layout.story.ktml")
	if err := os.WriteFile(later, []byte("story"), 0644); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, events, later)
}

func waitForFile(t *testing.T, events <-chan Event, file string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-events:
			for _, f := range ev.Files {
				if f == file {
					return
				}
			}
		case <-deadline:
			t.Fatalf("timeout waiting for an event naming %s", file)
		}
	}
}

func TestWatcher_GIVENAssetWatch_WHENAssetsChangeWithSourcesAndRestartFiles_THENReportsAssetsApart(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"site.css", "page.ktml", "main.go", "logo.svg"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// site.css matches restart_watch too: reload_watch wins, so a stylesheet
	// moved from restart_watch to reload_watch no longer restarts.
	w := New(dir, []string{"*.ktml"}, []string{"*.go", "*.css"}, 20*time.Millisecond)
	w.SetAssetWatch([]string{"*.css", "*.svg"})
	events := w.Start(t.Context())

	if err := os.WriteFile(filepath.Join(dir, "site.css"), []byte("a{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Type != EventReload || len(ev.Files) != 0 || !onlyNamed(ev.Assets, "site.css") {
			t.Errorf("asset-only batch = %+v, want a reload with the stylesheet as its only asset", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for the asset event")
	}

	for _, name := range []string{"page.ktml", "logo.svg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case ev := <-events:
		if ev.Type != EventReload || !onlyNamed(ev.Files, "page.ktml") || !onlyNamed(ev.Assets, "logo.svg") {
			t.Errorf("mixed batch = %+v, want the source in Files and the image in Assets", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for the mixed event")
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Type != EventRestart {
			t.Errorf("restart batch = %+v, want a restart", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for the restart event")
	}
}

// onlyNamed reports whether paths name only the file name, at least once: an
// editor save may write a file more than once in a batch.
func onlyNamed(paths []string, name string) bool {
	for _, path := range paths {
		if filepath.Base(path) != name {
			return false
		}
	}
	return len(paths) > 0
}
