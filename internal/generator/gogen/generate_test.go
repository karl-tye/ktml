package gogen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

func TestGenerate_GIVENTypedComponent_WHENRendered_THENWritesEscapedDirectHTML(t *testing.T) {
	result := compiler.CompileSyntax("card.ktml", []byte(`Card [title: string, disabled: boolean] {
		input title="{title}" data-label="prefix-{title}" disabled="{disabled}"
	}`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if strings.Contains(string(generated), "html/template") {
		t.Fatal("generated renderer depends on html/template")
	}

	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"
)

func TestGeneratedCard(t *testing.T) {
	var output strings.Builder
	err := (Card{Title: "<unsafe>", Disabled: true}).Render(context.Background(), &output)
	if err != nil { t.Fatal(err) }
	if got, want := output.String(), "<input title=\"&lt;unsafe&gt;\" data-label=\"prefix-&lt;unsafe&gt;\" disabled>"; got != want {
		t.Fatalf("rendered output = %q, want %q", got, want)
	}
}
	`)
}

func TestGenerate_GIVENComponentChildren_WHENRendered_THENScopesAndComposesChildren(t *testing.T) {
	result := compiler.CompileSyntax("page.ktml", []byte(`Layout { section { children } }
Page [title: string] { Layout { h1 { "{title}" } } }`))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
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
	err := (Page{Title: "Dashboard"}).Render(context.Background(), &output)
	if err != nil { t.Fatal(err) }
	if got, want := output.String(), "<section><h1>Dashboard</h1></section>"; got != want {
		t.Fatalf("rendered output = %q, want %q", got, want)
	}
}
`)
}

func TestGenerateSnapshot_GIVENCrossFileComponents_WHENRendered_THENUsesValidatedWorkspaceGraph(t *testing.T) {
	dir := t.TempDir()
	componentsPath := filepath.Join(dir, "components.ktml")
	pagePath := filepath.Join(dir, "page.ktml")
	if err := os.WriteFile(componentsPath, []byte(`Title [text: string] { h1 { "{text}" } }`), 0o600); err != nil {
		t.Fatalf("write components: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte(`import { Title } from "components"
Page [heading: string] { main { Title text="{heading}" } }`), 0o600); err != nil {
		t.Fatalf("write page: %v", err)
	}
	snapshot, err := compiler.NewWorkspace(dir).Load(pagePath)
	if err != nil || !snapshot.Valid() {
		t.Fatalf("workspace = (%v, %v), diagnostics = %#v", snapshot, err, snapshot.Diagnostics(pagePath))
	}

	generated, err := gogen.GenerateSnapshot(snapshot, gogen.Options{Package: "generated"})
	if err != nil {
		t.Fatalf("GenerateSnapshot() error = %v", err)
	}
	compileGenerated(t, generated, `package generated

import (
	"context"
	"strings"
	"testing"
)

func TestGeneratedPage(t *testing.T) {
	var output strings.Builder
	err := (Page{Heading: "Operations"}).Render(context.Background(), &output)
	if err != nil { t.Fatal(err) }
	if got, want := output.String(), "<main><h1>Operations</h1></main>"; got != want {
		t.Fatalf("rendered output = %q, want %q", got, want)
	}
}
`)
}

func compileGenerated(t *testing.T, generated []byte, generatedTest string) {
	t.Helper()
	dir := t.TempDir()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	files := map[string]string{
		"go.mod":        "module generated\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0\nreplace github.com/karl-tye/ktml => " + filepath.ToSlash(root) + "\n",
		"input.ktml.go": string(generated),
		"input_test.go": generatedTest,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	command := exec.Command("go", "test", "./...")
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated renderer does not compile or render correctly: %v\n%s\nGenerated:\n%s", err, output, generated)
	}
}
