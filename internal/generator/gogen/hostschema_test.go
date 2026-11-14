package gogen_test

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

var update = flag.Bool("update", false, "rewrite golden generated Go")

const fixtureModule = "example.com/fixture"

// fixturePackages maps a fixture directory to its Go package in the temporary
// fixture module.
func fixturePackages(root string) func(string) (gogen.GoPackage, error) {
	return func(dir string) (gogen.GoPackage, error) {
		relative, err := filepath.Rel(root, dir)
		if err != nil {
			return gogen.GoPackage{}, err
		}
		return gogen.GoPackage{ImportPath: fixtureModule + "/" + filepath.ToSlash(relative), Name: filepath.Base(dir)}, nil
	}
}

func generateHostSchemaFixture(t *testing.T) map[string][]byte {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "hostschema"))
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
	options := func(dir string) gogen.Options {
		return gogen.Options{Package: filepath.Base(dir), Packages: fixturePackages(root)}
	}
	outputs := make(map[string][]byte)
	for _, schema := range []string{"people/person.ktmlschema", "app/user.ktmlschema"} {
		path := filepath.Join(root, schema)
		generated, mappings, err := gogen.GenerateSchema(snapshot, path, options(filepath.Dir(path)))
		if err != nil {
			t.Fatalf("GenerateSchema(%s) error = %v", schema, err)
		}
		if len(mappings) == 0 {
			t.Fatalf("GenerateSchema(%s) returned no source map", schema)
		}
		outputs[strings.TrimSuffix(schema, ".ktmlschema")+"_ktmlschema.go"] = generated
	}
	for _, document := range []string{"app/badge.ktml", "app/card.ktml"} {
		path := filepath.Join(root, document)
		generated, err := gogen.GenerateFile(snapshot, path, options(filepath.Dir(path)))
		if err != nil {
			t.Fatalf("GenerateFile(%s) error = %v", document, err)
		}
		outputs[strings.TrimSuffix(document, ".ktml")+"_ktml.go"] = generated
	}
	return outputs
}

func TestGenerate_GIVENHostSchemaFixture_WHENGenerated_THENMatchesGoldenAndIsDeterministic(t *testing.T) {
	first := generateHostSchemaFixture(t)
	second := generateHostSchemaFixture(t)
	for name, content := range first {
		if !bytes.Equal(content, second[name]) {
			t.Errorf("%s is not deterministic", name)
		}
		golden := filepath.Join("testdata", "hostschema", "golden", filepath.FromSlash(name)+".golden")
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
			t.Fatalf("read golden %s: %v (run go test -run HostSchema -update)", golden, err)
		}
		if !bytes.Equal(content, want) {
			t.Errorf("%s differs from %s:\n%s", name, golden, content)
		}
	}
}

func TestGenerate_GIVENHostSchemaFixture_WHENCompiledAcrossPackages_THENRendersTypedPaths(t *testing.T) {
	outputs := generateHostSchemaFixture(t)
	repository, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string][]byte{
		"go.mod": []byte("module " + fixtureModule + "\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0\nreplace github.com/karl-tye/ktml => " + filepath.ToSlash(repository) + "\n"),
		"app/render_test.go": []byte(`package app

import (
	"context"
	"strings"
	"testing"

	"example.com/fixture/people"
)

func TestRender(t *testing.T) {
	user := User{
		Id: "u<1>", Name: "Ada & Co", Visits: -42, Admin: true,
		Address: Address{City: "Paris"},
		Owner:   people.Person{Id: "p1", DisplayName: "Grace"},
		History: [][]Address{{{City: "Rome"}}},
	}
	var output strings.Builder
	if err := (Card{User: user, Places: nil}).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	want := "<article id=\"u&lt;1&gt;\" data-admin data-visits=\"-42\"><h2>Ada &amp; Co &lt;Grace&gt;</h2>" +
		"<span class=\"badge\" data-count=\"-42\">Ada &amp; Co: -42</span>" +
		"<span class=\"badge\" data-count=\"1\">static: 1</span>" +
		"<span>Paris</span></article>"
	if got := output.String(); got != want {
		t.Fatalf("render = %q\nwant     %q", got, want)
	}
	var _ *int64 = people.Person{}.Age
	var _ *string = user.Nickname
	var _ *people.Person = user.Backup
	var _ []string = user.Tags
	var _ *[]string = user.Aliases
}
`),
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
	for _, args := range [][]string{{"vet", "./..."}, {"test", "./..."}} {
		command := exec.Command("go", args...)
		command.Dir = dir
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
}

func TestGenerate_GIVENRecordInputWithoutSnapshot_WHENGenerated_THENFailsWithoutOutput(t *testing.T) {
	result := compiler.CompileSyntax("card.ktml", []byte(`Card [user: User] { div }`))
	if result.HasErrors() {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
	if err == nil || generated != nil || !strings.Contains(err.Error(), "without a workspace snapshot") {
		t.Fatalf("Generate() = %q, %v; want fatal error and no output", generated, err)
	}
}

func TestGenerateSchema_GIVENCrossPackageRecordWithoutResolver_WHENGenerated_THENFailsWithoutOutput(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("testdata", "hostschema"))
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil || !snapshot.Valid() {
		t.Fatalf("Index() = %v, %v", err, snapshot.Err())
	}
	generated, _, err := gogen.GenerateSchema(snapshot, filepath.Join(root, "app", "user.ktmlschema"), gogen.Options{Package: "app"})
	if err == nil || generated != nil || !strings.Contains(err.Error(), "Options.Packages") {
		t.Fatalf("GenerateSchema() = %q, %v; want missing resolver error", generated, err)
	}
}
