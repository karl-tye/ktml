package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateFile_GIVENAPluginOutputOutsideItsDeclaredOutputs_WHENGenerating_THENRefusedNamingPluginAndPath(t *testing.T) {
	tests := []struct{ name, outputPath, want string }{
		{"git hook", filepath.Join(".git", "hooks", "pre-commit"), ".git/hooks/pre-commit"},
		{"git hook through a symlink in the root", filepath.Join("gen", "hooks", "pre-commit"), ".git/hooks/pre-commit"},
		{"config", "ktml.config.json", "ktml.config.json"},
		{"build state", filepath.Join(".ktml", "state.json"), ".ktml/state.json"},
		{"KTML source", "page.ktml", "page.ktml"},
		{"prefix lookalike", filepath.Join("generated", "page.out"), "generated/page.out"},
		{"dot-dot back out of the root", filepath.Join("gen", "..", "page.out"), "page.out"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			t.Chdir(project)
			for _, dir := range []string{filepath.Join(".git", "hooks"), "gen", "generated", ".ktml"} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(filepath.Join("..", ".git", "hooks"), filepath.Join("gen", "hooks")); err != nil {
				t.Fatal(err)
			}
			pluginPath := buildOutputPathPlugin(t, test.outputPath)
			files := map[string]string{
				"page.ktml":        `Page { div }`,
				"ktml.config.json": `{"resolution":{"generate":"evil"},"plugin":{"evil":{"outputs":[{"dir":"gen"}],"path":` + quoteJSON(t, pluginPath) + `}}}`,
				filepath.Join(".git", "hooks", "pre-commit"): "#!/bin/sh\n",
			}
			for name, content := range files {
				if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			err := generateFile("page.ktml", false)

			if err == nil || !strings.Contains(err.Error(), `plugin "evil"`) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("generateFile() error = %v; want a refusal naming plugin \"evil\" and %s", err, test.want)
			}
			for name, content := range files {
				if got, _ := os.ReadFile(name); string(got) != content {
					t.Fatalf("%s = %q; want it unchanged", name, got)
				}
			}
			for _, name := range []string{filepath.Join("generated", "page.out"), "page.out", filepath.Join(".ktml", "state.json")} {
				if _, statErr := os.Stat(name); !os.IsNotExist(statErr) {
					t.Fatalf("%s stat error = %v; want nothing published", name, statErr)
				}
			}
		})
	}
}

func TestGenerateFile_GIVENAPluginOutputInsideItsDeclaredOutputs_WHENGenerating_THENWritten(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("gen", 0o755); err != nil {
		t.Fatal(err)
	}
	pluginPath := buildOutputPathPlugin(t, filepath.Join("gen", "page.out"))
	for name, content := range map[string]string{
		"page.ktml":        `Page { div }`,
		"ktml.config.json": `{"resolution":{"generate":"good"},"plugin":{"good":{"outputs":[{"dir":"gen","suffixes":[".out"]}],"path":` + quoteJSON(t, pluginPath) + `}}}`,
	} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := generateFile("page.ktml", false); err != nil {
		t.Fatalf("generateFile() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join("gen", "page.out")); err != nil {
		t.Fatalf("gen/page.out stat error = %v", err)
	}
}
