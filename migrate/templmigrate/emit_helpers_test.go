package templmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpers_GIVENAttributesMapWithURLAndScript_WHENMigrated_THENURLsGoThroughThePolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a module")
	}
	dir := t.TempDir()
	ktmlRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":     "module example.com/helpers\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0-00010101000000-000000000000\n\nreplace github.com/karl-tye/ktml => " + ktmlRoot + "\n",
		"helpers.go": string(helperFile("helpers", map[string]bool{"attributes": true})),
		"helpers_test.go": `package helpers

import (
	"bytes"
	"testing"

	"github.com/karl-tye/ktml/rt"
)

func TestMigrateAttributes(t *testing.T) {
	bag := migrateAttributes(map[string]any{"href": "javascript:alert(1)", "src": "/a.png", "title": "t", "@click": "go()", "hidden": true})
	var out bytes.Buffer
	w, owned := rt.Wrap(&out)
	if err := rt.Finish(w, owned, rt.Attributes(w, bag, nil)); err != nil {
		t.Fatal(err)
	}
	want := ` + "`" + ` @click="go()" hidden href="about:invalid#ktml-unsafe-url" src="/a.png" title="t"` + "`" + `
	if got := out.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goCommand(t, dir, "test", "./...")
}

func TestHelpers_GIVENStandardAndModuleImports_WHENWritten_THENTheyAreGroupedAsGoimportsDoes(t *testing.T) {
	got := string(helperFile("ui", map[string]bool{"attributes": true, "classes": true}))
	want := "import (\n\t\"fmt\"\n\t\"sort\"\n\t\"strings\"\n\n\t\"github.com/karl-tye/ktml\"\n)\n"
	if !strings.Contains(got, want) {
		t.Errorf("imports are not grouped:\n%s", got)
	}
}

func TestHelpers_GIVENTemplStylesAndJSONScripts_WHENMigrated_THENTheyRenderAsTemplWroteThem(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a module")
	}
	dir := t.TempDir()
	ktmlRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":     "module example.com/helpers\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0-00010101000000-000000000000\n\nreplace github.com/karl-tye/ktml => " + ktmlRoot + "\n",
		"helpers.go": string(helperFile("helpers", map[string]bool{"style": true, "jsonscript": true})),
		"helpers_test.go": `package helpers

import "testing"

func TestTemplSemantics(t *testing.T) {
	// templ's style sanitizer trims a string and ends it with a semicolon.
	for in, want := range map[string]string{"width: 5%": "width: 5%;", " height: 1px; ": "height: 1px;", "": ""} {
		if got := migrateStyle(in).String(); got != want {
			t.Errorf("migrateStyle(%q) = %q, want %q", in, got, want)
		}
	}
	// templ.JSONScript encodes with json.Encoder: HTML-escaped, newline-terminated.
	if got := migrateJSONScript(map[string]string{"a": "</script>"}).String(); got != "{\"a\":\"\\u003c/script\\u003e\"}\n" {
		t.Errorf("migrateJSONScript = %q", got)
	}
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goCommand(t, dir, "test", "./...")
}
