package buildstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// publishInto publishes source with one output at path, failing the test on
// error.
func publishInto(t *testing.T, statePath string, manifest *Manifest, source, hash, path string) {
	t.Helper()
	if err := Publish(statePath, manifest, source, FileEntry{Hash: hash, PrimaryOutput: path}, []Output{{Path: path, Content: []byte(hash), Description: "primary output"}}); err != nil {
		t.Fatalf("publish %s: %v", source, err)
	}
}

func TestPublish_GIVENStaleEntryWhoseOutputDirectoryWasDeleted_WHENPublishingAnotherSource_THENSucceeds(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: make(map[string]FileEntry)}
	for _, dir := range []string{"gone", "kept"} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	publishInto(t, statePath, manifest, "gone/a.ktml", "a1", filepath.Join("gone", "a.out"))
	publishInto(t, statePath, manifest, "kept/b.ktml", "b1", filepath.Join("kept", "b.out"))
	if err := os.RemoveAll("gone"); err != nil {
		t.Fatalf("delete output directory: %v", err)
	}

	publishInto(t, statePath, manifest, "kept/b.ktml", "b2", filepath.Join("kept", "b.out"))
	if hit, err := CacheHit(statePath, manifest, "kept/b.ktml", "b2"); err != nil || !hit {
		t.Fatalf("CacheHit(kept/b.ktml) = %v, %v; want true, nil", hit, err)
	}
	reloaded, err := Load(statePath)
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if reloaded.Files["kept/b.ktml"].Hash != "b2" {
		t.Fatalf("saved hash = %q, want b2", reloaded.Files["kept/b.ktml"].Hash)
	}
}

func TestRemove_GIVENEntryWhoseOutputDirectoryWasDeleted_WHENRemoving_THENTreatsOutputAsRemoved(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: make(map[string]FileEntry)}
	if err := os.MkdirAll(filepath.Join("gone", "deep"), 0o700); err != nil {
		t.Fatalf("create output directory: %v", err)
	}
	publishInto(t, statePath, manifest, "gone/a.ktml", "a1", filepath.Join("gone", "deep", "a.out"))
	if err := os.RemoveAll("gone"); err != nil {
		t.Fatalf("delete output directory: %v", err)
	}

	if err := Remove(statePath, manifest, "gone/a.ktml"); err != nil {
		t.Fatalf("remove source with deleted output directory: %v", err)
	}
	if _, ok := manifest.Files["gone/a.ktml"]; ok {
		t.Fatalf("removed source is still recorded")
	}
	if _, err := os.Stat("gone"); !os.IsNotExist(err) {
		t.Fatalf("removal recreated the deleted directory: %v", err)
	}
}

func TestPublish_GIVENStaleEntryOutsideProject_WHENItsDirectoryIsMissing_THENStillRejects(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		setup func(t *testing.T)
	}{
		{name: "parent traversal", path: filepath.Join("..", "missing", "a.out")},
		{name: "absolute", path: filepath.Join(string(filepath.Separator), "missing-ktml-dir", "a.out")},
		{
			name: "symlink to a missing absolute directory",
			path: filepath.Join("link", "a.out"),
			setup: func(t *testing.T) {
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), "link"); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
		},
		{
			name: "symlink escaping through a missing relative directory",
			path: filepath.Join("link", "a.out"),
			setup: func(t *testing.T) {
				if err := os.Symlink(filepath.Join("..", "missing"), "link"); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			t.Chdir(project)
			if test.setup != nil {
				test.setup(t)
			}
			statePath := filepath.Join(".ktml", "state.json")
			manifest := &Manifest{Files: map[string]FileEntry{
				"a.ktml": {Hash: "a1", PrimaryOutput: test.path},
			}}

			err := Publish(statePath, manifest, "b.ktml", FileEntry{Hash: "b1", PrimaryOutput: "b.out"}, []Output{{Path: "b.out", Content: []byte("b"), Description: "primary output"}})
			if err == nil || !strings.Contains(err.Error(), "outside project root") {
				t.Fatalf("Publish error = %v, want an outside-project-root rejection", err)
			}
			if _, statErr := os.Stat("b.out"); !os.IsNotExist(statErr) {
				t.Fatalf("rejected publication wrote b.out: %v", statErr)
			}
		})
	}
}

func TestCacheHit_GIVENEntryWhoseOutputDirectoryWasDeleted_WHENChecking_THENMisses(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: make(map[string]FileEntry)}
	if err := os.Mkdir("gone", 0o700); err != nil {
		t.Fatalf("create output directory: %v", err)
	}
	publishInto(t, statePath, manifest, "gone/a.ktml", "a1", filepath.Join("gone", "a.out"))
	if err := os.RemoveAll("gone"); err != nil {
		t.Fatalf("delete output directory: %v", err)
	}

	if hit, err := CacheHit(statePath, manifest, "gone/a.ktml", "a1"); err != nil || hit {
		t.Fatalf("CacheHit = %v, %v; want a miss without error", hit, err)
	}
}
