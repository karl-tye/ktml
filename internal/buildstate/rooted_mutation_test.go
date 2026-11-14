package buildstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildState_GIVENParentDirectoryIsSwappedToEscapingSymlink_WHENMutating_THENDoesNotMutateOutsideProject(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		run       func(t *testing.T, statePath string, manifest *Manifest) error
	}{
		{
			name:      "output write",
			operation: "output write",
			run: func(t *testing.T, statePath string, manifest *Manifest) error {
				return Publish(statePath, manifest, "page.ktml", FileEntry{Hash: "new", PrimaryOutput: "dist/page.out"}, []Output{
					{Path: "dist/page.out", Content: []byte("replace"), Description: "primary output"},
				})
			},
		},
		{
			name:      "output backup",
			operation: "output backup",
			run: func(t *testing.T, statePath string, manifest *Manifest) error {
				if err := os.WriteFile(filepath.Join("dist", "page.out"), []byte("inside"), 0o600); err != nil {
					t.Fatalf("write existing output: %v", err)
				}
				return Publish(statePath, manifest, "page.ktml", FileEntry{Hash: "new", PrimaryOutput: "dist/page.out"}, []Output{
					{Path: "dist/page.out", Content: []byte("replace"), Description: "primary output"},
				})
			},
		},
		{
			name:      "stale output deletion",
			operation: "stale output deletion",
			run: func(t *testing.T, statePath string, manifest *Manifest) error {
				if err := os.WriteFile(filepath.Join("dist", "page.out"), []byte("inside"), 0o600); err != nil {
					t.Fatalf("write stale output: %v", err)
				}
				manifest.Files["page.ktml"] = FileEntry{Hash: "old", PrimaryOutput: "dist/page.out", OutputHashes: map[string]string{"dist/page.out": Hash([]byte("inside"))}}
				return Publish(statePath, manifest, "page.ktml", FileEntry{Hash: "new", PrimaryOutput: "fresh.out"}, []Output{
					{Path: "fresh.out", Content: []byte("fresh"), Description: "primary output"},
				})
			},
		},
		{
			name:      "source output deletion",
			operation: "source output deletion",
			run: func(t *testing.T, statePath string, manifest *Manifest) error {
				if err := os.WriteFile(filepath.Join("dist", "page.out"), []byte("inside"), 0o600); err != nil {
					t.Fatalf("write source output: %v", err)
				}
				manifest.Files["page.ktml"] = FileEntry{Hash: "old", PrimaryOutput: "dist/page.out", OutputHashes: map[string]string{"dist/page.out": Hash([]byte("inside"))}}
				return Remove(statePath, manifest, "page.ktml")
			},
		},
		{
			name:      "build state save",
			operation: "build state save",
			run: func(t *testing.T, statePath string, manifest *Manifest) error {
				return Publish(statePath, manifest, "page.ktml", FileEntry{Hash: "new", PrimaryOutput: "page.out"}, []Output{
					{Path: "page.out", Content: []byte("generated"), Description: "primary output"},
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			outside := t.TempDir()
			t.Chdir(project)
			if err := os.Mkdir("dist", 0o700); err != nil {
				t.Fatalf("create output directory: %v", err)
			}
			if err := os.Mkdir(".ktml", 0o700); err != nil {
				t.Fatalf("create state directory: %v", err)
			}
			for _, name := range []string{"page.out", "state.json"} {
				if err := os.WriteFile(filepath.Join(outside, name), []byte("preserve"), 0o600); err != nil {
					t.Fatalf("write external %s: %v", name, err)
				}
			}

			swapped := false
			testBeforeRootMutation = func(operation, _ string) {
				if swapped || operation != test.operation {
					return
				}
				swapped = true
				parent := "dist"
				if operation == "build state save" {
					parent = ".ktml"
				}
				if err := os.Rename(parent, parent+"-inside"); err != nil {
					t.Fatalf("move validated parent: %v", err)
				}
				if err := os.Symlink(outside, parent); err != nil {
					t.Fatalf("replace parent with escaping symlink: %v", err)
				}
			}
			t.Cleanup(func() { testBeforeRootMutation = nil })

			manifest := &Manifest{Files: make(map[string]FileEntry)}
			err := test.run(t, filepath.Join(".ktml", "state.json"), manifest)

			if !swapped {
				t.Fatalf("production path did not reach instrumented %s", test.operation)
			}
			if err == nil || !strings.Contains(err.Error(), "escapes from parent") {
				t.Fatalf("mutation error = %v, want rooted-filesystem rejection", err)
			}
			for _, name := range []string{"page.out", "state.json"} {
				content, readErr := os.ReadFile(filepath.Join(outside, name))
				if readErr != nil || string(content) != "preserve" {
					t.Fatalf("external %s = %q, %v; want preserved", name, content, readErr)
				}
			}
		})
	}
}
