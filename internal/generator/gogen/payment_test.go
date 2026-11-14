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

// generatePaymentFixture generates the shop checkout payment proof:
// Presentation States in billing and Render Clause components in web.
func generatePaymentFixture(t *testing.T) (map[string][]byte, *compiler.Snapshot) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "payment"))
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
	schema := filepath.Join(root, "billing", "payment.ktmlschema")
	generated, mappings, err := gogen.GenerateSchema(snapshot, schema, options(filepath.Dir(schema)))
	if err != nil {
		t.Fatalf("GenerateSchema() error = %v", err)
	}
	source := snapshot.Source(schema)
	for _, want := range []struct{ goName, declaration string }{
		{"StaffPayment", "StaffPayment"},
		{"StaffPaymentVariantPayable", "Payable"},
		{"StaffPaymentPayable", "Payable"},
		{"CardAction", "cardAction"},
		{"Money", "Money"},
		{"Display", "display"},
	} {
		found := false
		for _, mapping := range mappings {
			found = found || mapping.Source.File == schema &&
				string(source[mapping.Source.Start:mapping.Source.End]) == want.declaration &&
				strings.HasPrefix(string(generated[mapping.Go:]), want.goName+" ")
		}
		if !found {
			t.Errorf("source map lacks %s from %s", want.goName, want.declaration)
		}
	}
	outputs["billing/payment_ktmlschema.go"] = generated
	document := filepath.Join(root, "web", "payment.ktml")
	generated, err = gogen.GenerateFile(snapshot, document, options(filepath.Dir(document)))
	if err != nil {
		t.Fatalf("GenerateFile() error = %v", err)
	}
	outputs["web/payment_ktml.go"] = generated
	return outputs, snapshot
}

func TestGenerate_GIVENPaymentFixture_WHENGenerated_THENMatchesGoldenAndWarnsOnlyAboutTheDefault(t *testing.T) {
	first, snapshot := generatePaymentFixture(t)
	second, _ := generatePaymentFixture(t)
	for name, content := range first {
		if !bytes.Equal(content, second[name]) {
			t.Errorf("%s is not deterministic", name)
		}
		golden := filepath.Join("testdata", "payment", "golden", filepath.FromSlash(name)+".golden")
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
			t.Fatalf("read golden %s: %v (run go test -run Payment -update)", golden, err)
		}
		if !bytes.Equal(content, want) {
			t.Errorf("%s differs from %s:\n%s", name, golden, content)
		}
	}
	root, _ := filepath.Abs(filepath.Join("testdata", "payment"))
	diagnostics := snapshot.Diagnostics(filepath.Join(root, "web", "payment.ktml"))
	if len(diagnostics) != 1 || diagnostics[0].Code != "default-fallthrough" || diagnostics[0].Severity != compiler.SeverityWarning || !strings.Contains(diagnostics[0].Message, "NotDue, Payable, Confirming, Retryable") {
		t.Errorf("diagnostics = %#v", diagnostics)
	}
}

func TestGenerate_GIVENPaymentFixture_WHENCompiledWithPresenters_THENRendersEveryStateAndRejectsInvalidOnes(t *testing.T) {
	outputs, _ := generatePaymentFixture(t)
	repository, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string][]byte{
		"go.mod": []byte("module " + fixtureModule + "\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0\nreplace github.com/karl-tye/ktml => " + filepath.ToSlash(repository) + "\n"),
	}
	for _, handwritten := range []string{"web/presenter.go", "web/payment_test.go"} {
		content, err := os.ReadFile(filepath.Join("testdata", "payment", filepath.FromSlash(handwritten)))
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
	for _, args := range [][]string{{"vet", "./..."}, {"test", "-count=1", "./..."}} {
		command := exec.Command("go", args...)
		command.Dir = dir
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
}

func TestGenerate_GIVENStatesOrClausesWithoutSnapshot_WHENGenerated_THENFailsWithoutOutput(t *testing.T) {
	tests := []struct{ name, source, want string }{
		{"state", "state S { A }", "state S requires a workspace snapshot"},
		{"bodyless signature", "Card [on: boolean]\nCard [on = true] {}\nCard [on = false] {}", "component signature Card requires a workspace snapshot"},
		{"default signature", "Card [on: boolean] default { div }\nCard [on = true] {}", "component signature Card requires a workspace snapshot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compiler.CompileSyntax("card.ktml", []byte(tt.source))
			if result.HasErrors() {
				t.Fatalf("diagnostics = %#v", result.Diagnostics)
			}
			generated, err := gogen.Generate(result.Document, gogen.Options{Package: "generated"})
			if err == nil || generated != nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Generate() = %q, %v; want %q and no output", generated, err, tt.want)
			}
		})
	}
}
