package dev

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/buildstate"
	"github.com/karl-tye/ktml/internal/dev/livereload"
	"github.com/karl-tye/ktml/internal/dev/runner"
	"github.com/karl-tye/ktml/internal/dev/watcher"
)

func TestHub_GIVENTwoCompatibleFiles_WHENReloadedTogether_THENPublishesOneFullReloadAndCommitsBoth(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	first := filepath.Join(dir, "first.ktml")
	second := filepath.Join(dir, "second.ktml")
	for path, source := range map[string]string{
		first:  `First { div { "old first" } }`,
		second: `Second { div { "old second" } }`,
	} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(target.Close)
	liveReload := livereload.NewServer(target.URL)
	events, closeSSE := openHubSSE(t, liveReload)
	t.Cleanup(closeSSE)

	startLog := filepath.Join(dir, "runner-starts")
	process := runner.New([]string{"sh", "-c", `echo started >> "$1"; exec sleep 30`, "runner", startLog})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := process.Start(ctx); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(func() { _ = process.Stop() })
	waitForRunnerStarts(t, startLog, 1)

	generated := make(chan string, 4)
	hub := NewHub(liveReload, process, 0, func(batch Batch) error {
		for _, path := range batch.Generate {
			generated <- filepath.Clean(path)
		}
		return nil
	})
	for _, path := range []string{first, second} {
		if err := hub.TrackFile(path); err != nil {
			t.Fatalf("TrackFile(%s) error = %v", path, err)
		}
	}
	initialFirst := hub.asts[filepath.Clean(first)]
	initialSecond := hub.asts[filepath.Clean(second)]
	if err := os.WriteFile(first, []byte(`First { div { "new first" } }`), 0o600); err != nil {
		t.Fatalf("write first change: %v", err)
	}
	if err := os.WriteFile(second, []byte(`Second { div { "new second" } }`), 0o600); err != nil {
		t.Fatalf("write second change: %v", err)
	}

	watchEvents := make(chan watcher.Event, 1)
	done := make(chan struct{})
	go func() {
		hub.Listen(ctx, watchEvents)
		close(done)
	}()
	watchEvents <- watcher.Event{Type: watcher.EventReload, Files: []string{first, second}}
	select {
	case event := <-events:
		if event != "reload" {
			t.Fatalf("SSE event = %q, want reload", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("full reload was not published")
	}
	waitForRunnerStarts(t, startLog, 2)

	wantGenerated := map[string]bool{filepath.Clean(first): false, filepath.Clean(second): false}
	for range 2 {
		select {
		case path := <-generated:
			if _, ok := wantGenerated[path]; ok {
				wantGenerated[path] = true
			}
		case <-time.After(time.Second):
			t.Fatal("both changed files were not generated")
		}
	}
	for path, found := range wantGenerated {
		if !found {
			t.Errorf("generation did not include %s", path)
		}
	}
	cancel()
	<-done
	if got := hub.asts[filepath.Clean(first)]; got == initialFirst {
		t.Error("first AST baseline was not committed after full reload")
	}
	if got := hub.asts[filepath.Clean(second)]; got == initialSecond {
		t.Error("second AST baseline was not committed after full reload")
	}
}

func TestHub_GIVENMultiFileGenerationFailure_WHENPreparingFullReload_THENCommitsNeitherBaselineAndPublishesNothing(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.ktml")
	second := filepath.Join(dir, "second.ktml")
	for path, source := range map[string]string{
		first:  `First { div { "old first" } }`,
		second: `Second { div { "old second" } }`,
	} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(target.Close)
	liveReload := livereload.NewServer(target.URL)
	events, closeSSE := openHubSSE(t, liveReload)
	t.Cleanup(closeSSE)

	startLog := filepath.Join(dir, "runner-starts")
	process := runner.New([]string{"sh", "-c", `echo started >> "$1"; exec sleep 30`, "runner", startLog})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := process.Start(ctx); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(func() { _ = process.Stop() })
	waitForRunnerStarts(t, startLog, 1)

	generated := make(chan string, 2)
	hub := NewHub(liveReload, process, 0, func(batch Batch) error {
		for _, path := range batch.Generate {
			generated <- filepath.Clean(path)
			if filepath.Clean(path) == filepath.Clean(second) {
				return errors.New("second generation failed")
			}
		}
		return nil
	})
	for _, path := range []string{first, second} {
		if err := hub.TrackFile(path); err != nil {
			t.Fatalf("TrackFile(%s) error = %v", path, err)
		}
	}
	initialFirst := hub.asts[filepath.Clean(first)]
	initialSecond := hub.asts[filepath.Clean(second)]
	if err := os.WriteFile(first, []byte(`First { div { "new first" } }`), 0o600); err != nil {
		t.Fatalf("write first change: %v", err)
	}
	if err := os.WriteFile(second, []byte(`Second { div { "new second" } }`), 0o600); err != nil {
		t.Fatalf("write second change: %v", err)
	}

	watchEvents := make(chan watcher.Event)
	done := make(chan struct{})
	go func() {
		hub.Listen(ctx, watchEvents)
		close(done)
	}()
	watchEvents <- watcher.Event{Type: watcher.EventReload, Files: []string{first, second}}
	for range 2 {
		select {
		case <-generated:
		case <-time.After(time.Second):
			t.Fatal("both changed files were not prepared")
		}
	}
	watchEvents <- watcher.Event{Type: watcher.EventType(-1)}
	cancel()
	<-done

	if starts := runnerStarts(t, startLog); starts != 1 {
		t.Errorf("runner starts = %d, want 1 without restart", starts)
	}
	select {
	case event := <-events:
		t.Errorf("published SSE event after batch generation failure: %q", event)
	default:
	}
	if got := hub.asts[filepath.Clean(first)]; got != initialFirst {
		t.Error("first baseline advanced after batch generation failure")
	}
	if got := hub.asts[filepath.Clean(second)]; got != initialSecond {
		t.Error("second baseline advanced after batch generation failure")
	}
}

func TestHub_GIVENDeletedFileBeforeValidEdit_WHENReloadedTogether_THENGeneratesSurvivingEditBeforePublishing(t *testing.T) {
	dir := t.TempDir()
	deleted := filepath.Join(dir, "deleted.ktml")
	changed := filepath.Join(dir, "changed.ktml")
	for path, source := range map[string]string{
		deleted: `Deleted { div }`,
		changed: `Changed { div { "old" } }`,
	} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(target.Close)
	liveReload := livereload.NewServer(target.URL)
	events, closeSSE := openHubSSE(t, liveReload)
	t.Cleanup(closeSSE)

	startLog := filepath.Join(dir, "runner-starts")
	process := runner.New([]string{"sh", "-c", `echo started >> "$1"; exec sleep 30`, "runner", startLog})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := process.Start(ctx); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(func() { _ = process.Stop() })
	waitForRunnerStarts(t, startLog, 1)

	generated := make(chan string, 2)
	hub := NewHub(liveReload, process, 0, func(batch Batch) error {
		for _, path := range batch.Generate {
			generated <- filepath.Clean(path)
		}
		return nil
	})
	for _, path := range []string{deleted, changed} {
		if err := hub.TrackFile(path); err != nil {
			t.Fatalf("TrackFile(%s) error = %v", path, err)
		}
	}
	initialChanged := hub.asts[filepath.Clean(changed)]
	if err := os.Remove(deleted); err != nil {
		t.Fatalf("remove source: %v", err)
	}
	if err := os.WriteFile(changed, []byte(`Changed { div { "new" } }`), 0o600); err != nil {
		t.Fatalf("write surviving change: %v", err)
	}

	watchEvents := make(chan watcher.Event)
	done := make(chan struct{})
	go func() {
		hub.Listen(ctx, watchEvents)
		close(done)
	}()
	watchEvents <- watcher.Event{Type: watcher.EventReload, Files: []string{deleted, changed}}
	select {
	case path := <-generated:
		if path != filepath.Clean(changed) {
			t.Fatalf("generated path = %s, want %s", path, changed)
		}
	case <-time.After(time.Second):
		t.Fatal("surviving changed file was not generated")
	}
	select {
	case event := <-events:
		if event != "reload" {
			t.Fatalf("SSE event = %q, want reload", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("full reload was not published")
	}
	waitForRunnerStarts(t, startLog, 2)
	watchEvents <- watcher.Event{Type: watcher.EventType(-1)}
	cancel()
	<-done
	if _, ok := hub.asts[filepath.Clean(deleted)]; ok {
		t.Error("deleted source AST was retained after full reload")
	}
	if got := hub.asts[filepath.Clean(changed)]; got == initialChanged {
		t.Error("surviving changed source AST was not committed after full reload")
	}
}

func TestHub_GIVENImportAndCompatibleEdit_WHENReloadedTogether_THENPublishesFullReloadAndCommitsBaseline(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "page.ktml")
	for path, source := range map[string]string{
		filepath.Join(dir, "old.ktml"): `Shared { span }`,
		filepath.Join(dir, "new.ktml"): `Shared { div }`,
		root:                           "import { Shared } from \"old\"\nCard { div { \"old\" } }",
	} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(target.Close)
	liveReload := livereload.NewServer(target.URL)
	events, closeSSE := openHubSSE(t, liveReload)
	t.Cleanup(closeSSE)

	generated := make(chan string, 1)
	hub := NewHub(liveReload, nil, 0, func(batch Batch) error {
		for _, path := range batch.Generate {
			generated <- filepath.Clean(path)
		}
		return nil
	})
	if err := hub.TrackFile(root); err != nil {
		t.Fatalf("TrackFile() error = %v", err)
	}
	initialDocument := hub.asts[filepath.Clean(root)]
	if err := os.WriteFile(root, []byte("import { Shared } from \"new\"\nCard { div { \"new\" } }"), 0o600); err != nil {
		t.Fatalf("write import and compatible edit: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	watchEvents := make(chan watcher.Event)
	done := make(chan struct{})
	go func() {
		hub.Listen(ctx, watchEvents)
		close(done)
	}()
	watchEvents <- watcher.Event{Type: watcher.EventReload, Files: []string{root}}
	select {
	case path := <-generated:
		if path != filepath.Clean(root) {
			t.Fatalf("generated path = %s, want %s", path, root)
		}
	case <-time.After(time.Second):
		t.Fatal("changed import file was not generated")
	}
	select {
	case event := <-events:
		if event != "reload" {
			t.Fatalf("SSE event = %q, want reload", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("full reload was not published")
	}
	watchEvents <- watcher.Event{Type: watcher.EventType(-1)}
	cancel()
	<-done
	if got := hub.asts[filepath.Clean(root)]; got == initialDocument {
		t.Error("import change AST was not committed after full reload")
	}
}

func TestHub_GIVENRunnerCannotPrepareRestart_WHENRestartEventArrives_THENKeepsRunnerAndPublishesNothing(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "server")
	startLog := filepath.Join(dir, "runner-starts")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\necho started >> \"$1\"\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatalf("write runner executable: %v", err)
	}
	process := runner.New([]string{executable, startLog})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := process.Start(ctx); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(func() { _ = process.Stop() })
	waitForRunnerStarts(t, startLog, 1)
	if err := os.Remove(executable); err != nil {
		t.Fatalf("remove runner executable: %v", err)
	}

	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(target.Close)
	liveReload := livereload.NewServer(target.URL)
	events, closeSSE := openHubSSE(t, liveReload)
	t.Cleanup(closeSSE)
	hub := NewHub(liveReload, process, 0, func(Batch) error { return nil })
	watchEvents := make(chan watcher.Event)
	done := make(chan struct{})
	go func() {
		hub.Listen(ctx, watchEvents)
		close(done)
	}()
	watchEvents <- watcher.Event{Type: watcher.EventRestart}
	watchEvents <- watcher.Event{Type: watcher.EventType(-1)}
	cancel()
	<-done

	if !process.IsRunning() {
		t.Error("failed restart stopped the last-valid runner")
	}
	if starts := runnerStarts(t, startLog); starts != 1 {
		t.Errorf("runner starts = %d, want 1", starts)
	}
	select {
	case event := <-events:
		t.Errorf("published SSE event after restart failure: %q", event)
	default:
	}
}

func TestHub_GIVENBackendNotReadyAfterRestart_WHENPreparingFullReload_THENPublishesNothingAndPreservesBaseline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "components.ktml")
	if err := os.WriteFile(path, []byte(`First { div { "old first" } } Second { div { "old second" } }`), 0o600); err != nil {
		t.Fatalf("write initial source: %v", err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	targetURL := target.URL
	target.Close()
	liveReload := livereload.NewServer(targetURL)
	events, closeSSE := openHubSSE(t, liveReload)
	t.Cleanup(closeSSE)

	startLog := filepath.Join(dir, "runner-starts")
	process := runner.New([]string{"sh", "-c", `echo started >> "$1"; exec sleep 30`, "runner", startLog})
	runnerCtx, stopRunner := context.WithCancel(context.Background())
	t.Cleanup(stopRunner)
	if err := process.Start(runnerCtx); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(func() { _ = process.Stop() })
	waitForRunnerStarts(t, startLog, 1)

	hub := NewHub(liveReload, process, 0, func(Batch) error { return nil })
	if err := hub.TrackFile(path); err != nil {
		t.Fatalf("TrackFile() error = %v", err)
	}
	cleanPath := filepath.Clean(path)
	initialDocument := hub.asts[cleanPath]
	if err := os.WriteFile(path, []byte(`First { div { "new first" } } Second { div { "new second" } }`), 0o600); err != nil {
		t.Fatalf("write changed source: %v", err)
	}

	listenCtx, stopListening := context.WithTimeout(context.Background(), 300*time.Millisecond)
	t.Cleanup(stopListening)
	watchEvents := make(chan watcher.Event, 1)
	done := make(chan struct{})
	go func() {
		hub.Listen(listenCtx, watchEvents)
		close(done)
	}()
	watchEvents <- watcher.Event{Type: watcher.EventReload, Files: []string{path}}
	waitForRunnerStarts(t, startLog, 2)
	<-done

	select {
	case event := <-events:
		t.Errorf("published SSE event before backend readiness: %q", event)
	default:
	}
	if got := hub.asts[cleanPath]; got != initialDocument {
		t.Error("readiness failure replaced the last published AST")
	}
}

func TestHub_GIVENGenerationFailure_WHENPreparingCompatibleEdit_THENDoesNotRestartOrPublish(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "components.ktml")
	if err := os.WriteFile(path, []byte(`Card { div { "old" } }`), 0o600); err != nil {
		t.Fatalf("write initial source: %v", err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(target.Close)
	liveReload := livereload.NewServer(target.URL)
	events, closeSSE := openHubSSE(t, liveReload)
	t.Cleanup(closeSSE)

	startLog := filepath.Join(dir, "runner-starts")
	process := runner.New([]string{"sh", "-c", `echo started >> "$1"; exec sleep 30`, "runner", startLog})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := process.Start(ctx); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(func() { _ = process.Stop() })
	waitForRunnerStarts(t, startLog, 1)

	generated := make(chan struct{}, 2)
	hub := NewHub(liveReload, process, 0, func(Batch) error {
		generated <- struct{}{}
		return errors.New("generation failed")
	})
	if err := hub.TrackFile(path); err != nil {
		t.Fatalf("TrackFile() error = %v", err)
	}
	cleanPath := filepath.Clean(path)
	initialDocument := hub.asts[cleanPath]
	if err := os.WriteFile(path, []byte(`Card { div { "new" } }`), 0o600); err != nil {
		t.Fatalf("write changed source: %v", err)
	}

	watchEvents := make(chan watcher.Event)
	done := make(chan struct{})
	go func() {
		hub.Listen(ctx, watchEvents)
		close(done)
	}()
	watchEvents <- watcher.Event{Type: watcher.EventReload, Files: []string{path}}
	select {
	case <-generated:
	case <-time.After(time.Second):
		t.Fatal("generation was not attempted")
	}
	watchEvents <- watcher.Event{Type: watcher.EventType(-1)}
	cancel()
	<-done

	if starts := runnerStarts(t, startLog); starts != 1 {
		t.Errorf("runner starts = %d, want 1 without restart", starts)
	}
	select {
	case event := <-events:
		t.Errorf("published SSE event after generation failure: %q", event)
	default:
	}
	if got := hub.asts[cleanPath]; got != initialDocument {
		t.Error("generation failure replaced the last published AST")
	}
}

func TestHub_GIVENInvalidAndCompatibleChanges_WHENReloadedTogether_THENPreparesNothingAndPreservesBaselines(t *testing.T) {
	dir := t.TempDir()
	invalid := filepath.Join(dir, "invalid.ktml")
	compatible := filepath.Join(dir, "compatible.ktml")
	for path, source := range map[string]string{
		invalid:    `Invalid { div { "old invalid" } }`,
		compatible: `Compatible { div { "old compatible" } }`,
	} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	generated := make(chan string, 1)
	hub := NewHub(nil, nil, 0, func(batch Batch) error {
		for _, path := range batch.Generate {
			generated <- filepath.Clean(path)
		}
		return nil
	})
	for _, path := range []string{invalid, compatible} {
		if err := hub.TrackFile(path); err != nil {
			t.Fatalf("TrackFile(%s) error = %v", path, err)
		}
	}
	initialInvalid := hub.asts[filepath.Clean(invalid)]
	initialCompatible := hub.asts[filepath.Clean(compatible)]
	if err := os.WriteFile(invalid, []byte(`Invalid {`), 0o600); err != nil {
		t.Fatalf("write invalid change: %v", err)
	}
	if err := os.WriteFile(compatible, []byte(`Compatible { div { "new compatible" } }`), 0o600); err != nil {
		t.Fatalf("write compatible change: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan watcher.Event)
	done := make(chan struct{})
	go func() {
		hub.Listen(ctx, events)
		close(done)
	}()
	events <- watcher.Event{Type: watcher.EventReload, Files: []string{invalid, compatible}}
	cancel()
	<-done

	select {
	case path := <-generated:
		t.Errorf("generated %s despite invalid batch member", path)
	default:
	}
	if got := hub.asts[filepath.Clean(invalid)]; got != initialInvalid {
		t.Error("invalid file baseline changed")
	}
	if got := hub.asts[filepath.Clean(compatible)]; got != initialCompatible {
		t.Error("compatible file baseline changed despite invalid batch member")
	}
}

func TestHub_GIVENDeletionAndGenerationFailure_WHENReloadedTogether_THENPreservesPublishedFilesStateAndBaselines(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	deleted := filepath.Join(dir, "deleted.ktml")
	changed := filepath.Join(dir, "changed.ktml")
	for path, source := range map[string]string{
		deleted: `Deleted { div }`,
		changed: `First { div { "old first" } } Second { div { "old second" } }`,
	} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	for path, content := range map[string]string{
		"deleted.html": "published primary",
		"deleted.go":   "published auxiliary",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &buildstate.Manifest{Files: map[string]buildstate.FileEntry{
		deleted: {
			Hash:             "published hash",
			PrimaryOutput:    "deleted.html",
			AuxiliaryOutputs: []string{"deleted.go"},
		},
	}}
	if err := buildstate.Save(statePath, manifest); err != nil {
		t.Fatalf("save initial state: %v", err)
	}
	initialState := mustReadHubFile(t, statePath)

	prepared := make(chan struct{}, 1)
	hub := NewHub(nil, nil, 0, func(Batch) error {
		prepared <- struct{}{}
		return errors.New("generation failed")
	})
	for _, path := range []string{deleted, changed} {
		if err := hub.TrackFile(path); err != nil {
			t.Fatalf("TrackFile(%s) error = %v", path, err)
		}
	}
	initialDeleted := hub.asts[filepath.Clean(deleted)]
	initialChanged := hub.asts[filepath.Clean(changed)]
	if err := os.Remove(deleted); err != nil {
		t.Fatalf("remove source: %v", err)
	}
	if err := os.WriteFile(changed, []byte(`First { div { "new first" } } Second { div { "new second" } }`), 0o600); err != nil {
		t.Fatalf("write changed source: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan watcher.Event)
	done := make(chan struct{})
	go func() {
		hub.Listen(ctx, events)
		close(done)
	}()
	events <- watcher.Event{Type: watcher.EventReload, Files: []string{deleted, changed}}
	select {
	case <-prepared:
	case <-time.After(time.Second):
		t.Fatal("surviving file generation was not attempted")
	}
	cancel()
	<-done

	if got := hub.asts[filepath.Clean(deleted)]; got != initialDeleted {
		t.Error("deleted source baseline was removed before batch publication")
	}
	if got := hub.asts[filepath.Clean(changed)]; got != initialChanged {
		t.Error("changed source baseline advanced after generation failure")
	}
	for path, want := range map[string]string{
		"deleted.html": "published primary",
		"deleted.go":   "published auxiliary",
	} {
		if got := string(mustReadHubFile(t, path)); got != want {
			t.Errorf("%s = %q, want preserved %q", path, got, want)
		}
	}
	if got := string(mustReadHubFile(t, statePath)); got != string(initialState) {
		t.Errorf("build state changed before batch publication:\n%s", got)
	}
}

func mustReadHubFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return content
}

func openHubSSE(t *testing.T, liveReload *livereload.Server) (<-chan string, func()) {
	t.Helper()
	server := httptest.NewServer(liveReload)
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/__ktml/livereload", nil)
	if err != nil {
		server.Close()
		cancel()
		t.Fatalf("create SSE request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		server.Close()
		cancel()
		t.Fatalf("connect SSE: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for liveReload.ActiveClients() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if liveReload.ActiveClients() != 1 {
		_ = response.Body.Close()
		server.Close()
		cancel()
		t.Fatal("SSE client was not registered")
	}
	events := make(chan string, 4)
	go func() {
		defer close(events)
		reader := bufio.NewReader(response.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "event: ") {
				event := strings.TrimSpace(strings.TrimPrefix(line, "event: "))
				// The error overlay publishes no page change.
				if event == "diagnostic" || event == "clear" {
					continue
				}
				events <- event
			}
		}
	}()
	return events, func() {
		cancel()
		_ = response.Body.Close()
		server.Close()
	}
}

func waitForRunnerStarts(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(path)
		if err == nil && strings.Count(string(content), "started") >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runner did not record %d starts", want)
}

func runnerStarts(t *testing.T, path string) int {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read runner starts: %v", err)
	}
	return strings.Count(string(content), "started")
}
