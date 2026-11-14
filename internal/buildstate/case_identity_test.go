package buildstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublish_GIVENCaseAliasedOutputPaths_WHENOwnedByDifferentSources_THENMatchesVolumeSemantics(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	caseInsensitive := pathsAreFilesystemAliases(t, "case-probe", "CASE-PROBE")
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: make(map[string]FileEntry)}
	if err := Publish(statePath, manifest, "first.ktml", FileEntry{Hash: "first", PrimaryOutput: "shared.out"}, []Output{
		{Path: "shared.out", Content: []byte("first"), Description: "primary output"},
	}); err != nil {
		t.Fatalf("publish first source: %v", err)
	}

	err := Publish(statePath, manifest, "second.ktml", FileEntry{Hash: "second", PrimaryOutput: "SHARED.OUT"}, []Output{
		{Path: "SHARED.OUT", Content: []byte("second"), Description: "primary output"},
	})

	if caseInsensitive {
		if err == nil || !strings.Contains(err.Error(), "owned by") {
			t.Fatalf("Publish() error = %v, want ownership collision on case-insensitive volume", err)
		}
		if content, readErr := os.ReadFile("shared.out"); readErr != nil || string(content) != "first" {
			t.Fatalf("shared output = %q, %v; want first owner preserved", content, readErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("Publish() error = %v on case-sensitive volume", err)
	}
	if content, readErr := os.ReadFile("shared.out"); readErr != nil || string(content) != "first" {
		t.Fatalf("lowercase output = %q, %v", content, readErr)
	}
	if content, readErr := os.ReadFile("SHARED.OUT"); readErr != nil || string(content) != "second" {
		t.Fatalf("uppercase output = %q, %v", content, readErr)
	}
}

func pathsAreFilesystemAliases(t *testing.T, first, second string) bool {
	t.Helper()
	if err := os.WriteFile(first, []byte("probe"), 0o600); err != nil {
		t.Fatalf("write case probe: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(first)
		_ = os.Remove(second)
	})
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatalf("stat case probe: %v", err)
	}
	secondInfo, err := os.Stat(second)
	return err == nil && os.SameFile(firstInfo, secondInfo)
}

func TestFallbackOutputPath_GIVENCaseAliasedAbsoluteInProjectSource_WHENVolumeIsCaseInsensitive_THENReturnsContainedPath(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "ProjectRoot")
	if err := os.MkdirAll(filepath.Join(project, "views"), 0o700); err != nil {
		t.Fatalf("create project source directory: %v", err)
	}
	source := filepath.Join(project, "views", "page.ktml")
	if err := os.WriteFile(source, []byte("page"), 0o600); err != nil {
		t.Fatalf("write project source: %v", err)
	}
	t.Chdir(project)
	if !pathsAreFilesystemAliases(t, "case-probe", "CASE-PROBE") {
		t.Skip("volume is case-sensitive")
	}
	aliasedSource := filepath.Join(parent, "projectroot", "views", "page.ktml")

	got, err := FallbackOutputPath(project, aliasedSource, ".go")

	if err != nil {
		t.Fatalf("FallbackOutputPath() error = %v", err)
	}
	if want := filepath.Join("views", "page.ktml.go"); got != want {
		t.Fatalf("FallbackOutputPath() = %q, want %q", got, want)
	}
	outside := filepath.Join(t.TempDir(), "outside.ktml")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside source: %v", err)
	}
	if _, err := FallbackOutputPath(project, outside, ".go"); err == nil || !strings.Contains(err.Error(), "outside project root") {
		t.Fatalf("outside FallbackOutputPath() error = %v, want containment rejection", err)
	}
}
