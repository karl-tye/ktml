package buildstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublish_GIVENProjectPathIsSwappedAfterRootAcquisition_WHENPublishing_THENDoesNotWriteToReplacement(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	replacement := filepath.Join(parent, "replacement")
	for _, dir := range []string{project, replacement} {
		if err := os.MkdirAll(filepath.Join(dir, ".ktml"), 0o700); err != nil {
			t.Fatalf("create project layout: %v", err)
		}
	}
	statePath := filepath.Join(project, ".ktml", "state.json")
	swapped := false
	probeRooted := false
	testBeforeRootMutation = func(operation, path string) {
		if operation == "filesystem case probe created" {
			if _, err := os.Stat(filepath.Join(project+"-acquired", path)); err != nil {
				t.Fatalf("case probe was not created through acquired root: %v", err)
			}
			if _, err := os.Stat(filepath.Join(replacement, path)); !os.IsNotExist(err) {
				t.Fatalf("case probe reached replacement root: %v", err)
			}
			probeRooted = true
			return
		}
		if !swapped && operation == "project root acquired" && path == project {
			swapped = true
			if err := os.Rename(project, project+"-acquired"); err != nil {
				t.Fatalf("move acquired project: %v", err)
			}
			if err := os.Symlink(replacement, project); err != nil {
				t.Fatalf("replace project path: %v", err)
			}
		}
	}
	t.Cleanup(func() { testBeforeRootMutation = nil })
	manifest := &Manifest{Files: make(map[string]FileEntry)}

	err := Publish(statePath, manifest, "page.ktml", FileEntry{
		Hash:          "hash",
		PrimaryOutput: "page.out",
	}, []Output{{Path: "page.out", Content: []byte("generated"), Description: "primary output"}})

	if !swapped {
		t.Fatal("Publish did not reach the project root acquisition boundary")
	}
	if !probeRooted {
		t.Fatal("Publish did not probe case semantics through the acquired project root")
	}
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	for _, path := range []string{"page.out", filepath.Join(".ktml", "state.json")} {
		if _, statErr := os.Stat(filepath.Join(replacement, path)); !os.IsNotExist(statErr) {
			t.Errorf("replacement path %s was mutated: %v", path, statErr)
		}
		if _, statErr := os.Stat(filepath.Join(project+"-acquired", path)); statErr != nil {
			t.Errorf("acquired project path %s was not published: %v", path, statErr)
		}
	}
}

func TestBuildState_GIVENProjectPathIsSwappedAfterRootAcquisition_WHENMutating_THENUsesAcquiredProject(t *testing.T) {
	tests := []struct {
		name string
		run  func(project string) error
		want func(t *testing.T, acquired, replacement string)
	}{
		{
			name: "direct output write",
			run: func(project string) error {
				return WriteOutput(project, nil, Output{Path: "page.out", Content: []byte("generated"), Description: "auxiliary output"})
			},
			want: func(t *testing.T, acquired, replacement string) {
				assertFileContent(t, filepath.Join(acquired, "page.out"), "generated")
				assertFileContent(t, filepath.Join(replacement, "page.out"), "preserve")
			},
		},
		{
			name: "source removal",
			run: func(project string) error {
				return Remove(filepath.Join(project, ".ktml", "state.json"), &Manifest{Files: map[string]FileEntry{
					"page.ktml": {Hash: "hash", PrimaryOutput: "page.out", OutputHashes: map[string]string{"page.out": Hash([]byte("preserve"))}},
				}}, "page.ktml")
			},
			want: func(t *testing.T, acquired, replacement string) {
				if _, err := os.Stat(filepath.Join(acquired, "page.out")); !os.IsNotExist(err) {
					t.Fatalf("acquired project output was not removed: %v", err)
				}
				assertFileContent(t, filepath.Join(replacement, "page.out"), "preserve")
			},
		},
		{
			name: "build state save",
			run: func(project string) error {
				return Save(filepath.Join(project, ".ktml", "state.json"), &Manifest{Files: map[string]FileEntry{
					"page.ktml": {Hash: "new"},
				}})
			},
			want: func(t *testing.T, acquired, replacement string) {
				data, err := os.ReadFile(filepath.Join(acquired, ".ktml", "state.json"))
				if err != nil {
					t.Fatalf("read acquired Build State: %v", err)
				}
				var manifest Manifest
				if err := json.Unmarshal(data, &manifest); err != nil || manifest.Files["page.ktml"].Hash != "new" {
					t.Fatalf("acquired Build State = %s, %v", data, err)
				}
				assertFileContent(t, filepath.Join(replacement, ".ktml", "state.json"), "preserve")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			project := filepath.Join(parent, "project")
			replacement := filepath.Join(parent, "replacement")
			for _, dir := range []string{project, replacement} {
				if err := os.MkdirAll(filepath.Join(dir, ".ktml"), 0o700); err != nil {
					t.Fatalf("create project layout: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, "page.out"), []byte("preserve"), 0o600); err != nil {
					t.Fatalf("write output fixture: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".ktml", "state.json"), []byte("preserve"), 0o600); err != nil {
					t.Fatalf("write state fixture: %v", err)
				}
			}
			swapped := false
			testBeforeRootMutation = func(operation, path string) {
				if swapped || operation != "project root acquired" || path != project {
					return
				}
				swapped = true
				if err := os.Rename(project, project+"-acquired"); err != nil {
					t.Fatalf("move acquired project: %v", err)
				}
				if err := os.Symlink(replacement, project); err != nil {
					t.Fatalf("replace project path: %v", err)
				}
			}
			t.Cleanup(func() { testBeforeRootMutation = nil })

			err := test.run(project)

			if !swapped {
				t.Fatal("operation did not reach the project root acquisition boundary")
			}
			if err != nil {
				t.Fatalf("operation error = %v", err)
			}
			test.want(t, project+"-acquired", replacement)
		})
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil || string(content) != want {
		t.Fatalf("%s = %q, %v; want %q", path, content, err, want)
	}
}

func TestPublish_GIVENFilesystemCaseProbeFails_WHENCheckingOwnership_THENFailsClosedWithoutMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory write permissions do not provide a deterministic probe failure on Windows")
	}
	project := t.TempDir()
	statePath := filepath.Join(project, ".ktml", "state.json")
	testBeforeRootMutation = func(operation, path string) {
		if operation == "project root acquired" && path == project {
			if err := os.Chmod(project, 0o500); err != nil {
				t.Fatalf("make acquired project read-only: %v", err)
			}
		}
	}
	t.Cleanup(func() {
		testBeforeRootMutation = nil
		_ = os.Chmod(project, 0o700)
	})

	err := Publish(statePath, &Manifest{Files: make(map[string]FileEntry)}, "page.ktml", FileEntry{
		Hash:          "hash",
		PrimaryOutput: "page.out",
	}, []Output{{Path: "page.out", Content: []byte("generated"), Description: "primary output"}})

	if chmodErr := os.Chmod(project, 0o700); chmodErr != nil {
		t.Fatalf("restore project permissions: %v", chmodErr)
	}
	if err == nil || !strings.Contains(err.Error(), "determine filesystem case sensitivity") {
		t.Fatalf("Publish() error = %v, want fail-closed case probe error", err)
	}
	for _, path := range []string{"page.out", filepath.Join(".ktml", "state.json")} {
		if _, statErr := os.Stat(filepath.Join(project, path)); !os.IsNotExist(statErr) {
			t.Errorf("%s was mutated after failed case probe: %v", path, statErr)
		}
	}
	probes, globErr := filepath.Glob(filepath.Join(project, ".ktml-case-probe-*"))
	if globErr != nil || len(probes) != 0 {
		t.Fatalf("case probe artifacts = %v, %v", probes, globErr)
	}
}

func TestPublish_GIVENFilesystemCaseProbeCleanupFails_WHENCheckingOwnership_THENFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory write permissions do not provide a deterministic cleanup failure on Windows")
	}
	project := t.TempDir()
	probeCreated := false
	testBeforeRootMutation = func(operation, _ string) {
		if operation != "filesystem case probe created" {
			return
		}
		probeCreated = true
		if err := os.Chmod(project, 0o500); err != nil {
			t.Fatalf("make acquired project read-only: %v", err)
		}
	}
	t.Cleanup(func() {
		testBeforeRootMutation = nil
		_ = os.Chmod(project, 0o700)
	})

	err := Publish(filepath.Join(project, ".ktml", "state.json"), &Manifest{Files: make(map[string]FileEntry)}, "page.ktml", FileEntry{
		Hash:          "hash",
		PrimaryOutput: "page.out",
	}, []Output{{Path: "page.out", Content: []byte("generated"), Description: "primary output"}})

	if chmodErr := os.Chmod(project, 0o700); chmodErr != nil {
		t.Fatalf("restore project permissions: %v", chmodErr)
	}
	if !probeCreated {
		t.Fatal("Publish did not create a rooted filesystem case probe")
	}
	if err == nil || !strings.Contains(err.Error(), "determine filesystem case sensitivity") {
		t.Fatalf("Publish() error = %v, want fail-closed probe cleanup error", err)
	}
	if _, statErr := os.Stat(filepath.Join(project, "page.out")); !os.IsNotExist(statErr) {
		t.Fatalf("output was mutated after failed probe cleanup: %v", statErr)
	}
	probes, globErr := filepath.Glob(filepath.Join(project, ".ktml-case-probe-*"))
	if globErr != nil {
		t.Fatalf("list case probes: %v", globErr)
	}
	for _, probe := range probes {
		if removeErr := os.Remove(probe); removeErr != nil {
			t.Errorf("remove failed probe fixture: %v", removeErr)
		}
	}
}

func TestPublish_GIVENDirectorySymlinkAliasedOutputs_WHENOwnedByDifferentSources_THENRejectsSecondOwner(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "real"), 0o700); err != nil {
		t.Fatalf("create output directory: %v", err)
	}
	if err := os.Symlink("real", filepath.Join(project, "alias")); err != nil {
		t.Fatalf("create in-project directory alias: %v", err)
	}
	statePath := filepath.Join(project, ".ktml", "state.json")
	manifest := &Manifest{Files: make(map[string]FileEntry)}
	if err := Publish(statePath, manifest, "first.ktml", FileEntry{Hash: "first", PrimaryOutput: "alias/shared.out"}, []Output{
		{Path: "alias/shared.out", Content: []byte("first"), Description: "primary output"},
	}); err != nil {
		t.Fatalf("publish first owner: %v", err)
	}
	if got := manifest.Files["first.ktml"].PrimaryOutput; got != "real/shared.out" {
		t.Fatalf("stored output path = %q, want canonical real/shared.out", got)
	}

	err := Publish(statePath, manifest, "second.ktml", FileEntry{Hash: "second", PrimaryOutput: "real/shared.out"}, []Output{
		{Path: "real/shared.out", Content: []byte("second"), Description: "primary output"},
	})

	if err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Fatalf("Publish() error = %v, want aliased ownership collision", err)
	}
	assertFileContent(t, filepath.Join(project, "real", "shared.out"), "first")
}
