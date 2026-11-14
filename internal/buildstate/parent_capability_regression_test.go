package buildstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildState_GIVENValidatedOutputParentIsRetargetedToOwnedDirectory_WHENMutating_THENDoesNotOverwriteOrDeleteVictim(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		run       func(string, *Manifest) error
	}{
		{
			name:      "output replacement",
			operation: "output backup",
			run: func(statePath string, manifest *Manifest) error {
				return Publish(statePath, manifest, "candidate.ktml", FileEntry{Hash: "new", PrimaryOutput: filepath.Join("candidate", "shared.out")}, []Output{
					{Path: filepath.Join("candidate", "shared.out"), Content: []byte("replacement"), Description: "primary output"},
				})
			},
		},
		{
			name:      "stale output deletion",
			operation: "stale output deletion",
			run: func(statePath string, manifest *Manifest) error {
				manifest.Files["candidate.ktml"] = FileEntry{Hash: "old", PrimaryOutput: filepath.Join("candidate", "shared.out"), OutputHashes: map[string]string{"candidate/shared.out": Hash([]byte("candidate"))}}
				return Publish(statePath, manifest, "candidate.ktml", FileEntry{Hash: "new", PrimaryOutput: "fresh.out"}, []Output{
					{Path: "fresh.out", Content: []byte("fresh"), Description: "primary output"},
				})
			},
		},
		{
			name:      "source output deletion",
			operation: "source output deletion",
			run: func(statePath string, manifest *Manifest) error {
				manifest.Files["candidate.ktml"] = FileEntry{Hash: "old", PrimaryOutput: filepath.Join("candidate", "shared.out"), OutputHashes: map[string]string{"candidate/shared.out": Hash([]byte("candidate"))}}
				return Remove(statePath, manifest, "candidate.ktml")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			for _, dir := range []string{"candidate", "victim"} {
				if err := os.Mkdir(filepath.Join(project, dir), 0o700); err != nil {
					t.Fatalf("create %s directory: %v", dir, err)
				}
			}
			for path, content := range map[string]string{
				filepath.Join("candidate", "shared.out"): "candidate",
				filepath.Join("victim", "shared.out"):    "victim",
			} {
				if err := os.WriteFile(filepath.Join(project, path), []byte(content), 0o600); err != nil {
					t.Fatalf("write %s fixture: %v", path, err)
				}
			}

			swapped := false
			testBeforeRootMutation = func(operation, _ string) {
				if swapped || operation != test.operation {
					return
				}
				swapped = true
				if err := os.Rename(filepath.Join(project, "candidate"), filepath.Join(project, "candidate-acquired")); err != nil {
					t.Fatalf("move validated output parent: %v", err)
				}
				if err := os.Symlink("victim", filepath.Join(project, "candidate")); err != nil {
					t.Fatalf("retarget candidate parent: %v", err)
				}
			}
			t.Cleanup(func() { testBeforeRootMutation = nil })

			manifest := &Manifest{Files: map[string]FileEntry{
				"victim.ktml": {Hash: "victim", PrimaryOutput: filepath.Join("victim", "shared.out")},
			}}
			_ = test.run(filepath.Join(project, ".ktml", "state.json"), manifest)

			if !swapped {
				t.Fatalf("operation did not reach the post-validation %s boundary", test.operation)
			}
			assertFileContent(t, filepath.Join(project, "victim", "shared.out"), "victim")
		})
	}
}

func TestSave_GIVENValidatedStateParentIsRetargeted_WHENPublishing_THENDoesNotReplaceVictimState(t *testing.T) {
	project := t.TempDir()
	for _, dir := range []string{".ktml", "victim"} {
		if err := os.Mkdir(filepath.Join(project, dir), 0o700); err != nil {
			t.Fatalf("create %s directory: %v", dir, err)
		}
	}
	for _, dir := range []string{".ktml", "victim"} {
		if err := os.WriteFile(filepath.Join(project, dir, "state.json"), []byte("preserve"), 0o600); err != nil {
			t.Fatalf("write %s state fixture: %v", dir, err)
		}
	}

	swapped := false
	testBeforeRootMutation = func(operation, _ string) {
		if swapped || operation != "build state save" {
			return
		}
		swapped = true
		if err := os.Rename(filepath.Join(project, ".ktml"), filepath.Join(project, ".ktml-acquired")); err != nil {
			t.Fatalf("move validated state parent: %v", err)
		}
		if err := os.Symlink("victim", filepath.Join(project, ".ktml")); err != nil {
			t.Fatalf("retarget state parent: %v", err)
		}
	}
	t.Cleanup(func() { testBeforeRootMutation = nil })

	_ = Save(filepath.Join(project, ".ktml", "state.json"), &Manifest{Files: map[string]FileEntry{
		"page.ktml": {Hash: "new"},
	}})

	if !swapped {
		t.Fatal("Save did not reach the post-validation state publication boundary")
	}
	assertFileContent(t, filepath.Join(project, "victim", "state.json"), "preserve")
}

func TestPublish_GIVENValidatedParentsAreRetargetedBeforeStateFailure_WHENRollingBack_THENDoesNotRestoreIntoVictim(t *testing.T) {
	project := t.TempDir()
	for _, dir := range []string{"candidate", "victim", ".ktml"} {
		if err := os.Mkdir(filepath.Join(project, dir), 0o700); err != nil {
			t.Fatalf("create %s directory: %v", dir, err)
		}
	}
	for path, content := range map[string]string{
		filepath.Join("candidate", "shared.out"): "candidate",
		filepath.Join("victim", "shared.out"):    "victim",
		filepath.Join(".ktml", "state.json"):     "preserve",
	} {
		if err := os.WriteFile(filepath.Join(project, path), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s fixture: %v", path, err)
		}
	}
	if err := os.Mkdir(filepath.Join(project, "victim", "state.json"), 0o700); err != nil {
		t.Fatalf("create victim state directory: %v", err)
	}

	swapped := false
	testBeforeRootMutation = func(operation, _ string) {
		if swapped || operation != "build state save" {
			return
		}
		swapped = true
		for _, dir := range []string{"candidate", ".ktml"} {
			if err := os.Rename(filepath.Join(project, dir), filepath.Join(project, dir+"-acquired")); err != nil {
				t.Fatalf("move validated %s parent: %v", dir, err)
			}
			if err := os.Symlink("victim", filepath.Join(project, dir)); err != nil {
				t.Fatalf("retarget %s parent: %v", dir, err)
			}
		}
	}
	t.Cleanup(func() { testBeforeRootMutation = nil })

	manifest := &Manifest{Files: map[string]FileEntry{
		"victim.ktml": {Hash: "victim", PrimaryOutput: filepath.Join("victim", "shared.out")},
	}}
	err := Publish(filepath.Join(project, ".ktml", "state.json"), manifest, "candidate.ktml", FileEntry{
		Hash:          "candidate",
		PrimaryOutput: filepath.Join("candidate", "shared.out"),
	}, []Output{{Path: filepath.Join("candidate", "shared.out"), Content: []byte("replacement"), Description: "primary output"}})

	if !swapped {
		t.Fatal("Publish did not reach the state publication boundary")
	}
	if err == nil || !strings.Contains(err.Error(), "failed to save build state") {
		t.Fatalf("Publish() error = %v, want state publication failure", err)
	}
	assertFileContent(t, filepath.Join(project, "victim", "shared.out"), "victim")
	assertFileContent(t, filepath.Join(project, "candidate-acquired", "shared.out"), "candidate")
}

func TestPublish_GIVENCaseProbeIsRemovedBeforeAliasLookup_WHENResolvingOutput_THENFailsClosed(t *testing.T) {
	project := t.TempDir()
	removed := false
	testBeforeRootMutation = func(operation, path string) {
		if operation != "filesystem case probe created" {
			return
		}
		if err := os.Remove(filepath.Join(project, path)); err != nil {
			t.Fatalf("remove case probe: %v", err)
		}
		removed = true
	}
	t.Cleanup(func() { testBeforeRootMutation = nil })

	err := Publish(filepath.Join(project, ".ktml", "state.json"), &Manifest{Files: make(map[string]FileEntry)}, "page.ktml", FileEntry{
		Hash:          "hash",
		PrimaryOutput: "page.out",
	}, []Output{{Path: "page.out", Content: []byte("generated"), Description: "primary output"}})

	if !removed {
		t.Fatal("Publish did not reach the case alias lookup boundary")
	}
	if err == nil || !strings.Contains(err.Error(), "determine filesystem case sensitivity") {
		t.Fatalf("Publish() error = %v, want fail-closed case probe error", err)
	}
	if _, statErr := os.Stat(filepath.Join(project, "page.out")); !os.IsNotExist(statErr) {
		t.Fatalf("output was published after concurrent probe removal: %v", statErr)
	}
}

func TestPublish_GIVENOutputsHaveDifferentCanonicalParents_WHENResolvingOwnership_THENProbesEachParent(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "mounted"), 0o700); err != nil {
		t.Fatalf("create mounted output directory: %v", err)
	}
	probed := make(map[string]bool)
	testBeforeRootMutation = func(operation, path string) {
		if operation != "filesystem case probe created" {
			return
		}
		for _, parent := range []string{".", "mounted"} {
			if _, err := os.Stat(filepath.Join(project, parent, path)); err == nil {
				probed[parent] = true
			}
		}
	}
	t.Cleanup(func() { testBeforeRootMutation = nil })

	err := Publish(filepath.Join(project, ".ktml", "state.json"), &Manifest{Files: make(map[string]FileEntry)}, "page.ktml", FileEntry{
		Hash:             "hash",
		PrimaryOutput:    "page.out",
		AuxiliaryOutputs: []string{filepath.Join("mounted", "page.css")},
	}, []Output{
		{Path: "page.out", Content: []byte("page"), Description: "primary output"},
		{Path: filepath.Join("mounted", "page.css"), Content: []byte("style"), Description: "auxiliary output"},
	})

	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	for _, parent := range []string{".", "mounted"} {
		if !probed[parent] {
			t.Errorf("canonical output parent %q was not probed", parent)
		}
	}
}

func TestFallbackOutputPath_GIVENTrustedRootWasAcquiredThroughSymlink_WHENSourceUsesCanonicalTargetPath_THENReturnsContainedOutput(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	if err := os.MkdirAll(filepath.Join(project, "views"), 0o700); err != nil {
		t.Fatalf("create project source directory: %v", err)
	}
	source := filepath.Join(project, "views", "page.ktml")
	if err := os.WriteFile(source, []byte("page"), 0o600); err != nil {
		t.Fatalf("write project source: %v", err)
	}
	projectLink := filepath.Join(parent, "project-link")
	if err := os.Symlink("project", projectLink); err != nil {
		t.Fatalf("create project symlink: %v", err)
	}

	got, err := FallbackOutputPath(projectLink, source, ".go")

	if err != nil {
		t.Fatalf("FallbackOutputPath() error = %v", err)
	}
	if want := filepath.Join("views", "page.ktml.go"); got != want {
		t.Fatalf("FallbackOutputPath() = %q, want %q", got, want)
	}
}
