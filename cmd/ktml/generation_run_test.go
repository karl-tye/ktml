package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeGenerationWorkspace writes a module shaped like a real application:
// packages of component documents that call a shared component from another
// package, read records from their own Host Schema, and sit beside large
// handwritten Go files. It returns the KTML sources in walk order.
func writeGenerationWorkspace(t testing.TB, root string, packages, documents, components int) []string {
	t.Helper()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/app\n\ngo 1.26.3\n")
	write("shared/card.ktml", "Card [title: string, children?: content] {\n\tarticle class=\"card\" {\n\t\th2 \"{title}\"\n\t\tchildren\n\t}\n}\n")
	write("shared/shared.go", "package shared\n")
	sources := []string{"shared/card.ktml"}
	for p := range packages {
		dir := fmt.Sprintf("p%d", p)
		var handwritten strings.Builder
		fmt.Fprintf(&handwritten, "package %s\n\n", dir)
		for f := range 1500 {
			fmt.Fprintf(&handwritten, "func helper%d(values []string) int {\n\ttotal := 0\n\tfor index, value := range values {\n\t\tif len(value) > %d {\n\t\t\ttotal += index * len(value)\n\t\t}\n\t}\n\treturn total\n}\n\n", f, f%7)
		}
		write(dir+"/handlers.go", handwritten.String())
		var schema strings.Builder
		for r := range 12 {
			fmt.Fprintf(&schema, "record Row%d [", r)
			for f := range 8 {
				if f > 0 {
					schema.WriteString(", ")
				}
				fmt.Fprintf(&schema, "field%d: string", f)
			}
			schema.WriteString("]\n")
		}
		write(dir+"/models.ktmlschema", schema.String())
		for d := range documents {
			var document strings.Builder
			document.WriteString("import { Card } from \"../shared/card.ktml\"\nimport { Row0, Row1 } from \"./models.ktmlschema\"\n\n")
			for c := range components {
				fmt.Fprintf(&document, "View%dx%d [row: Row0, other: Row1, title: string] {\n\tsection class=\"view\" {\n\t\th1 \"{title}\"\n", d, c)
				for call := range 12 {
					fmt.Fprintf(&document, "\t\tCard title=\"{row.field%d}\" {\n\t\t\tp class=\"line\" \"{other.field%d} and {row.field%d}\"\n\t\t}\n", call%8, (call+1)%8, (call+2)%8)
				}
				document.WriteString("\t}\n}\n\n")
			}
			name := fmt.Sprintf("%s/view%d.ktml", dir, d)
			write(name, document.String())
			sources = append(sources, name)
		}
		sources = append(sources, dir+"/models.ktmlschema")
	}
	return sources
}

// generatedOutputs reads every generated Go file below root, by relative
// path.
func generatedOutputs(t *testing.T, root string) map[string]string {
	t.Helper()
	outputs := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "handlers.go") || strings.HasSuffix(path, "shared.go") {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		outputs[relative] = string(mustReadFile(t, path))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return outputs
}

func TestGenerateFilesContext_GIVENAWorkspace_WHENGeneratedTogether_THENMatchesGeneratingFileByFile(t *testing.T) {
	sequential, concurrent := t.TempDir(), t.TempDir()
	sources := writeGenerationWorkspace(t, sequential, 3, 3, 2)
	writeGenerationWorkspace(t, concurrent, 3, 3, 2)
	t.Chdir(sequential)
	for _, source := range sources {
		if err := generateFile(source, false); err != nil {
			t.Fatalf("generateFile(%s) error = %v", source, err)
		}
	}
	want := generatedOutputs(t, sequential)
	t.Chdir(concurrent)
	for run := range 3 {
		if err := os.RemoveAll(".ktml"); err != nil {
			t.Fatal(err)
		}
		if err := generateFilesContext(context.Background(), sources, false); err != nil {
			t.Fatalf("generateFilesContext() run %d error = %v", run, err)
		}
		got := generatedOutputs(t, concurrent)
		if len(want) == 0 || len(got) != len(want) {
			t.Fatalf("run %d generated %d files, file by file generated %d", run, len(got), len(want))
		}
		for name, content := range want {
			if got[name] != content {
				t.Errorf("run %d: %s differs from file-by-file generation", run, name)
			}
		}
	}
}

func TestGenerateFilesContext_GIVENALaterFileFails_WHENGenerating_THENPublishesTheFilesBeforeItAndReportsTheFailure(t *testing.T) {
	writeComponentProject(t, map[string]string{
		"web/a.ktml":     "A { p \"a\" }\n",
		"web/b.ktml":     "B { Missing }\n",
		"web/c.ktml":     "C { p \"c\" }\n",
		"web/handler.go": "package web\n",
	})

	err := generateFilesContext(context.Background(), []string{"web/a.ktml", "web/b.ktml", "web/c.ktml"}, false)

	if err == nil || !strings.Contains(err.Error(), "b.ktml") {
		t.Fatalf("generateFilesContext() error = %v, want the failure of b.ktml", err)
	}
	if _, err := os.Stat("web/a_ktml.go"); err != nil {
		t.Errorf("a.ktml, before the failure, was not published: %v", err)
	}
	if _, err := os.Stat("web/c_ktml.go"); !os.IsNotExist(err) {
		t.Errorf("c.ktml, after the failure, was published: %v", err)
	}
	if state := string(mustReadFile(t, filepath.Join(".ktml", "state.json"))); !strings.Contains(state, "a_ktml.go") || strings.Contains(state, "c_ktml.go") {
		t.Errorf("build state does not record exactly the files before the failure:\n%s", state)
	}
}

// generationBudget bounds generating the gate workspace cold, with a wide
// margin for slower machines. When it was set, generation took about 0.1 s
// cold and 0.02 s warm on 32 cores, and over 10 s for both while generation
// parsed a package's handwritten Go for every cross-package reference and
// checked the cache only after generating.
const generationBudget = time.Second

func TestGenerateFilesContext_GIVENAnApplicationSizedWorkspace_WHENGeneratedColdAndWarm_THENStaysWithinTheBudget(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing budget runs without -race and -short")
	}
	root := t.TempDir()
	sources := writeGenerationWorkspace(t, root, 6, 8, 4)
	t.Chdir(root)

	start := time.Now()
	if err := generateFilesContext(context.Background(), sources, false); err != nil {
		t.Fatalf("cold generateFilesContext() error = %v", err)
	}
	cold := time.Since(start)
	state := mustReadFile(t, filepath.Join(".ktml", "state.json"))
	start = time.Now()
	if err := generateFilesContext(context.Background(), sources, false); err != nil {
		t.Fatalf("warm generateFilesContext() error = %v", err)
	}
	warm := time.Since(start)

	t.Logf("%d sources: cold %v, warm %v", len(sources), cold, warm)
	if cold > generationBudget {
		t.Errorf("cold generation took %v, budget %v", cold, generationBudget)
	}
	// Nothing changed, so warm generation only verifies the cache.
	if warm > generationBudget/2 {
		t.Errorf("warm generation took %v, budget %v", warm, generationBudget/2)
	}
	if got := mustReadFile(t, filepath.Join(".ktml", "state.json")); string(got) != string(state) {
		t.Error("warm generation changed the build state")
	}
}

func BenchmarkGenerateFilesContext(b *testing.B) {
	root := b.TempDir()
	sources := writeGenerationWorkspace(b, root, 6, 8, 4)
	b.Chdir(root)
	b.Run("cold", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()
			if err := os.RemoveAll(".ktml"); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
			if err := generateFilesContext(context.Background(), sources, false); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("warm", func(b *testing.B) {
		for b.Loop() {
			if err := generateFilesContext(context.Background(), sources, false); err != nil {
				b.Fatal(err)
			}
		}
	})
}
