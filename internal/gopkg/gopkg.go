// Package gopkg resolves the Go package that generated KTML files join and the
// handwritten declarations they must not collide with.
package gopkg

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Package is the Go package of one source directory.
type Package struct {
	ImportPath string
	Name       string
}

// Generated reports whether a Go file name is KTML generator output,
// including the development twin that the ktmldev build tag selects.
func Generated(name string) bool {
	for _, suffix := range []string{"_ktml.go", "_ktml_dev.go", "_ktmlschema.go", ".ktml.go"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// Resolve derives a directory's import path from the nearest go.mod and its
// package name from existing handwritten files, falling back to the directory
// name.
func Resolve(dir string) (Package, error) {
	return resolve(dir, handwrittenFiles)
}

func resolve(dir string, parse func(string) ([]*ast.File, []string, error)) (Package, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Package{}, err
	}
	moduleRoot, modulePath, err := FindModule(dir)
	if err != nil {
		return Package{}, err
	}
	relative, err := filepath.Rel(moduleRoot, dir)
	if err != nil {
		return Package{}, err
	}
	importPath := modulePath
	if relative != "." {
		importPath = path.Join(modulePath, filepath.ToSlash(relative))
	}
	name, err := packageName(dir, parse)
	if err != nil {
		return Package{}, err
	}
	return Package{ImportPath: importPath, Name: name}, nil
}

// FindModule returns the directory and module path of the nearest go.mod at or
// above dir.
func FindModule(dir string) (root, modulePath string, err error) {
	for current := dir; ; current = filepath.Dir(current) {
		file, err := os.Open(filepath.Join(current, "go.mod"))
		if err == nil {
			defer file.Close()
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				fields := strings.Fields(scanner.Text())
				if len(fields) >= 2 && fields[0] == "module" {
					return current, strings.Trim(fields[1], `"`), nil
				}
			}
			if err := scanner.Err(); err != nil {
				return "", "", err
			}
			return "", "", fmt.Errorf("%s has no module directive", filepath.Join(current, "go.mod"))
		}
		if !os.IsNotExist(err) {
			return "", "", err
		}
		if filepath.Dir(current) == current {
			return "", "", fmt.Errorf("no go.mod found above %s", dir)
		}
	}
}

// handwrittenFiles parses the non-generated, non-external-test Go files of dir.
func handwrittenFiles(dir string) ([]*ast.File, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	var files []*ast.File
	var names []string
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || Generated(name) {
			continue
		}
		file, err := parser.ParseFile(fileSet, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", name, err)
		}
		if strings.HasSuffix(file.Name.Name, "_test") {
			continue
		}
		files = append(files, file)
		names = append(names, name)
	}
	return files, names, nil
}

func packageName(dir string, parse func(string) ([]*ast.File, []string, error)) (string, error) {
	files, _, err := parse(dir)
	if err != nil {
		return "", err
	}
	for _, file := range files {
		return file.Name.Name, nil
	}
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, filepath.Base(dir))
	if name == "" || name[0] >= '0' && name[0] <= '9' {
		name = "p" + name
	}
	return name, nil
}

// Declarations returns the package-level names declared by handwritten Go
// files in dir, mapped to the file that declares them.
func Declarations(dir string) (map[string]string, error) {
	return declarations(dir, handwrittenFiles)
}

func declarations(dir string, parse func(string) ([]*ast.File, []string, error)) (map[string]string, error) {
	files, names, err := parse(dir)
	if err != nil {
		return nil, err
	}
	declarations := make(map[string]string)
	for index, file := range files {
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				if value.Recv == nil {
					declarations[value.Name.Name] = names[index]
				}
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					switch typed := spec.(type) {
					case *ast.TypeSpec:
						declarations[typed.Name.Name] = names[index]
					case *ast.ValueSpec:
						for _, name := range typed.Names {
							declarations[name.Name] = names[index]
						}
					}
				}
			}
		}
	}
	return declarations, nil
}

// CheckCollisions fails when a generated name is declared by handwritten Go.
func CheckCollisions(dir string, generated []string) error {
	return checkCollisions(dir, generated, handwrittenFiles)
}

func checkCollisions(dir string, generated []string, parse func(string) ([]*ast.File, []string, error)) error {
	declarations, err := declarations(dir, parse)
	if err != nil {
		return err
	}
	var collisions []string
	for _, name := range generated {
		if file, exists := declarations[name]; exists {
			collisions = append(collisions, fmt.Sprintf("%s (declared in %s)", name, file))
		}
	}
	sort.Strings(collisions)
	if len(collisions) > 0 {
		return fmt.Errorf("generated Go names collide with handwritten declarations: %s", strings.Join(collisions, ", "))
	}
	return nil
}

// Resolver answers Resolve, Declarations, and CheckCollisions while parsing
// each directory's handwritten Go files once. It is safe for concurrent use.
// It never rereads a directory, so it serves one generation run.
type Resolver struct {
	mu   sync.Mutex
	dirs map[string]*parsedDir
}

type parsedDir struct {
	once  sync.Once
	files []*ast.File
	names []string
	err   error
}

// NewResolver returns an empty Resolver.
func NewResolver() *Resolver {
	return &Resolver{dirs: make(map[string]*parsedDir)}
}

// Resolve is Resolve with dir's handwritten files parsed at most once.
func (r *Resolver) Resolve(dir string) (Package, error) {
	return resolve(dir, r.handwrittenFiles)
}

// Declarations is Declarations with dir's handwritten files parsed at most
// once.
func (r *Resolver) Declarations(dir string) (map[string]string, error) {
	return declarations(dir, r.handwrittenFiles)
}

// CheckCollisions is CheckCollisions with dir's handwritten files parsed at
// most once.
func (r *Resolver) CheckCollisions(dir string, generated []string) error {
	return checkCollisions(dir, generated, r.handwrittenFiles)
}

func (r *Resolver) handwrittenFiles(dir string) ([]*ast.File, []string, error) {
	r.mu.Lock()
	parsed, ok := r.dirs[dir]
	if !ok {
		parsed = &parsedDir{}
		r.dirs[dir] = parsed
	}
	r.mu.Unlock()
	parsed.once.Do(func() {
		parsed.files, parsed.names, parsed.err = handwrittenFiles(dir)
	})
	return parsed.files, parsed.names, parsed.err
}
