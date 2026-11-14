package buildstate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublish_GIVENAtomicWriteAndTempCleanupFail_WHENPublishing_THENReportsBothErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory write permissions do not provide deterministic rename and cleanup failures on Windows")
	}
	project := t.TempDir()
	blocked := false
	testBeforeRootMutation = func(operation, path string) {
		if blocked || operation != "atomic write temporary created" || !strings.HasPrefix(filepath.Base(path), ".ktml-output-") {
			return
		}
		blocked = true
		if err := os.Chmod(project, 0o500); err != nil {
			t.Fatalf("make output directory read-only: %v", err)
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
		t.Fatalf("restore output directory permissions: %v", chmodErr)
	}
	if !blocked {
		t.Fatal("Publish did not reach the atomic-write cleanup boundary")
	}
	if err == nil || !strings.Contains(err.Error(), "failed to write primary output") || !strings.Contains(err.Error(), "temporary cleanup failed") {
		t.Fatalf("Publish() error = %v, want primary write and temporary cleanup failures", err)
	}
	artifacts, globErr := filepath.Glob(filepath.Join(project, ".ktml-output-*.tmp"))
	if globErr != nil || len(artifacts) != 1 {
		t.Fatalf("failed-cleanup artifacts = %v, %v; want one reported temp file", artifacts, globErr)
	}
	if removeErr := os.Remove(artifacts[0]); removeErr != nil {
		t.Fatalf("remove failed-cleanup fixture: %v", removeErr)
	}
}

func TestPublish_GIVENRollbackWriteAndTempCleanupFail_WHENStateSaveFails_THENReportsPrimaryAndRollbackErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory write permissions do not provide deterministic rename and cleanup failures on Windows")
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "page.out"), []byte("published"), 0o600); err != nil {
		t.Fatalf("write published output: %v", err)
	}
	stateDir := filepath.Join(project, ".ktml")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatalf("create state directory: %v", err)
	}
	if err := os.Mkdir(filepath.Join(stateDir, "state.json"), 0o700); err != nil {
		t.Fatalf("create invalid state directory: %v", err)
	}

	blocked := false
	testBeforeRootMutation = func(operation, path string) {
		if blocked || operation != "atomic write temporary created" || !strings.HasPrefix(filepath.Base(path), ".ktml-rollback-") {
			return
		}
		blocked = true
		if err := os.Chmod(project, 0o500); err != nil {
			t.Fatalf("make rollback directory read-only: %v", err)
		}
	}
	t.Cleanup(func() {
		testBeforeRootMutation = nil
		_ = os.Chmod(project, 0o700)
	})

	manifest := &Manifest{Files: map[string]FileEntry{
		"page.ktml": {Hash: "published", PrimaryOutput: "page.out"},
	}}
	err := Publish(filepath.Join(stateDir, "state.json"), manifest, "page.ktml", FileEntry{
		Hash:          "replacement",
		PrimaryOutput: "page.out",
	}, []Output{{Path: "page.out", Content: []byte("replacement"), Description: "primary output"}})

	if chmodErr := os.Chmod(project, 0o700); chmodErr != nil {
		t.Fatalf("restore rollback directory permissions: %v", chmodErr)
	}
	if !blocked {
		t.Fatal("Publish did not reach the rollback cleanup boundary")
	}
	for _, want := range []string{"failed to save build state", "build state is not a regular file", "rollback failed", "temporary cleanup failed"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("Publish() error = %v, want context %q", err, want)
		}
	}
	if got := manifest.Files["page.ktml"].Hash; got != "published" {
		t.Fatalf("manifest hash = %q, want unchanged published hash", got)
	}
	artifacts, globErr := filepath.Glob(filepath.Join(project, ".ktml-rollback-*.tmp"))
	if globErr != nil || len(artifacts) != 1 {
		t.Fatalf("failed-cleanup artifacts = %v, %v; want one reported rollback temp file", artifacts, globErr)
	}
	if removeErr := os.Remove(artifacts[0]); removeErr != nil {
		t.Fatalf("remove failed-cleanup fixture: %v", removeErr)
	}
}
