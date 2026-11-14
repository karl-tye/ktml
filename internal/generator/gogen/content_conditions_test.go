package gogen_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

// generateGapsFixture generates the content-input and condition fixture: its
// schema and both variants of its component document.
func generateGapsFixture(t *testing.T) map[string][]byte {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "gaps"))
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
	dir := filepath.Join(root, "ui")
	options := gogen.Options{Package: "ui", Packages: fixturePackages(root)}
	outputs := make(map[string][]byte)
	schema, _, err := gogen.GenerateSchema(snapshot, filepath.Join(dir, "ui.ktmlschema"), options)
	if err != nil {
		t.Fatalf("GenerateSchema() error = %v", err)
	}
	outputs["ui/ui_ktmlschema.go"] = schema
	production, err := gogen.GenerateFile(snapshot, filepath.Join(dir, "ui.ktml"), options)
	if err != nil {
		t.Fatalf("GenerateFile() error = %v", err)
	}
	outputs["ui/ui_ktml.go"] = production
	options.Development = true
	development, err := gogen.GenerateFile(snapshot, filepath.Join(dir, "ui.ktml"), options)
	if err != nil {
		t.Fatalf("GenerateFile(Development) error = %v", err)
	}
	outputs["ui/ui_ktml_dev.go"] = development
	return outputs
}

func TestGenerate_GIVENContentAndConditionFixture_WHENGenerated_THENMatchesGoldenDeterministically(t *testing.T) {
	first, second := generateGapsFixture(t), generateGapsFixture(t)
	for name, content := range first {
		if !bytes.Equal(content, second[name]) {
			t.Errorf("%s is not deterministic", name)
		}
		golden := filepath.Join("testdata", "gaps", "golden", filepath.FromSlash(name)+".golden")
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
			t.Fatalf("read golden %s: %v (run go test -run ContentAndCondition -update)", golden, err)
		}
		if !bytes.Equal(content, want) {
			t.Errorf("%s differs from %s:\n%s", name, golden, content)
		}
	}
}

// TestGenerate_GIVENContentAndConditionFixture_WHENInspected_THENCarriesContentInPropsOnly
// inspects the generated source: content is a ktml.Component field, never a
// context value, and the withdrawn context API is gone.
func TestGenerate_GIVENContentAndConditionFixture_WHENInspected_THENCarriesContentInPropsOnly(t *testing.T) {
	outputs := generateGapsFixture(t)
	for _, name := range []string{"ui/ui_ktml.go", "ui/ui_ktml_dev.go"} {
		generated := string(stripDirectives(t, outputs[name]))
		for _, forbidden := range []string{"WithChildren", "ktml.Children(", "ClearChildren", "context.WithValue", "ctx.Value"} {
			if strings.Contains(generated, forbidden) {
				t.Errorf("%s contains %s", name, forbidden)
			}
		}
		for _, field := range []string{"\tChildren ktml.Component\n", "\tActions  ktml.Component\n", "\tHeading ktml.Component\n"} {
			if !strings.Contains(generated, field) {
				t.Errorf("%s lacks props field %q", name, field)
			}
		}
		if !strings.Contains(generated, `if props.Children == nil {
		return rt.MissingContent("example.com/fixture/ui.Modal", "children")
	}`) {
			t.Errorf("%s does not preflight required children", name)
		}
	}
}

func TestGenerate_GIVENContentAndConditionFixture_WHENCompiledInBothVariants_THENRendersContentAndBranches(t *testing.T) {
	outputs := generateGapsFixture(t)
	repository, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string][]byte{
		"go.mod": []byte("module " + fixtureModule + "\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0\nreplace github.com/karl-tye/ktml => " + filepath.ToSlash(repository) + "\n"),
	}
	for _, handwritten := range []string{"ui/ui_test.go", "ui/ui_prod_test.go", "ui/ui_dev_test.go"} {
		content, err := os.ReadFile(filepath.Join("testdata", "gaps", filepath.FromSlash(handwritten)))
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
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("go", args...)
		command.Dir = dir
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	run("vet", "./...")
	run("test", "-count=1", "./...")
	run("vet", "-tags", "ktmldev", "./...")
	run("test", "-count=1", "-tags", "ktmldev", "./...")
}

// TestGenerate_GIVENACalleeWithItsOwnOutletCalledInsideAFill_WHENRendered_THENDoesNotRecurse
// is the recursion recorded in ADR-0003. Context-carried children made Inner
// read Layout's children from ctx and recurse without end.
func TestGenerate_GIVENACalleeWithItsOwnOutletCalledInsideAFill_WHENRendered_THENDoesNotRecurse(t *testing.T) {
	result := compiler.CompileSyntax("page.ktml", []byte("Layout { main { children } }\nInner { em { children } }\nPage { Layout { Inner } }"))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() = %v", result.Err())
	}
	generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"
)

func TestGeneratedPage(t *testing.T) {
	var output strings.Builder
	if err := (Page{}).Render(context.Background(), &output); err != nil { t.Fatal(err) }
	if got := output.String(); got != "<main><em></em></main>" { t.Fatalf("rendered %q", got) }
}
`)
}
