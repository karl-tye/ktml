package gopkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolve_GIVENModuleLayouts_WHENResolved_THENDerivesImportPathAndPackageName(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":                       "module example.com/app\n\ngo 1.26\n",
		"web/handler.go":               "package handlers\n",
		"web/external_test.go":         "package handlers_test\n",
		"web/page_ktml.go":             "package generated\n",
		"ui/my-widgets/a.ktmlschema":   "",
		"cmd/server/main.go":           "package main\n",
		"9lives/only_test_external.go": "package other_test\n",
	})
	tests := []struct {
		dir, importPath, name string
	}{
		{dir: "web", importPath: "example.com/app/web", name: "handlers"},
		{dir: "ui/my-widgets", importPath: "example.com/app/ui/my-widgets", name: "my_widgets"},
		{dir: "cmd/server", importPath: "example.com/app/cmd/server", name: "main"},
		{dir: "9lives", importPath: "example.com/app/9lives", name: "p9lives"},
		{dir: ".", importPath: "example.com/app", name: filepath.Base(root)},
	}
	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			pkg, err := Resolve(filepath.Join(root, tt.dir))
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if pkg.ImportPath != tt.importPath || (tt.dir != "." && pkg.Name != tt.name) {
				t.Errorf("Resolve() = %+v, want %s %s", pkg, tt.importPath, tt.name)
			}
		})
	}
}

func TestResolve_GIVENNoModule_WHENResolved_THENFails(t *testing.T) {
	if _, err := Resolve(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no go.mod") {
		t.Fatalf("Resolve() error = %v, want missing go.mod", err)
	}
}

func TestCheckCollisions_GIVENHandwrittenDeclarations_WHENChecked_THENReportsOnlyHandwrittenCollisions(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"user.go":            "package app\n\ntype User struct{}\nfunc NewUser() User { return User{} }\nfunc (User) Method() {}\nvar Address, other = 1, 2\n",
		"user_ktmlschema.go": "package app\n\ntype Profile struct{}\n",
		"x_test.go":          "package app_test\n\ntype Profile struct{}\n",
	})
	err := CheckCollisions(root, []string{"User", "Address", "Profile", "Method"})
	if err == nil || err.Error() != "generated Go names collide with handwritten declarations: Address (declared in user.go), User (declared in user.go)" {
		t.Fatalf("CheckCollisions() error = %v", err)
	}
	if err := CheckCollisions(root, []string{"Profile"}); err != nil {
		t.Fatalf("CheckCollisions(Profile) error = %v", err)
	}
}

func TestGenerated_GIVENGoFileNames_WHENClassified_THENRecognisesEveryGeneratorOutput(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "card_ktml.go", want: true},
		{name: "card_ktml_dev.go", want: true},
		{name: "records_ktmlschema.go", want: true},
		{name: "card.ktml.go", want: true},
		{name: "card.go", want: false},
		{name: "card_test.go", want: false},
		{name: "card_ktml_test.go", want: false},
		{name: "ktml.go", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Generated(tt.name); got != tt.want {
				t.Errorf("Generated(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestFindModule_GIVENNestedDirectories_WHENFound_THENReturnsNearestModuleRootAndPath(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":            "module example.com/app\n\ngo 1.26\n",
		"web/ui/card.ktml":  "",
		"tools/go.mod":      "module \"example.com/tools\"\n",
		"tools/gen/main.go": "package main\n",
	})
	tests := []struct {
		dir, root, modulePath string
	}{
		{dir: "web/ui", root: root, modulePath: "example.com/app"},
		{dir: ".", root: root, modulePath: "example.com/app"},
		{dir: "tools/gen", root: filepath.Join(root, "tools"), modulePath: "example.com/tools"},
	}
	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			gotRoot, gotPath, err := FindModule(filepath.Join(root, tt.dir))
			if err != nil {
				t.Fatalf("FindModule() error = %v", err)
			}
			if gotRoot != tt.root || gotPath != tt.modulePath {
				t.Errorf("FindModule() = %s %s, want %s %s", gotRoot, gotPath, tt.root, tt.modulePath)
			}
		})
	}
}

func TestResolver_GIVENADirectory_WHENResolvedRepeatedly_THENParsesItsGoOnceAndAgreesWithResolve(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":         "module example.com/app\n\ngo 1.26\n",
		"web/handler.go": "package handlers\n\nfunc Page() {}\n\ntype Props struct{}\n",
	})
	dir := filepath.Join(root, "web")
	resolver := NewResolver()
	want, err := Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := resolver.Resolve(dir); err != nil || got != want {
		t.Fatalf("Resolver.Resolve() = %+v, %v; Resolve() = %+v", got, err, want)
	}
	if err := resolver.CheckCollisions(dir, []string{"Page"}); err == nil || !strings.Contains(err.Error(), "handler.go") {
		t.Fatalf("Resolver.CheckCollisions() error = %v, want the Page collision", err)
	}
	writeFiles(t, root, map[string]string{"web/handler.go": "package renamed\n"})
	if got, err := resolver.Resolve(dir); err != nil || got != want {
		t.Errorf("Resolver.Resolve() after an edit = %+v, %v; want the first parse %+v", got, err, want)
	}
	if declarations, err := resolver.Declarations(dir); err != nil || declarations["Props"] != "handler.go" {
		t.Errorf("Resolver.Declarations() after an edit = %v, %v; want the first parse", declarations, err)
	}
}
