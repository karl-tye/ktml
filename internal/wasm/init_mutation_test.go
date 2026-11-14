package wasm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// filesystemMutations are calls that change the filesystem.
var filesystemMutations = map[string]bool{
	"Chmod": true, "Chown": true, "Chtimes": true, "Create": true, "CreateTemp": true,
	"Link": true, "Mkdir": true, "MkdirAll": true, "MkdirTemp": true, "OpenFile": true,
	"Remove": true, "RemoveAll": true, "Rename": true, "Symlink": true, "Truncate": true,
	"WriteFile": true,
}

// TestPackageInit_GIVENRepositorySources_WHENInspected_THENNoInitMutatesTheFilesystem
// guards PLAN's plugin-platform rule: package initialisation runs inside
// every plugin and tool, so it must never write, create, or remove files.
func TestPackageInit_GIVENRepositorySources_WHENInspected_THENNoInitMutatesTheFilesystem(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != "init" || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "os" && filesystemMutations[selector.Sel.Name] {
					relative, _ := filepath.Rel(root, path)
					offenders = append(offenders, relative+":"+fset.Position(call.Pos()).String()[len(path)+1:]+" os."+selector.Sel.Name)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("package init functions mutate the filesystem:\n%s", strings.Join(offenders, "\n"))
	}
}
