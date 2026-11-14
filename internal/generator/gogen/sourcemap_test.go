package gogen_test

import (
	"bytes"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

// stripDirectives returns generated Go without its source map, for tests
// that inspect the code's shape.
func stripDirectives(t *testing.T, generated []byte) []byte {
	t.Helper()
	stripped, err := gogen.StripLineDirectives(generated)
	if err != nil {
		t.Fatalf("StripLineDirectives() error = %v", err)
	}
	return stripped
}

// writeSourceMapModule writes a module whose package holds a Host Schema, a
// component, and their generated Go, plus extra handwritten Go files. It
// returns the package directory and the snapshot it was generated from.
func writeSourceMapModule(t *testing.T, extra map[string]string) (string, *compiler.Snapshot) {
	t.Helper()
	dir := t.TempDir()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": "module example.com/views\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0\nreplace github.com/karl-tye/ktml => " + filepath.ToSlash(root) + "\n",
		"view.ktmlschema": `record CardView [
	title: string,
	count: integer,
	note?: string,
]
`,
		"view.ktml": `import { CardView } from "./view.ktmlschema"

Card [view: CardView, label: string = "Card"] {
	article class="card" {
		h2 "{view.title}"
		p "Count: {view.count}"
		if view.note? {
			small "{view.note}"
		}
	}
}

Cards [first: CardView] {
	Card view="{first}" label="First"
}
`,
	}
	for name, content := range extra {
		files[name] = content
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := compiler.NewWorkspace(dir).Index()
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if err := snapshot.Err(); err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	options := gogen.Options{Package: "views"}
	schema, _, err := gogen.GenerateSchema(snapshot, filepath.Join(dir, "view.ktmlschema"), options)
	if err != nil {
		t.Fatalf("GenerateSchema() error = %v", err)
	}
	component, err := gogen.GenerateFile(snapshot, filepath.Join(dir, "view.ktml"), options)
	if err != nil {
		t.Fatalf("GenerateFile() error = %v", err)
	}
	for name, content := range map[string][]byte{"view_ktmlschema.go": schema, "view_ktml.go": component} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, snapshot
}

// generatedPosition matches an error reported at a generated Go file.
var generatedPosition = regexp.MustCompile(`(?m)^(vet: )?\S*_ktml(schema|_dev)?\.go:\d+`)

func goCommand(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	command := exec.Command("go", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestGenerate_GIVENGeneratedGo_WHENBuiltAndVetted_THENCompilesCleanly(t *testing.T) {
	dir, _ := writeSourceMapModule(t, nil)
	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}} {
		if output, err := goCommand(t, dir, args...); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	if output, err := exec.Command("gofmt", "-l", dir).CombinedOutput(); err != nil || len(strings.TrimSpace(string(output))) > 0 {
		t.Fatalf("generated Go is not gofmt-clean: %v\n%s", err, output)
	}
}

// TestGenerate_GIVENGoErrorsInGeneratedCode_WHENBuilt_THENGoReportsKTMLPositions
// introduces Go errors that the type checker reports in generated files:
// handwritten Go that redeclares a generated component, and a presenter
// method that collides with a generated record field.
func TestGenerate_GIVENGoErrorsInGeneratedCode_WHENBuilt_THENGoReportsKTMLPositions(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]string
		want  []string
	}{
		{
			name:  "a Go type redeclares a component",
			extra: map[string]string{"card.go": "package views\n\ntype Card struct{}\n"},
			// card.go sorts before view_ktml.go, so the generated declaration
			// is the duplicate; the component is declared at view.ktml:3:1.
			want: []string{"view.ktml:3:2: Card redeclared in this block", "card.go:3:6: other declaration of Card", "view.ktml:3:29: props.Label undefined", "view.ktml:14:7: unknown field View in struct literal"},
		},
		{
			name:  "a presenter method collides with a record field",
			extra: map[string]string{"present.go": "package views\n\nfunc (v CardView) Title() string { return v.Title }\n"},
			want:  []string{"present.go:3:19: field and method with the same name Title", "view.ktmlschema:2:2", "other declaration of Title", "view.ktml:5:7: cannot use props.View.Title"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, _ := writeSourceMapModule(t, tt.extra)
			// go vet stops at the first type error.
			for _, check := range []struct {
				args []string
				want []string
			}{{[]string{"build", "./..."}, tt.want}, {[]string{"vet", "./..."}, tt.want[:1]}} {
				command := "go " + strings.Join(check.args, " ")
				output, err := goCommand(t, dir, check.args...)
				if err == nil {
					t.Fatalf("%s succeeded; want a type error", command)
				}
				for _, want := range check.want {
					if !strings.Contains(output, want) {
						t.Errorf("%s output lacks %q:\n%s", command, want, output)
					}
				}
				if generatedPosition.MatchString(output) {
					t.Errorf("%s reports an error at a generated-file position:\n%s", command, output)
				}
			}
		})
	}
}

// TestGenerate_GIVENSourceMap_WHENDirectivesAreRead_THENEachMappedTokenIsAtItsSource
// reads the generated files' line directives as the Go toolchain does: the
// token each mapping names must be positioned at the start of its KTML
// range. A construct at column 1 is reported at column 2, because gofmt
// keeps a space after the directive.
func TestGenerate_GIVENSourceMap_WHENDirectivesAreRead_THENEachMappedTokenIsAtItsSource(t *testing.T) {
	dir, snapshot := writeSourceMapModule(t, nil)
	for _, source := range []string{"view.ktml", "view.ktmlschema"} {
		path := filepath.Join(dir, source)
		var generated []byte
		var mappings []gogen.SourceMapping
		var err error
		if compiler.IsSchemaPath(path) {
			generated, mappings, err = gogen.GenerateSchema(snapshot, path, gogen.Options{Package: "views"})
		} else {
			generated, mappings, err = gogen.GenerateFileMap(snapshot, path, gogen.Options{Package: "views"})
		}
		if err != nil {
			t.Fatal(err)
		}
		again, againMappings, err := generateMapped(snapshot, path)
		if err != nil || !bytes.Equal(again, generated) || len(againMappings) != len(mappings) {
			t.Fatalf("%s: regeneration is not byte-stable (err %v)", source, err)
		}
		if len(mappings) < 4 {
			t.Fatalf("%s: %d mappings; want every construct mapped", source, len(mappings))
		}
		files := token.NewFileSet()
		file := files.AddFile(gogen.OutputFile(path, false), -1, len(generated))
		var s scanner.Scanner
		s.Init(file, generated, nil, scanner.ScanComments)
		for {
			if _, kind, _ := s.Scan(); kind == token.EOF {
				break
			}
		}
		text := snapshot.Source(path)
		for _, mapping := range mappings {
			if mapping.Source.File != path {
				t.Errorf("%s: mapping names %s", source, mapping.Source.File)
				continue
			}
			got := files.Position(file.Pos(mapping.Go))
			line := 1 + bytes.Count(text[:mapping.Source.Start], []byte("\n"))
			column := mapping.Source.Start - bytes.LastIndexByte(text[:mapping.Source.Start], '\n')
			wantColumn := max(column, 2)
			if filepath.Base(got.Filename) != source || got.Line != line || got.Column != wantColumn && got.Column != column {
				t.Errorf("%s: token %q at offset %d is positioned at %s; want %s:%d:%d (%q)", source, firstWord(generated[mapping.Go:]), mapping.Go, got, source, line, column, text[mapping.Source.Start:mapping.Source.End])
			}
		}
	}
}

func generateMapped(snapshot *compiler.Snapshot, path string) ([]byte, []gogen.SourceMapping, error) {
	if compiler.IsSchemaPath(path) {
		return gogen.GenerateSchema(snapshot, path, gogen.Options{Package: "views"})
	}
	return gogen.GenerateFileMap(snapshot, path, gogen.Options{Package: "views"})
}

func firstWord(content []byte) string {
	end := bytes.IndexAny(content, " ,()\n")
	if end < 0 {
		return string(content)
	}
	return string(content[:end])
}

// TestGenerate_GIVENEveryFixture_WHENDirectivesAreRead_THENEachMappedTokenIsAtItsSource
// checks the source map of every fixture document, in both variants: each
// mapped token is positioned at its KTML range by the directives, and the
// directives name only the document itself.
func TestGenerate_GIVENEveryFixture_WHENDirectivesAreRead_THENEachMappedTokenIsAtItsSource(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("testdata", "*"))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, fixture := range fixtures {
		root, err := filepath.Abs(fixture)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := compiler.NewWorkspace(root).Index()
		if err != nil || snapshot.Err() != nil {
			t.Fatalf("%s: index: %v %v", fixture, err, snapshot.Err())
		}
		err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || compiler.IsStoryPath(path) || !strings.HasSuffix(path, ".ktml") && !compiler.IsSchemaPath(path) {
				return err
			}
			for _, development := range []bool{false, true} {
				options := gogen.Options{Package: filepath.Base(filepath.Dir(path)), Packages: fixturePackages(root), Development: development}
				var generated []byte
				var mappings []gogen.SourceMapping
				if compiler.IsSchemaPath(path) {
					if development {
						continue
					}
					generated, mappings, err = gogen.GenerateSchema(snapshot, path, options)
				} else {
					generated, mappings, err = gogen.GenerateFileMap(snapshot, path, options)
				}
				if err != nil {
					return err
				}
				checkDirectives(t, snapshot, path, gogen.OutputFile(path, development), generated, mappings)
				checked++
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: %v", fixture, err)
		}
	}
	if checked < 10 {
		t.Fatalf("checked %d generated files; want every fixture document", checked)
	}
}

// checkDirectives reads generated Go's line directives as the Go toolchain
// does and checks every mapping against them.
func checkDirectives(t *testing.T, snapshot *compiler.Snapshot, path, target string, generated []byte, mappings []gogen.SourceMapping) {
	t.Helper()
	source := filepath.Base(path)
	if len(mappings) == 0 {
		t.Errorf("%s: no mappings", source)
		return
	}
	files := token.NewFileSet()
	file := files.AddFile(target, -1, len(generated))
	var s scanner.Scanner
	s.Init(file, generated, nil, scanner.ScanComments)
	for {
		if _, kind, literal := s.Scan(); kind == token.EOF {
			break
		} else if kind == token.COMMENT && strings.HasPrefix(literal, "/*line ") {
			name := strings.SplitN(strings.TrimPrefix(literal, "/*line "), ":", 2)[0]
			if name != source && name != filepath.Base(target) {
				t.Errorf("%s: directive %s names another file", source, literal)
			}
		}
	}
	text := snapshot.Source(path)
	for _, mapping := range mappings {
		got := files.Position(file.Pos(mapping.Go))
		line := 1 + bytes.Count(text[:mapping.Source.Start], []byte("\n"))
		column := mapping.Source.Start - bytes.LastIndexByte(text[:mapping.Source.Start], '\n')
		// A directive's column cannot be below 1, so a construct within the
		// first two columns may be reported up to two columns late.
		if mapping.Source.File != path || filepath.Base(got.Filename) != source || got.Line != line || got.Column != column && (column > 2 || got.Column > 3) {
			t.Errorf("%s: token %q at offset %d is positioned at %s; want %s:%d:%d (%q)", source, firstWord(generated[mapping.Go:]), mapping.Go, got, source, line, column, text[mapping.Source.Start:mapping.Source.End])
		}
	}
}
