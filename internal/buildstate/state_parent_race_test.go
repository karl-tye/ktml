package buildstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublish_GIVENMissingStateParentIsRacedWithInRootSymlink_WHENPublishing_THENPreservesVictimState(t *testing.T) {
	project := t.TempDir()
	victim := filepath.Join(project, "victim")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatalf("create victim directory: %v", err)
	}
	victimState := filepath.Join(victim, "state.json")
	if err := os.WriteFile(victimState, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("write victim state: %v", err)
	}

	raced := false
	testBeforeRootMutation = func(operation, _ string) {
		if raced || operation != "build state parent absent" {
			return
		}
		raced = true
		if err := os.Symlink("victim", filepath.Join(project, ".ktml")); err != nil {
			t.Fatalf("race state parent with symlink: %v", err)
		}
	}
	t.Cleanup(func() { testBeforeRootMutation = nil })

	manifest := &Manifest{Files: make(map[string]FileEntry)}
	err := Publish(filepath.Join(project, ".ktml", "state.json"), manifest, "page.ktml", FileEntry{
		Hash:          "hash",
		PrimaryOutput: "page.out",
	}, []Output{{Path: "page.out", Content: []byte("generated"), Description: "primary output"}})

	if !raced {
		t.Fatal("Publish did not reach the missing-parent race boundary")
	}
	if err == nil || !strings.Contains(err.Error(), "build state parent") {
		t.Fatalf("Publish() error = %v, want fail-closed state acquisition error", err)
	}
	assertFileContent(t, victimState, "preserve")
	if _, statErr := os.Stat(filepath.Join(project, "page.out")); !os.IsNotExist(statErr) {
		t.Fatalf("output was not rolled back after failed state acquisition: %v", statErr)
	}
	if len(manifest.Files) != 0 {
		t.Fatalf("manifest changed after failed publication: %#v", manifest.Files)
	}
}

func TestSave_GIVENMissingStateParentIsRacedWithInRootSymlink_WHENSaving_THENPreservesVictimState(t *testing.T) {
	project := t.TempDir()
	victim := filepath.Join(project, "victim")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatalf("create victim directory: %v", err)
	}
	victimState := filepath.Join(victim, "state.json")
	if err := os.WriteFile(victimState, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("write victim state: %v", err)
	}

	raced := false
	testBeforeRootMutation = func(operation, _ string) {
		if raced || operation != "build state parent absent" {
			return
		}
		raced = true
		if err := os.Symlink("victim", filepath.Join(project, ".ktml")); err != nil {
			t.Fatalf("race state parent with symlink: %v", err)
		}
	}
	t.Cleanup(func() { testBeforeRootMutation = nil })

	err := Save(filepath.Join(project, ".ktml", "state.json"), &Manifest{Files: map[string]FileEntry{
		"page.ktml": {Hash: "hash"},
	}})

	if !raced {
		t.Fatal("Save did not reach the missing-parent race boundary")
	}
	if err == nil || !strings.Contains(err.Error(), "build state parent") {
		t.Fatalf("Save() error = %v, want fail-closed state acquisition error", err)
	}
	assertFileContent(t, victimState, "preserve")
}
