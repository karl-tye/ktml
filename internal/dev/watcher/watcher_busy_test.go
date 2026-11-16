package watcher

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// TestWatcher_GIVENBusyConsumer_WHENFilesChangeMeanwhile_THENTheyJoinTheWaitingBatch
// writes a source, leaves its batch unreceived as a hub busy generating
// does, writes a second one, and receives once: the watcher keeps reading
// file events while a batch waits, so the kernel's queue cannot overflow,
// and the waiting batch names both.
func TestWatcher_GIVENBusyConsumer_WHENFilesChangeMeanwhile_THENTheyJoinTheWaitingBatch(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.ktml")
	second := filepath.Join(dir, "second.ktml")
	events := New(dir, []string{"*.ktml"}, nil, 10*time.Millisecond).Start(t.Context())

	if err := os.WriteFile(first, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(second, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	select {
	case ev := <-events:
		if !slices.Contains(ev.Files, first) || !slices.Contains(ev.Files, second) {
			t.Errorf("batch = %+v, want both sources", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for the batch")
	}
}

// TestWatcher_GIVENEventQueueOverflow_WHENReported_THENRescansEveryWatchedFile
// reports an inotify queue overflow, which drops events such as a git stash
// restoring sources: the watcher cannot know what changed, so its next
// batch names every file the patterns match.
func TestWatcher_GIVENEventQueueOverflow_WHENReported_THENRescansEveryWatchedFile(t *testing.T) {
	dir := t.TempDir()
	files := []string{filepath.Join(dir, "a.ktml"), filepath.Join(dir, "web", "b.ktmlschema"), filepath.Join(dir, "notes.txt")}
	w := New(dir, []string{"*.ktml", "*.ktmlschema"}, nil, 10*time.Millisecond)
	fsEvents := make(chan fsnotify.Event)
	errs := make(chan error, 1)
	out := make(chan Event)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go w.loop(ctx, fsEvents, errs, func(root string) []string {
		if root != dir {
			t.Errorf("rescanned %s, want %s", root, dir)
		}
		return files
	}, out)

	errs <- fsnotify.ErrEventOverflow

	select {
	case ev := <-out:
		slices.Sort(ev.Files)
		if want := files[:2]; !slices.Equal(ev.Files, want) {
			t.Errorf("batch after overflow = %v, want %v", ev.Files, want)
		}
	case <-time.After(time.Second):
		t.Fatal("an overflow produced no batch")
	}
}

// TestWatcher_GIVENGitDirectory_WHENItChanges_THENNoBatch leaves .git
// unwatched: a commit writes many files there, none of them sources, and
// they only fill the kernel's event queue.
func TestWatcher_GIVENGitDirectory_WHENItChanges_THENNoBatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	events := New(dir, []string{"*"}, nil, 10*time.Millisecond).Start(t.Context())
	if err := os.WriteFile(filepath.Join(dir, ".git", "refs", "HEAD"), []byte("ref"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		t.Fatalf("a change under .git produced %+v", ev)
	case <-time.After(150 * time.Millisecond):
	}
}
