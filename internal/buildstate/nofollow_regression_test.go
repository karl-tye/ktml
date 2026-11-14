package buildstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildState_GIVENStateParentIsInRootSymlink_WHENSavingOrPublishing_THENRejectsWithoutVictimMutation(t *testing.T) {
	operations := []struct {
		name string
		run  func(string, *Manifest) error
	}{
		{
			name: "save",
			run: func(statePath string, manifest *Manifest) error {
				return Save(statePath, manifest)
			},
		},
		{
			name: "publish",
			run: func(statePath string, manifest *Manifest) error {
				return Publish(statePath, manifest, "page.ktml", FileEntry{
					Hash:          "new",
					PrimaryOutput: "page.out",
				}, []Output{{Path: "page.out", Content: []byte("generated"), Description: "primary output"}})
			},
		},
	}
	timings := []string{"existing", "raced"}

	for _, operation := range operations {
		for _, timing := range timings {
			t.Run(operation.name+"/"+timing, func(t *testing.T) {
				project := t.TempDir()
				victim := filepath.Join(project, "victim")
				if err := os.Mkdir(victim, 0o700); err != nil {
					t.Fatalf("create victim directory: %v", err)
				}
				victimState := filepath.Join(victim, "state.json")
				if err := os.WriteFile(victimState, []byte("preserve"), 0o600); err != nil {
					t.Fatalf("write victim state: %v", err)
				}
				stateParent := filepath.Join(project, ".ktml")
				raced := timing == "existing"
				if raced {
					if err := os.Symlink("victim", stateParent); err != nil {
						t.Fatalf("create state parent symlink: %v", err)
					}
				} else {
					testBeforeRootMutation = func(operation, path string) {
						if operation != "project root acquired" || path != project {
							return
						}
						raced = true
						if err := os.Symlink("victim", stateParent); err != nil {
							t.Fatalf("race state parent with symlink: %v", err)
						}
					}
					t.Cleanup(func() { testBeforeRootMutation = nil })
				}

				manifest := &Manifest{Files: map[string]FileEntry{"page.ktml": {Hash: "old"}}}
				err := operation.run(filepath.Join(stateParent, "state.json"), manifest)

				if !raced {
					t.Fatal("operation did not reach the state-parent race boundary")
				}
				if err == nil {
					t.Fatal("operation succeeded through an in-root state-parent symlink")
				}
				assertFileContent(t, victimState, "preserve")
				if _, statErr := os.Stat(filepath.Join(project, "page.out")); !os.IsNotExist(statErr) {
					t.Fatalf("output was mutated despite rejected state parent: %v", statErr)
				}
			})
		}
	}
}

func TestBuildState_GIVENCreatedStateTempDirectoryIsReplacedWithSymlink_WHENAcquiringIt_THENRejectsWithoutVictimMutationOrAttackerCleanup(t *testing.T) {
	operations := []struct {
		name string
		run  func(string, *Manifest) error
	}{
		{
			name: "save",
			run: func(statePath string, manifest *Manifest) error {
				return Save(statePath, manifest)
			},
		},
		{
			name: "publish",
			run: func(statePath string, manifest *Manifest) error {
				return Publish(statePath, manifest, "page.ktml", FileEntry{
					Hash:          "new",
					PrimaryOutput: "page.out",
				}, []Output{{Path: "page.out", Content: []byte("generated"), Description: "primary output"}})
			},
		},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			project := t.TempDir()
			victim := filepath.Join(project, "victim")
			if err := os.Mkdir(victim, 0o700); err != nil {
				t.Fatalf("create victim directory: %v", err)
			}
			victimState := filepath.Join(victim, "state.json")
			if err := os.WriteFile(victimState, []byte("preserve"), 0o600); err != nil {
				t.Fatalf("write victim state: %v", err)
			}

			var replacedPath string
			testBeforeRootMutation = func(boundary, path string) {
				if boundary != "build state temporary parent created" {
					return
				}
				replacedPath = filepath.Join(project, path)
				if err := os.Remove(replacedPath); err != nil {
					t.Fatalf("remove created state temp directory: %v", err)
				}
				if err := os.Symlink("victim", replacedPath); err != nil {
					t.Fatalf("replace state temp directory with symlink: %v", err)
				}
			}
			t.Cleanup(func() { testBeforeRootMutation = nil })

			manifest := &Manifest{Files: map[string]FileEntry{"page.ktml": {Hash: "old"}}}
			err := operation.run(filepath.Join(project, ".ktml", "state.json"), manifest)

			if replacedPath == "" {
				t.Fatal("operation did not reach the state temp-directory acquisition boundary")
			}
			if err == nil {
				t.Fatal("operation succeeded through a replaced state temp directory")
			}
			assertFileContent(t, victimState, "preserve")
			if info, statErr := os.Lstat(replacedPath); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("attacker symlink was cleaned up: mode=%v, error=%v", info, statErr)
			}
			if _, statErr := os.Stat(filepath.Join(project, "page.out")); !os.IsNotExist(statErr) {
				t.Fatalf("output was not rolled back after rejected state parent: %v", statErr)
			}
			if got := manifest.Files["page.ktml"].Hash; got != "old" {
				t.Fatalf("manifest hash = %q, want unchanged old hash", got)
			}
		})
	}
}

func TestPublish_GIVENAtomicWriteTempPathIsRecreatedAfterRename_WHENPublishing_THENLeavesReplacementAndCommittedOutput(t *testing.T) {
	tests := []struct {
		name   string
		create func(string) error
	}{
		{
			name: "file",
			create: func(path string) error {
				return os.WriteFile(path, []byte("attacker"), 0o600)
			},
		},
		{
			name: "non-empty directory",
			create: func(path string) error {
				if err := os.Mkdir(path, 0o700); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(path, "owned"), []byte("attacker"), 0o600)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			var recreatedPath string
			testBeforeRootMutation = func(operation, path string) {
				if operation != "atomic write committed" || !strings.HasPrefix(filepath.Base(path), ".ktml-output-") {
					return
				}
				recreatedPath = filepath.Join(project, path)
				if err := test.create(recreatedPath); err != nil {
					t.Fatalf("recreate committed temp path: %v", err)
				}
			}
			t.Cleanup(func() { testBeforeRootMutation = nil })

			manifest := &Manifest{Files: make(map[string]FileEntry)}
			err := Publish(filepath.Join(project, ".ktml", "state.json"), manifest, "page.ktml", FileEntry{
				Hash:          "new",
				PrimaryOutput: "page.out",
			}, []Output{{Path: "page.out", Content: []byte("generated"), Description: "primary output"}})

			if recreatedPath == "" {
				t.Fatal("Publish did not reach the committed atomic-write boundary")
			}
			if err != nil {
				t.Fatalf("Publish() error = %v", err)
			}
			assertFileContent(t, filepath.Join(project, "page.out"), "generated")
			if _, statErr := os.Lstat(recreatedPath); statErr != nil {
				t.Fatalf("recreated temp path was removed: %v", statErr)
			}
			if got := manifest.Files["page.ktml"].Hash; got != "new" {
				t.Fatalf("manifest hash = %q, want committed new hash", got)
			}
		})
	}
}
