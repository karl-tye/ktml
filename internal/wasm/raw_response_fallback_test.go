package wasm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlugin_GIVENRawResponseAndAbsoluteSource_WHENGenerating_THENContainsFallbackInsideProjectOnly(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	t.Chdir(project)
	insideSource := filepath.Join(project, "page.ktml")
	outsideSource := filepath.Join(outside, "outside.ktml")
	for _, source := range []string{insideSource, outsideSource} {
		if err := os.WriteFile(source, []byte(`Page { div }`), 0o600); err != nil {
			t.Fatalf("write source: %v", err)
		}
	}
	ctx := context.Background()
	plugin, err := NewPluginWithOptions(ctx, buildDummyWasm(t), Options{Outputs: anyOutputs})
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })

	if err := plugin.Generate(insideSource); err != nil {
		t.Fatalf("generate contained absolute source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "page.ktml.go")); err != nil {
		t.Fatalf("contained fallback output missing: %v", err)
	}

	err = plugin.Generate(outsideSource)
	if err == nil || !strings.Contains(err.Error(), "outside project root") {
		t.Fatalf("generate outside source error = %v, want project-root rejection", err)
	}
	if _, statErr := os.Stat(outsideSource + ".go"); !os.IsNotExist(statErr) {
		t.Fatalf("fallback output written outside project: %v", statErr)
	}
}
