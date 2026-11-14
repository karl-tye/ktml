package buildstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublish_GIVENOutputRolesChange_WHENCommitted_THENRetainsCurrentOutputAndRemovesStalePrimary(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for path, content := range map[string]string{
		"old-primary.out": "old primary",
		"promoted.out":    "old auxiliary",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: map[string]FileEntry{
		"page.ktml": {
			Hash:             "old hash",
			PrimaryOutput:    "old-primary.out",
			AuxiliaryOutputs: []string{"promoted.out"},
			OutputHashes:     map[string]string{"old-primary.out": Hash([]byte("old primary")), "promoted.out": Hash([]byte("old auxiliary"))},
		},
	}}
	if err := Save(statePath, manifest); err != nil {
		t.Fatalf("save initial state: %v", err)
	}
	entry := FileEntry{Hash: "new hash", PrimaryOutput: "promoted.out"}

	if err := Publish(statePath, manifest, "page.ktml", entry, []Output{
		{Path: "promoted.out", Content: []byte("new primary"), Description: "primary output"},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	if got, err := os.ReadFile("promoted.out"); err != nil || string(got) != "new primary" {
		t.Fatalf("promoted output = %q, %v; want new primary", got, err)
	}
	if _, err := os.Stat("old-primary.out"); !os.IsNotExist(err) {
		t.Errorf("stale primary still exists: %v", err)
	}
	loaded, err := Load(statePath)
	if err != nil {
		t.Fatalf("load published state: %v", err)
	}
	if got := loaded.Files["page.ktml"]; got.Hash != "new hash" || got.PrimaryOutput != "promoted.out" {
		t.Errorf("published entry = %#v", got)
	}
}

func TestRemove_GIVENStateSaveFailure_WHENRemovingSource_THENRestoresOutputsAndPublishedState(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for path, content := range map[string]string{
		"page.html": "published primary",
		"page.go":   "published auxiliary",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: map[string]FileEntry{
		"page.ktml": {
			Hash:             "published hash",
			PrimaryOutput:    "page.html",
			AuxiliaryOutputs: []string{"page.go"},
		},
	}}
	if err := Save(statePath, manifest); err != nil {
		t.Fatalf("save initial state: %v", err)
	}
	initialState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read initial state: %v", err)
	}
	if err := os.Chmod(filepath.Dir(statePath), 0o500); err != nil {
		t.Fatalf("make state directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(statePath), 0o700) })

	err = Remove(statePath, manifest, "page.ktml")

	if err == nil || !strings.Contains(err.Error(), "failed to save build state") {
		t.Fatalf("Remove() error = %v, want state save failure", err)
	}
	for path, want := range map[string]string{
		"page.html": "published primary",
		"page.go":   "published auxiliary",
	} {
		if got, readErr := os.ReadFile(path); readErr != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want restored %q", path, got, readErr, want)
		}
	}
	if state, readErr := os.ReadFile(statePath); readErr != nil || string(state) != string(initialState) {
		t.Errorf("state after failed removal = %q, %v", state, readErr)
	}
	if _, ok := manifest.Files["page.ktml"]; !ok {
		t.Error("in-memory manifest entry removed after failed publication")
	}
}
