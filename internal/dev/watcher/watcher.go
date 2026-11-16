package watcher

import (
	"context"
	"io/fs"
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

func (w *Watcher) Start(ctx context.Context) <-chan Event {
	out := make(chan Event)

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		close(out)
		return out
	}

	filepath.WalkDir(w.dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			fw.Add(path)
		}
		return nil
	})

	go func() {
		defer fw.Close()
		defer close(out)

		var timer *time.Timer
		var currentFiles, currentAssets []string
		currentType := EventReload

		for {
			var timerCh <-chan time.Time
			if timer != nil {
				timerCh = timer.C
			}

			select {
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				return
			case event, ok := <-fw.Events:
				if !ok {
					return
				}

				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
					continue
				}

				names := []string{event.Name}
				if event.Op&fsnotify.Create != 0 {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						names = addTree(fw, event.Name)
					}
				}
				matched := false
				for _, name := range names {
					base := filepath.Base(name)
					// The Hub writes generated Go itself and decides whether it
					// needs a rebuild, so its outputs are never assets and
					// never restart the backend.
					if !GeneratedGo(base) && matchAny(base, w.assetWatch) {
						matched = true
						currentAssets = append(currentAssets, name)
						continue
					}
					matchedReload := matchAny(base, w.watch)
					matchedRestart := matchAny(base, w.restartWatch) && !GeneratedGo(base)
					if !matchedReload && !matchedRestart {
						continue
					}
					matched = true
					currentFiles = append(currentFiles, name)
					if matchedRestart {
						currentType = EventRestart
					}
				}
				if !matched {
					continue
				}

				if timer != nil {
					timer.Stop()
				}
				timer = time.NewTimer(w.debounce)

			case <-timerCh:
				out <- Event{
					Type:   currentType,
					Files:  currentFiles,
					Assets: currentAssets,
				}
				currentFiles, currentAssets = nil, nil
				currentType = EventReload
				timer = nil

			case <-fw.Errors:
			}
		}
	}()

	return out
}

// addTree watches a directory created after Start and everything below it,
// and returns the files already in it: they may have been written before the
// directory was watched, so no event names them.
func addTree(fw *fsnotify.Watcher, dir string) []string {
	var files []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
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
