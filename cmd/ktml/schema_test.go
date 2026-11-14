package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func writeSchemaProject(t *testing.T, files map[string]string) {
	t.Helper()
	t.Chdir(t.TempDir())
	files["go.mod"] = "module example.com/app\n\ngo 1.26.3\n"
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGenerateFile_GIVENHostSchemas_WHENGenerated_THENPublishesCompilingRecordPackages(t *testing.T) {
	writeSchemaProject(t, map[string]string{
		"people/person.ktmlschema": "record Person [id: string, age?: integer]",
		"web/user.ktmlschema":      "import { Person } from \"../people/person.ktmlschema\"\nrecord User [owner: Person, tags: [string]]",
	})
	if err := os.WriteFile("web/handler.go", []byte("package handlers\n\nimport \"example.com/app/people\"\n\nvar _ = User{Owner: people.Person{Id: \"1\"}, Tags: nil}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"people/person.ktmlschema", "web/user.ktmlschema"} {
		if err := generateFile(file, false); err != nil {
			t.Fatalf("generateFile(%s) error = %v", file, err)
		}
	}
	generated := string(mustReadFile(t, "web/user_ktmlschema.go"))
	if !strings.Contains(generated, "package handlers") || !strings.Contains(generated, "Owner people.Person") {
		t.Fatalf("generated user schema:\n%s", generated)
	}
	command := exec.Command("go", "vet", "./...")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated packages do not compile: %v\n%s", err, output)
	}
	state := string(mustReadFile(t, filepath.Join(".ktml", "state.json")))
	if !strings.Contains(state, "web/user_ktmlschema.go") {
		t.Fatalf("build state does not record the schema output:\n%s", state)
	}
}

func TestGenerateFile_GIVENInvalidOrCollidingSchema_WHENGenerated_THENFailsWithoutOutput(t *testing.T) {
	tests := []struct {
		name, schema, handwritten, want string
	}{
		{name: "semantic error", schema: "record User [id: Missing]", want: "unknown-type"},
		{name: "handwritten collision", schema: "record User [id: string]", handwritten: "package app\n\ntype User struct{}\n", want: "User (declared in user.go)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"app/user.ktmlschema": tt.schema}
			if tt.handwritten != "" {
				files["app/user.go"] = tt.handwritten
			}
			writeSchemaProject(t, files)
			err := generateFile("app/user.ktmlschema", false)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("generateFile() error = %v, want %q", err, tt.want)
			}
			if _, statErr := os.Stat("app/user_ktmlschema.go"); !os.IsNotExist(statErr) {
				t.Fatalf("output exists after failure: %v", statErr)
			}
			if _, statErr := os.Stat(filepath.Join(".ktml", "state.json")); !os.IsNotExist(statErr) {
				t.Fatalf("build state written after failure: %v", statErr)
			}
		})
	}
}

func TestGenerateFile_GIVENStateSchema_WHENGenerated_THENPublishesCompilingStatePackage(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	writeSchemaProject(t, map[string]string{
		"billing/payment.ktmlschema": "state Payment { Unpaid, Paid [receipt: URL] }",
		"billing/use.go":             "package billing\n\nimport \"github.com/karl-tye/ktml\"\n\nvar _ = NewPaymentPaid(PaymentPaid{Receipt: ktml.NewURL(\"/r\")}).Variant() == PaymentVariantPaid\n",
	})
	goMod := "module example.com/app\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0\nreplace github.com/karl-tye/ktml => " + filepath.ToSlash(repository) + "\n"
	if err := os.WriteFile("go.mod", []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := generateFile("billing/payment.ktmlschema", false); err != nil {
		t.Fatalf("generateFile() error = %v", err)
	}
	if output, err := exec.Command("go", "vet", "./...").CombinedOutput(); err != nil {
		t.Fatalf("generated state package does not compile: %v\n%s", err, output)
	}
}

func TestGenerateFile_GIVENStateCollidingWithHandwrittenGo_WHENGenerated_THENFailsWithoutOutput(t *testing.T) {
	for _, handwritten := range []string{"func NewPaymentPaid() {}\n", "type ktmlPaymentUnpaid struct{}\n", "type PaymentVariant int\n"} {
		writeSchemaProject(t, map[string]string{
			"billing/payment.ktmlschema": "state Payment { Unpaid, Paid [receipt: string] }",
			"billing/handwritten.go":     "package billing\n\n" + handwritten,
		})
		err := generateFile("billing/payment.ktmlschema", false)
		if err == nil || !strings.Contains(err.Error(), "collide") {
			t.Fatalf("generateFile() error = %v, want collision for %q", err, handwritten)
		}
		if _, statErr := os.Stat("billing/payment_ktmlschema.go"); !os.IsNotExist(statErr) {
			t.Fatalf("output written despite collision: %v", statErr)
		}
	}
}

func TestReportWarnings_GIVENDefaultFallthrough_WHENReported_THENPrintsLocatedWarning(t *testing.T) {
	writeSchemaProject(t, map[string]string{
		"web/payment.ktmlschema": "state Payment { Unpaid, Paid }",
		"web/badge.ktml":         "import { Payment } from \"./payment.ktmlschema\"\nBadge [payment: Payment] default { span }\nBadge [payment = Paid] { b }\n",
	})
	path, _ := filepath.Abs("web/badge.ktml")
	snapshot, err := compiler.NewWorkspace(filepath.Dir(path)).Load(path)
	if err != nil || !snapshot.Valid() {
		t.Fatalf("Load() = %v, %v", err, snapshot.Err())
	}
	var output strings.Builder
	reportWarnings(&output, snapshot, path)
	want := path + ":2:26: warning [default-fallthrough] default body of Badge handles Unpaid\n"
	if output.String() != want {
		t.Fatalf("reportWarnings() = %q, want %q", output.String(), want)
	}
}
