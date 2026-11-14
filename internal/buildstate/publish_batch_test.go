package buildstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishBatch_GIVENPublicationsAndRemoval_WHENCommitted_THENWritesOutputsDropsRemovedSourceAndSavesOnce(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("removed.out", []byte("removed"), 0o600); err != nil {
		t.Fatalf("write removed output: %v", err)
	}
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: map[string]FileEntry{}}
	if err := Publish(statePath, manifest, "removed.ktml", FileEntry{Hash: "old", PrimaryOutput: "removed.out"}, []Output{{Path: "removed.out", Content: []byte("removed"), Description: "primary output"}}); err != nil {
		t.Fatalf("seed Publish() error = %v", err)
	}
	saves := 0
	testBeforeRootMutation = func(operation, _ string) {
		if operation == "build state save" {
			saves++
		}
	}
	t.Cleanup(func() { testBeforeRootMutation = nil })

	err := PublishBatch(statePath, manifest, []Publication{
		{Source: "a.ktml", Entry: FileEntry{Hash: "a", PrimaryOutput: "a.out"}, Outputs: []Output{{Path: "a.out", Content: []byte("a"), Description: "primary output"}}},
		{Source: "b.ktml", Entry: FileEntry{Hash: "b", PrimaryOutput: "b.out"}, Outputs: []Output{{Path: "b.out", Content: []byte("b"), Description: "primary output"}}},
	}, []string{"removed.ktml"})

	if err != nil {
		t.Fatalf("PublishBatch() error = %v", err)
	}
	if saves != 1 {
		t.Errorf("build state saves = %d, want 1", saves)
	}
	assertFileContent(t, "a.out", "a")
	assertFileContent(t, "b.out", "b")
	if _, err := os.Stat("removed.out"); !os.IsNotExist(err) {
		t.Errorf("removed source output still exists: %v", err)
	}
	loaded, err := Load(statePath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for source, want := range map[string]bool{"a.ktml": true, "b.ktml": true, "removed.ktml": false} {
		if _, got := loaded.Files[source]; got != want {
			t.Errorf("saved state has %s = %v, want %v", source, got, want)
		}
		if _, got := manifest.Files[source]; got != want {
			t.Errorf("in-memory manifest has %s = %v, want %v", source, got, want)
		}
	}
}

func TestPublishBatch_GIVENStateSaveFails_WHENCommitting_THENRestoresEveryOutputAndKeepsManifest(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: map[string]FileEntry{}}
	for source, output := range map[string]string{"a.ktml": "a.out", "removed.ktml": "removed.out"} {
		if err := Publish(statePath, manifest, source, FileEntry{Hash: "old", PrimaryOutput: output}, []Output{{Path: output, Content: []byte("old " + output), Description: "primary output"}}); err != nil {
			t.Fatalf("seed Publish(%s) error = %v", source, err)
		}
	}
	testBeforeRootMutation = func(operation, _ string) {
		if operation != "build state save" {
			return
		}
		if err := os.Rename(".ktml", ".ktml-moved"); err != nil {
			t.Fatalf("move state directory: %v", err)
		}
		if err := os.Mkdir(".ktml", 0o700); err != nil {
			t.Fatalf("replace state directory: %v", err)
		}
	}
	t.Cleanup(func() { testBeforeRootMutation = nil })

	err := PublishBatch(statePath, manifest, []Publication{
		{Source: "a.ktml", Entry: FileEntry{Hash: "a", PrimaryOutput: "a.out"}, Outputs: []Output{{Path: "a.out", Content: []byte("new a"), Description: "primary output"}}},
		{Source: "b.ktml", Entry: FileEntry{Hash: "b", PrimaryOutput: "b.out"}, Outputs: []Output{{Path: "b.out", Content: []byte("new b"), Description: "primary output"}}},
	}, []string{"removed.ktml"})

	if err == nil {
		t.Fatal("PublishBatch() succeeded after the state directory was replaced")
	}
	assertFileContent(t, "a.out", "old a.out")
	assertFileContent(t, "removed.out", "old removed.out")
	if _, err := os.Stat("b.out"); !os.IsNotExist(err) {
		t.Errorf("new output survived rollback: %v", err)
	}
	if len(manifest.Files) != 2 || manifest.Files["a.ktml"].Hash != "old" {
		t.Errorf("manifest changed after failed batch: %+v", manifest.Files)
	}
}

func TestPublishBatch_GIVENOutputOwnedOutsideBatch_WHENCommitting_THENWritesNothing(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: map[string]FileEntry{}}
	if err := Publish(statePath, manifest, "owner.ktml", FileEntry{Hash: "owner", PrimaryOutput: "shared.out"}, []Output{{Path: "shared.out", Content: []byte("owner"), Description: "primary output"}}); err != nil {
		t.Fatalf("seed Publish() error = %v", err)
	}

	err := PublishBatch(statePath, manifest, []Publication{
		{Source: "a.ktml", Entry: FileEntry{Hash: "a", PrimaryOutput: "a.out"}, Outputs: []Output{{Path: "a.out", Content: []byte("a"), Description: "primary output"}}},
		{Source: "b.ktml", Entry: FileEntry{Hash: "b", PrimaryOutput: "shared.out"}, Outputs: []Output{{Path: "shared.out", Content: []byte("b"), Description: "primary output"}}},
	}, nil)

	if err == nil {
		t.Fatal("PublishBatch() claimed an output owned by a source outside the batch")
	}
	if _, err := os.Stat("a.out"); !os.IsNotExist(err) {
		t.Errorf("first publication was written before the batch failed: %v", err)
	}
	assertFileContent(t, "shared.out", "owner")
}

func TestPublishBatch_GIVENOutputMovesBetweenBatchSources_WHENCommitted_THENTransfersOwnership(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: map[string]FileEntry{}}
	if err := Publish(statePath, manifest, "old.ktml", FileEntry{Hash: "old", PrimaryOutput: "moved.out"}, []Output{{Path: "moved.out", Content: []byte("old"), Description: "primary output"}}); err != nil {
		t.Fatalf("seed Publish() error = %v", err)
	}

	err := PublishBatch(statePath, manifest, []Publication{
		{Source: "new.ktml", Entry: FileEntry{Hash: "new", PrimaryOutput: "moved.out"}, Outputs: []Output{{Path: "moved.out", Content: []byte("new"), Description: "primary output"}}},
	}, []string{"old.ktml"})

	if err != nil {
		t.Fatalf("PublishBatch() error = %v", err)
	}
	assertFileContent(t, "moved.out", "new")
	if _, ok := manifest.Files["old.ktml"]; ok {
		t.Error("removed source is still recorded")
	}
}
