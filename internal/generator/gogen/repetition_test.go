package gogen_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

// generateBoardFixture generates the Keyed Repetition fixture: its schema, and
// the production and development variants of its component document.
func generateBoardFixture(t *testing.T) map[string][]byte {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "board"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if err := snapshot.Err(); err != nil {
		t.Fatalf("fixture diagnostics: %v", err)
	}
	dir := filepath.Join(root, "kanban")
	options := gogen.Options{Package: "kanban", Packages: fixturePackages(root)}
	outputs := make(map[string][]byte)
	schema, _, err := gogen.GenerateSchema(snapshot, filepath.Join(dir, "kanban.ktmlschema"), options)
	if err != nil {
		t.Fatalf("GenerateSchema() error = %v", err)
	}
	outputs["kanban/kanban_ktmlschema.go"] = schema
	production, err := gogen.GenerateFile(snapshot, filepath.Join(dir, "board.ktml"), options)
	if err != nil {
		t.Fatalf("GenerateFile() error = %v", err)
	}
	outputs["kanban/board_ktml.go"] = production
	options.Development = true
	development, err := gogen.GenerateFile(snapshot, filepath.Join(dir, "board.ktml"), options)
	if err != nil {
		t.Fatalf("GenerateFile(Development) error = %v", err)
	}
	outputs["kanban/board_ktml_dev.go"] = development
	return outputs
}

func TestGenerate_GIVENKeyedRepetitionFixture_WHENGenerated_THENMatchesGoldenDeterministically(t *testing.T) {
	first, second := generateBoardFixture(t), generateBoardFixture(t)
	for name, content := range first {
		if !bytes.Equal(content, second[name]) {
			t.Errorf("%s is not deterministic", name)
		}
		golden := filepath.Join("testdata", "board", "golden", filepath.FromSlash(name)+".golden")
		if *update {
			if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, content, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("read golden %s: %v (run go test -run KeyedRepetition -update)", golden, err)
		}
		if !bytes.Equal(content, want) {
			t.Errorf("%s differs from %s:\n%s", name, golden, content)
		}
	}
}

// TestGenerate_GIVENKeyedRepetitionFixture_WHENInspected_THENHasOneSetPerRepetitionAndNoBodyBuffer
// is the generated-source inspection half of the Keyed Repetition gate.
func TestGenerate_GIVENKeyedRepetitionFixture_WHENInspected_THENHasOneSetPerRepetitionAndNoBodyBuffer(t *testing.T) {
	outputs := generateBoardFixture(t)
	source, err := os.ReadFile(filepath.Join("testdata", "board", "kanban", "board.ktml"))
	if err != nil {
		t.Fatal(err)
	}
	repetitions := len(regexp.MustCompile(`(?m)^\s*for \w+ in `).FindAll(source, -1))
	for _, name := range []string{"kanban/board_ktml.go", "kanban/board_ktml_dev.go"} {
		generated := string(outputs[name])
		sets := regexp.MustCompile(`var ktmlKeys\d+ rt\.(String|Integer)Keys\n`).FindAllString(generated, -1)
		if len(sets) != repetitions {
			t.Errorf("%s declares %d key sets, want one per repetition (%d)", name, len(sets), repetitions)
		}
		if resets := strings.Count(generated, ".Reset(len(ktmlItems"); resets != repetitions {
			t.Errorf("%s resets %d key sets, want %d", name, resets, repetitions)
		}
		for _, buffer := range []string{"bytes.Buffer", "strings.Builder", "bufio.", "[]byte"} {
			if strings.Contains(generated, buffer) {
				t.Errorf("%s buffers output with %s", name, buffer)
			}
		}
		for _, preflight := range regexp.MustCompile(`(?s)for ktmlIndex\d+ := range ktmlItems\d+ \{\n\t*if !ktmlKeys\d+\.Add\(.*?\n\t*\}\n`).FindAllString(generated, -1) {
			if strings.Contains(preflight, "w,") || strings.Contains(preflight, "Render(") {
				t.Errorf("%s preflight writes output:\n%s", name, preflight)
			}
		}
	}
	production := string(outputs["kanban/board_ktml.go"])
	if !strings.HasPrefix(production, "//go:build !ktmldev\n") || strings.Contains(production, "ktml/dev") || strings.Contains(production, "ktml:item") || strings.Contains(production, "ctx.Value") {
		t.Errorf("production variant leaks development identity:\n%s", production)
	}
	if development := string(outputs["kanban/board_ktml_dev.go"]); !strings.HasPrefix(development, "//go:build ktmldev\n") || !strings.Contains(development, `"github.com/karl-tye/ktml/dev"`) {
		t.Errorf("development variant lacks its constraint or dev import")
	}
}

func TestGenerate_GIVENKeyedRepetitionFixture_WHENCompiledInBothVariants_THENRendersPreflightsAndMarksIdentity(t *testing.T) {
	outputs := generateBoardFixture(t)
	repository, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string][]byte{
		"go.mod": []byte("module " + fixtureModule + "\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0\nreplace github.com/karl-tye/ktml => " + filepath.ToSlash(repository) + "\n"),
	}
	for _, handwritten := range []string{"kanban/board_test.go", "kanban/board_prod_test.go", "kanban/board_dev_test.go"} {
		content, err := os.ReadFile(filepath.Join("testdata", "board", filepath.FromSlash(handwritten)))
		if err != nil {
			t.Fatal(err)
		}
		files[handwritten] = content
	}
	for name, content := range outputs {
		files[name] = content
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) string {
		t.Helper()
		command := exec.Command("go", args...)
		command.Dir = dir
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
		}
		return string(output)
	}
	run("vet", "./...")
	run("test", "-count=1", "./...")
	run("vet", "-tags", "ktmldev", "./...")
	run("test", "-count=1", "-tags", "ktmldev", "./...")
	if deps := run("list", "-deps", "./kanban"); strings.Contains(deps, "github.com/karl-tye/ktml/dev") {
		t.Errorf("production build links ktml/dev:\n%s", deps)
	}
	if deps := run("list", "-deps", "-tags", "ktmldev", "./kanban"); !strings.Contains(deps, "github.com/karl-tye/ktml/dev") {
		t.Errorf("development build does not link ktml/dev")
	}
}

func TestGenerate_GIVENRepetitionWithoutSnapshot_WHENGenerated_THENFailsWithoutOutput(t *testing.T) {
	result := compiler.CompileSyntax("list.ktml", []byte(`List [label: string] { for item in label key item {} }`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() = %v", result.Err())
	}
	generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
	if err == nil || !strings.Contains(err.Error(), "requires a workspace snapshot") || generated != nil {
		t.Fatalf("Generate() = %q, %v", generated, err)
	}
}
