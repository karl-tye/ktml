package watcher

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

type EventType int

const (
	EventReload EventType = iota
	EventRestart
)

// Event is one debounced batch of changes. Files are the batch's watch and
// restart_watch matches; Type is EventRestart if any matched restart_watch.
// Assets are the reload_watch matches, which need neither generation nor a
// rebuild: pages only reload them.
type Event struct {
	Type   EventType
	Files  []string
	Assets []string
}

type Watcher struct {
	dir          string
	watch        []string
	restartWatch []string
	assetWatch   []string
	debounce     time.Duration
}

// SetAssetWatch sets the reload_watch patterns: static assets that pages
// reload without a rebuild. A file matching one is never a restart change.
func (w *Watcher) SetAssetWatch(patterns []string) {
	w.assetWatch = patterns
}

func New(dir string, watch []string, restartWatch []string, debounce time.Duration) *Watcher {
	return &Watcher{
		dir:          dir,
		watch:        watch,
		restartWatch: restartWatch,
		debounce:     debounce,
	}
}

// Start watches every directory below the watcher's root, except .git, and
// sends a debounced batch of changes once they stop for the debounce
// interval. While a batch waits for the consumer, as when the hub is still
// generating the last one, later changes join it, so file events are always
// read and the kernel's queue does not overflow. If it overflows anyway,
// dropping events, the next batch names every file the patterns match.
func (w *Watcher) Start(ctx context.Context) <-chan Event {
	out := make(chan Event)

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		close(out)
		return out
	}
	addTree(fw, w.dir)

	go func() {
		defer fw.Close()
		defer close(out)
		w.loop(ctx, fw.Events, fw.Errors, func(dir string) []string { return addTree(fw, dir) }, out)
	}()

	return out
}

// loop debounces file events into batches on out. tree watches a directory
// and everything below it and returns the files in it.
func (w *Watcher) loop(ctx context.Context, events <-chan fsnotify.Event, errs <-chan error, tree func(string) []string, out chan<- Event) {
	var timer *time.Timer
	var current, ready Event
	var changed, waiting bool
	for {
		var timerCh <-chan time.Time
		if timer != nil {
			timerCh = timer.C
		}
		var send chan<- Event
		if waiting {
			send = out
		}
		debounce := func() {
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(w.debounce)
		}

		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case send <- ready:
			ready, waiting = Event{}, false
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			names := []string{event.Name}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if skippedDir(event.Name) {
						continue
					}
					names = tree(event.Name)
				}
			}
			if w.add(&current, names) {
				changed = true
				debounce()
			}
		case <-timerCh:
			timer = nil
			if !changed {
				continue
			}
			ready = merge(ready, current)
			current, changed, waiting = Event{}, false, true
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				slog.Warn("The file watcher's event queue overflowed; regenerating every watched file", "dir", w.dir)
				if w.add(&current, tree(w.dir)) {
					changed = true
					debounce()
				}
			}
		}
	}
}

// add classifies changed files into a batch and reports whether any
// matched a pattern.
func (w *Watcher) add(batch *Event, names []string) bool {
	matched := false
	for _, name := range names {
		base := filepath.Base(name)
		// The Hub writes generated Go itself and decides whether it needs
		// a rebuild, so its outputs are never assets and never restart the
		// backend.
		if !GeneratedGo(base) && matchAny(base, w.assetWatch) {
			matched = true
			batch.Assets = append(batch.Assets, name)
			continue
		}
		matchedReload := matchAny(base, w.watch)
		matchedRestart := matchAny(base, w.restartWatch) && !GeneratedGo(base)
		if !matchedReload && !matchedRestart {
			continue
		}
		matched = true
		batch.Files = append(batch.Files, name)
		if matchedRestart {
			batch.Type = EventRestart
		}
	}
	return matched
}

// merge folds a newer batch into one still waiting for the consumer.
func merge(waiting, newer Event) Event {
	if newer.Type == EventRestart {
		waiting.Type = EventRestart
	}
	waiting.Files = append(waiting.Files, newer.Files...)
	waiting.Assets = append(waiting.Assets, newer.Assets...)
	return waiting
}

// skippedDir reports a directory the watcher never watches: .git, where a
// commit writes many files and none is a source.
func skippedDir(path string) bool {
	return filepath.Base(path) == ".git"
}

// addTree watches a directory and everything below it, except skipped
// directories, and returns the files already in it: a directory created
// after Start may have been written before it was watched, so no event
// names its files.
func addTree(fw *fsnotify.Watcher, dir string) []string {
	var files []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && skippedDir(path) {
				return filepath.SkipDir
			}
			fw.Add(path)
		} else {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// GeneratedGo reports whether a file name is Go that KTML generates: per-file
// renderers and their ktmldev variants, Host Schema records, and gohtml
// component bindings.
func GeneratedGo(name string) bool {
	for _, suffix := range []string{"_ktml.go", "_ktml_dev.go", "_ktmlschema.go", ".ktml.go"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func matchAny(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}
