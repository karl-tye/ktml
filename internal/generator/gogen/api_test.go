package gogen_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

var apis = []gogen.API{gogen.APIStruct, gogen.APIDirect}

// generateAPIFixture generates both production documents of the two-package
// ADR-0003 API fixture with one candidate API.
func generateAPIFixture(t *testing.T, api gogen.API) map[string][]byte {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "apis"))
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
	outputs := make(map[string][]byte)
	for _, name := range []string{"ui/ui.ktml", "page/page.ktml"} {
		dir := filepath.Dir(name)
		options := gogen.Options{Package: dir, Packages: fixturePackages(root), API: api}
		generated, err := gogen.GenerateFile(snapshot, filepath.Join(root, filepath.FromSlash(name)), options)
		if err != nil {
			t.Fatalf("GenerateFile(%s, %s) error = %v", name, api, err)
		}
		outputs[strings.TrimSuffix(name, ".ktml")+"_ktml.go"] = generated
	}
	return outputs
}

func TestGenerate_GIVENEachAPI_WHENGeneratedTwice_THENIsByteIdentical(t *testing.T) {
	for _, api := range apis {
		first, second := generateAPIFixture(t, api), generateAPIFixture(t, api)
		for name, content := range first {
			if !bytes.Equal(content, second[name]) {
				t.Errorf("%s %s is not deterministic", api, name)
			}
		}
	}
}

func TestGenerate_GIVENEachAPI_WHENInspected_THENHasTheCandidateShapeAndNoForbiddenConstructs(t *testing.T) {
	tests := []struct {
		api  gogen.API
		want []string
		deny []string
	}{
		{api: gogen.APIStruct, want: []string{
			"type Card struct {",
			"func (component Card) Render(ctx context.Context, w io.Writer) error {",
			"(ui.Card{Title: props.Heading",
			"}).Render(ctx, w); err != nil",
			"ktmlRenderPanel(ctx, w, &Panel{Label: \"inner\"",
		}, deny: []string{"CardProps", "ktmlCardComponent"}},
		{api: gogen.APIDirect, want: []string{
			"type CardProps struct {",
			"Children ktml.ComponentFunc",
			"func RenderCard(ctx context.Context, w io.Writer, props CardProps) error {",
			"ui.RenderCard(ctx, w, ui.CardProps{Title: props.Heading",
		}, deny: []string{"ktmlCardComponent"}},
	}
	for _, tt := range tests {
		t.Run(tt.api.String(), func(t *testing.T) {
			outputs := generateAPIFixture(t, tt.api)
			all := string(stripDirectives(t, outputs["ui/ui_ktml.go"])) + string(stripDirectives(t, outputs["page/page_ktml.go"]))
			for _, want := range tt.want {
				if !strings.Contains(all, want) {
					t.Errorf("generated code lacks %q", want)
				}
			}
			for _, forbidden := range append(tt.deny, `"reflect"`, "map[", "context.WithValue", "ctx.Value", "templ", "ktml/dev", "WithChildren", "bufio") {
				if strings.Contains(all, forbidden) {
					t.Errorf("generated code contains %q", forbidden)
				}
			}
			// Adjacent static markup is one write.
			if !strings.Contains(all, `rt.Raw(w, "</p></div></div><footer><button type=\"button\">Save</button></footer></article></section>")`) &&
				!strings.Contains(all, `rt.Raw(w, "<button type=\"button\">Save</button>")`) {
				t.Errorf("static markup is not coalesced:\n%s", all)
			}
		})
	}
}

func TestGenerate_GIVENHybridAndRenderInput_WHENGenerated_THENFails(t *testing.T) {
	document := parseDocument(t, "Card [render: string] {\n\tp \"{render}\"\n}\n")
	if _, err := gogen.Generate(document, gogen.Options{Package: "ui", API: gogen.APIStruct}); err == nil || !strings.Contains(err.Error(), "Render method") {
		t.Fatalf("Generate() error = %v, want the Render collision", err)
	}
	if _, err := gogen.Generate(document, gogen.Options{Package: "ui", API: gogen.APIDirect}); err != nil {
		t.Fatalf("Generate(B) error = %v", err)
	}
}

func TestOptions_GIVENZeroAPI_WHENGenerated_THENIsHybridH(t *testing.T) {
	var options gogen.Options
	if options.API != gogen.APIStruct || options.API.String() != "H" {
		t.Fatalf("zero API = %s, want H", options.API)
	}
}

func TestGenerate_GIVENEachAPI_WHENCompiledAndRun_THENRendersIdenticalBytesAndTypedErrors(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, api := range apis {
		t.Run(api.String(), func(t *testing.T) {
			dir := t.TempDir()
			files := map[string][]byte{
				"go.mod": []byte("module " + fixtureModule + "\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0\nreplace github.com/karl-tye/ktml => " + filepath.ToSlash(repository) + "\n"),
			}
			for _, handwritten := range []string{"page/shared_test.go", "page/" + strings.ToLower(api.String()) + "_test.go"} {
				content, err := os.ReadFile(filepath.Join("testdata", "apis", filepath.FromSlash(handwritten)))
				if err != nil {
					t.Fatal(err)
				}
				files[handwritten] = content
			}
			for name, content := range generateAPIFixture(t, api) {
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
			for _, args := range [][]string{{"vet", "./..."}, {"test", "-count=1", "./..."}} {
				command := exec.Command("go", args...)
				command.Dir = dir
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
				}
			}
		})
	}
}

func parseDocument(t *testing.T, source string) *ast.Document {
	t.Helper()
	result := compiler.CompileSyntax("fixture.ktml", []byte(source))
	if result.HasErrors() {
		t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
	}
	return result.Document
}
