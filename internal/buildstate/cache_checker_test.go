package buildstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheChecker_GIVENRecordedOutputs_WHENChecked_THENAgreesWithCacheHit(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: make(map[string]FileEntry)}
	publishInto(t, statePath, manifest, "a.ktml", "a1", "a.out")
	publishInto(t, statePath, manifest, "b.ktml", "b1", "b.out")
	if err := os.WriteFile("b.out", []byte("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	checker := NewCacheChecker(statePath, manifest)
	for _, test := range []struct{ source, hash string }{
		{"a.ktml", "a1"},
		{"a.ktml", "a2"},
		{"b.ktml", "b1"},
		{"missing.ktml", "m1"},
	} {
		want, wantErr := CacheHit(statePath, manifest, test.source, test.hash)
		got, err := checker.Hit(test.source, test.hash)
		if got != want || (err == nil) != (wantErr == nil) {
			t.Errorf("Hit(%s, %s) = %v, %v; CacheHit = %v, %v", test.source, test.hash, got, err, want, wantErr)
		}
	}
	if hit, _ := checker.Hit("a.ktml", "a1"); !hit {
		t.Error("an unchanged output is not a hit")
	}
}

func TestCacheChecker_GIVENTwoSourcesOwningOneOutput_WHENChecked_THENFailsOnEveryHit(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	statePath := filepath.Join(".ktml", "state.json")
	manifest := &Manifest{Files: make(map[string]FileEntry)}
	publishInto(t, statePath, manifest, "a.ktml", "a1", "a.out")
	entry := manifest.Files["a.ktml"]
	manifest.Files["b.ktml"] = entry
	checker := NewCacheChecker(statePath, manifest)
	for range 2 {
		if hit, err := checker.Hit("a.ktml", "a1"); hit || err == nil || !strings.Contains(err.Error(), "owned by both") {
			t.Fatalf("Hit = %v, %v; want the ownership conflict", hit, err)
		}
	}
}
