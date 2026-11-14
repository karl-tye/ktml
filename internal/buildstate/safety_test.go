package buildstate

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublish_GIVENReaderHasLastValidOutput_WHENReplacing_THENReaderObservesCompleteOldFile(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: make(map[string]FileEntry)}
	oldContent := []byte("complete last-valid output")
	if err := Publish(statePath, manifest, "page.ktml", FileEntry{
		Hash:          "old",
		PrimaryOutput: "page.out",
	}, []Output{{Path: "page.out", Content: oldContent, Description: "primary output"}}); err != nil {
		t.Fatalf("publish initial output: %v", err)
	}
	if err := os.Chmod("page.out", 0o600); err != nil {
		t.Fatalf("set last-valid output permissions: %v", err)
	}
	reader, err := os.Open("page.out")
	if err != nil {
		t.Fatalf("open last-valid output: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	if err := Publish(statePath, manifest, "page.ktml", FileEntry{
		Hash:          "new",
		PrimaryOutput: "page.out",
	}, []Output{{Path: "page.out", Content: []byte("complete new output"), Description: "primary output"}}); err != nil {
		t.Fatalf("publish replacement: %v", err)
	}

	observed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read through last-valid handle: %v", err)
	}
	if string(observed) != string(oldContent) {
		t.Fatalf("opened reader observed %q, want complete old output %q", observed, oldContent)
	}
	info, err := os.Stat("page.out")
	if err != nil {
		t.Fatalf("stat replacement: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("replacement permissions = %o, want 600", got)
	}
}

func TestPublish_GIVENCurrentOutputUsesAliasOfRecordedPath_WHENReplacing_THENDoesNotDeleteFreshOutput(t *testing.T) {
	tests := []struct {
		name     string
		recorded string
	}{
		{name: "clean path alias", recorded: "./dist/page.out"},
		{name: "case alias", recorded: filepath.Join("dist", "PAGE.OUT")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			t.Chdir(project)
			if err := os.Mkdir("dist", 0o700); err != nil {
				t.Fatalf("create output directory: %v", err)
			}
			if strings.Contains(test.name, "case") {
				if runtime.GOOS != "darwin" {
					t.Skip("case alias identity is Darwin-specific")
				}
				if err := os.WriteFile(filepath.Join("dist", "page.out"), []byte("probe"), 0o600); err != nil {
					t.Fatalf("write case-sensitivity probe: %v", err)
				}
				lower, _ := os.Stat(filepath.Join("dist", "page.out"))
				upper, err := os.Stat(filepath.Join("dist", "PAGE.OUT"))
				if err != nil || !os.SameFile(lower, upper) {
					t.Skip("Darwin volume is case-sensitive")
				}
			}
			statePath := filepath.Join(".ktml", "state.json")
			manifest := &Manifest{Files: map[string]FileEntry{
				"page.ktml": {Hash: "old", PrimaryOutput: test.recorded},
			}}
			if err := Publish(statePath, manifest, "page.ktml", FileEntry{
				Hash:          "new",
				PrimaryOutput: filepath.Join("dist", "page.out"),
			}, []Output{{Path: filepath.Join("dist", "page.out"), Content: []byte("fresh"), Description: "primary output"}}); err != nil {
				t.Fatalf("Publish() error = %v", err)
			}
			content, err := os.ReadFile(filepath.Join("dist", "page.out"))
			if err != nil || string(content) != "fresh" {
				t.Fatalf("fresh output = %q, %v", content, err)
			}
		})
	}
}

func TestRemove_GIVENManifestOutputEscapesProject_WHENRemovingSource_THENRejectsWithoutMutation(t *testing.T) {
	project := t.TempDir()
	out := t.TempDir()
	externalPath := filepath.Join(out, "outside.out")
	if err := os.WriteFile(externalPath, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("write external file: %v", err)
	}
	t.Chdir(project)
	manifest := &Manifest{Files: map[string]FileEntry{
		"page.ktml": {Hash: "hash", PrimaryOutput: filepath.Join("..", filepath.Base(out), "outside.out")},
	}}

	err := Remove(filepath.Join(".ktml", "state.json"), manifest, "page.ktml")

	if err == nil || !strings.Contains(err.Error(), "outside project root") {
		t.Fatalf("Remove() error = %v, want project-root rejection", err)
	}
	content, readErr := os.ReadFile(externalPath)
	if readErr != nil || string(content) != "preserve" {
		t.Fatalf("external output = %q, %v; want preserved", content, readErr)
	}
	if _, ok := manifest.Files["page.ktml"]; !ok {
		t.Fatal("manifest entry removed after rejected output path")
	}
}

func TestPublish_GIVENStaleManifestOutputEscapesProject_WHENGenerating_THENRejectsBeforeWriting(t *testing.T) {
	project := t.TempDir()
	out := t.TempDir()
	externalPath := filepath.Join(out, "outside.out")
	if err := os.WriteFile(externalPath, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("write external file: %v", err)
	}
	t.Chdir(project)
	manifest := &Manifest{Files: map[string]FileEntry{
		"page.ktml": {Hash: "old", PrimaryOutput: externalPath},
	}}

	err := Publish(filepath.Join(".ktml", "state.json"), manifest, "page.ktml", FileEntry{
		Hash:          "new",
		PrimaryOutput: "page.out",
	}, []Output{{Path: "page.out", Content: []byte("new"), Description: "primary output"}})

	if err == nil || !strings.Contains(err.Error(), "outside project root") {
		t.Fatalf("Publish() error = %v, want stale path rejection", err)
	}
	if _, statErr := os.Stat("page.out"); !os.IsNotExist(statErr) {
		t.Fatalf("new output written before stale path rejection: %v", statErr)
	}
	content, readErr := os.ReadFile(externalPath)
	if readErr != nil || string(content) != "preserve" {
		t.Fatalf("external stale target = %q, %v; want preserved", content, readErr)
	}
}

func TestSave_GIVENExistingPrivatePermissions_WHENReplacing_THENPreservesPermissions(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(statePath, []byte(`{"files":{}}`), 0o600); err != nil {
		t.Fatalf("write initial state: %v", err)
	}
	if err := Save(statePath, &Manifest{Files: map[string]FileEntry{"page.ktml": {Hash: "hash"}}}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat state: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state permissions = %o, want 600", got)
	}
}
