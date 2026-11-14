// Package vet implements ktml vet: static checks over the handwritten Go code
// that uses generated KTML components (ADR-0003, Resolved Question Q2).
//
// The only check today is missing-required-input. Generated props structs
// cannot express that an input is required, because Go has no required struct
// fields, so vet reports keyed composite literals of a generated props type
// that omit the Go field of a required KTML input.
package vet

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/gopkg"
)

// Diagnostic codes.
const (
	// CodeMissingRequiredInput marks a keyed props literal that omits the Go
	// field of a required input.
	CodeMissingRequiredInput = "missing-required-input"
	// CodeKTMLErrors marks a KTML package whose components could not be
	// analyzed, so literals of its props types were not checked.
	CodeKTMLErrors = "ktml-errors"
	// CodeGoParseError marks a Go file that could not be parsed.
	CodeGoParseError = "go-parse-error"
)

// Diagnostic is one vet finding. Path is relative to the working directory
// passed to Run when possible; Line and Column are 1-based, and Column counts
// bytes, as in Go compiler output.
type Diagnostic struct {
	Path    string
	Line    int
	Column  int
	Code    string
	Message string
}

// String formats the diagnostic as path:line:col: [code] message.
func (d Diagnostic) String() string {
	return fmt.Sprintf("%s:%d:%d: [%s] %s", d.Path, d.Line, d.Column, d.Code, d.Message)
}

// Run vets the Go packages that patterns select. A pattern is a directory, or
// a directory followed by /... to include every directory below it (./...
// selects the whole tree). Relative patterns resolve against workDir, and no
// patterns means ./.... Directories named testdata or vendor, and those
// starting with . or _, are skipped by recursive patterns, as by the go tool.
//
// Run returns diagnostics sorted by path, line, and column. KTML packages with
// compiler errors and unparsable Go files become diagnostics, not errors; the
// error result is reserved for patterns that cannot be resolved.
func Run(workDir string, patterns []string) ([]Diagnostic, error) {
	return RunWithOverlays(workDir, patterns, nil)
}

// RunWithOverlays is Run with editor buffers: overlays holds the text of
// KTML documents by absolute path, read instead of the files on disk.
func RunWithOverlays(workDir string, patterns []string, overlays map[string][]byte) ([]Diagnostic, error) {
	workDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	dirs, err := expandPatterns(workDir, patterns)
	if err != nil {
		return nil, err
	}
	r := &runner{
		overlays:   overlays,
		workDir:    workDir,
		fileSet:    token.NewFileSet(),
		modules:    make(map[string]module),
		workspaces: make(map[string]*compiler.Workspace),
		packages:   make(map[string]*ktmlPackage),
		names:      make(map[string]string),
	}
	for _, dir := range dirs {
		if err := r.vetDir(dir); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(r.diagnostics, func(i, j int) bool {
		a, b := r.diagnostics[i], r.diagnostics[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	return r.diagnostics, nil
}

func expandPatterns(workDir string, patterns []string) ([]string, error) {
	seen := make(map[string]bool)
	var dirs []string
	add := func(dir string) {
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	for _, pattern := range patterns {
		recursive := pattern == "..." || strings.HasSuffix(pattern, "/...")
		base := strings.TrimSuffix(strings.TrimSuffix(pattern, "..."), "/")
		if base == "" {
			base = "."
		}
		base = filepath.FromSlash(base)
		if !filepath.IsAbs(base) {
			base = filepath.Join(workDir, base)
		}
		base = filepath.Clean(base)
		info, err := os.Stat(base)
		if err != nil {
			return nil, fmt.Errorf("vet pattern %s: %w", pattern, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("vet pattern %s: %s is not a directory", pattern, base)
		}
		if !recursive {
			add(base)
			continue
		}
		err = filepath.WalkDir(base, func(current string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if current != base && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			add(current)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("vet pattern %s: %w", pattern, err)
		}
	}
	return dirs, nil
}

type module struct {
	root, path string
	err        error
}

// ktmlPackage is the analysis of one directory's KTML components, keyed by
// their generated Go names.
type ktmlPackage struct {
	components map[string]compiler.Component
	// declared holds handwritten package-level Go names. A literal of a
	// handwritten type is never a generated props literal.
	declared map[string]string
	failed   bool
}

type runner struct {
	overlays    map[string][]byte
	workDir     string
	fileSet     *token.FileSet
	modules     map[string]module
	workspaces  map[string]*compiler.Workspace
	packages    map[string]*ktmlPackage
	names       map[string]string
	diagnostics []Diagnostic
}

func (r *runner) vetDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || gopkg.Generated(name) {
			continue
		}
		r.vetFile(filepath.Join(dir, name))
	}
	return nil
}

func (r *runner) vetFile(file string) {
	syntax, err := parser.ParseFile(r.fileSet, file, nil, parser.SkipObjectResolution)
	if err != nil {
		position := token.Position{Filename: file, Line: 1, Column: 1}
		var list scanner.ErrorList
		if errors.As(err, &list) && len(list) > 0 {
			position = list[0].Pos
			err = errors.New(list[0].Msg)
		}
		r.report(position.Filename, position.Line, position.Column, CodeGoParseError, err.Error())
		return
	}
	dir := filepath.Dir(file)
	ast.Inspect(syntax, func(node ast.Node) bool {
		if literal, ok := node.(*ast.CompositeLit); ok {
			r.checkLiteral(dir, syntax, literal)
		}
		return true
	})
}

func (r *runner) checkLiteral(dir string, file *ast.File, literal *ast.CompositeLit) {
	var typeName, written, targetDir string
	switch typ := literal.Type.(type) {
	case *ast.Ident:
		typeName, written, targetDir = typ.Name, typ.Name, dir
	case *ast.SelectorExpr:
		qualifier, ok := typ.X.(*ast.Ident)
		if !ok {
			return
		}
		resolved, ok := r.importedDir(dir, file, qualifier.Name)
		if !ok {
			return
		}
		typeName, written, targetDir = typ.Sel.Name, qualifier.Name+"."+typ.Sel.Name, resolved
	default:
		return
	}
	keys := make(map[string]bool, len(literal.Elts))
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			// Go already requires every field of a positional literal.
			return
		}
		if key, ok := pair.Key.(*ast.Ident); ok {
			keys[key.Name] = true
		}
	}
	pkg := r.ktmlPackage(targetDir)
	if pkg.failed {
		return
	}
	if _, handwritten := pkg.declared[typeName]; handwritten {
		return
	}
	component, ok := pkg.match(typeName)
	if !ok {
		return
	}
	position := r.fileSet.Position(literal.Pos())
	for _, input := range component.Inputs {
		if input.Mode != compiler.RequiredInput {
			continue
		}
		field := compiler.GeneratedGoField(input.Name)
		if keys[field] {
			continue
		}
		r.report(position.Filename, position.Line, position.Column, CodeMissingRequiredInput,
			fmt.Sprintf("%s literal omits required input %s (field %s)", written, input.Name, field))
	}
}

// match finds the component whose props type is typeName: XProps for X under
// APIs A and B, or X itself under Hybrid H.
func (pkg *ktmlPackage) match(typeName string) (compiler.Component, bool) {
	if trimmed, isProps := strings.CutSuffix(typeName, "Props"); isProps {
		if component, ok := pkg.components[trimmed]; ok {
			return component, true
		}
	}
	component, ok := pkg.components[typeName]
	return component, ok
}

// importedDir resolves a package qualifier through the file's imports to a
// directory of the module that contains dir. Imports outside that module, and
// blank and dot imports, do not resolve.
func (r *runner) importedDir(dir string, file *ast.File, qualifier string) (string, bool) {
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if spec.Name != nil {
			if spec.Name.Name != qualifier {
				continue
			}
			return r.moduleDir(dir, importPath)
		}
		target, ok := r.moduleDir(dir, importPath)
		if ok && r.packageName(target, importPath) == qualifier {
			return target, true
		}
	}
	return "", false
}

func (r *runner) moduleDir(dir, importPath string) (string, bool) {
	mod := r.module(dir)
	if mod.err != nil {
		return "", false
	}
	var target string
	switch {
	case importPath == mod.path:
		target = mod.root
	case strings.HasPrefix(importPath, mod.path+"/"):
		target = filepath.Join(mod.root, filepath.FromSlash(strings.TrimPrefix(importPath, mod.path+"/")))
	default:
		return "", false
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return "", false
	}
	return target, true
}

func (r *runner) module(dir string) module {
	if cached, ok := r.modules[dir]; ok {
		return cached
	}
	root, modulePath, err := gopkg.FindModule(dir)
	mod := module{root: root, path: modulePath, err: err}
	r.modules[dir] = mod
	return mod
}

func (r *runner) packageName(dir, importPath string) string {
	if cached, ok := r.names[dir]; ok {
		return cached
	}
	name := path.Base(importPath)
	if pkg, err := gopkg.Resolve(dir); err == nil {
		name = pkg.Name
	}
	r.names[dir] = name
	return name
}

// ktmlPackage analyzes the KTML component documents of one directory once per
// run. Each document is loaded with its import closure into a workspace shared
// by the module, so documents imported by several others are parsed once.
func (r *runner) ktmlPackage(dir string) *ktmlPackage {
	if cached, ok := r.packages[dir]; ok {
		return cached
	}
	pkg := &ktmlPackage{components: make(map[string]compiler.Component)}
	r.packages[dir] = pkg
	if declared, err := gopkg.Declarations(dir); err == nil {
		pkg.declared = declared
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return pkg
	}
	root := dir
	if mod := r.module(dir); mod.err == nil {
		root = mod.root
	}
	workspace, ok := r.workspaces[root]
	if !ok {
		workspace = compiler.NewWorkspace(root)
		for path, source := range r.overlays {
			_ = workspace.Overlay(path, 0, source)
		}
		r.workspaces[root] = workspace
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".ktml") {
			continue
		}
		file := filepath.Join(dir, name)
		snapshot, err := workspace.Load(file)
		if err == nil {
			err = snapshot.Err()
		}
		if err != nil {
			r.reportKTMLErrors(dir, file, snapshot, err)
			pkg.failed = true
			return pkg
		}
		for _, component := range snapshot.Components() {
			if filepath.Dir(component.File) == dir {
				pkg.components[compiler.GeneratedGoField(component.Name)] = component
			}
		}
	}
	return pkg
}

// reportKTMLErrors reports one diagnostic for a KTML package that vet cannot
// analyze, at the document's first error when it has one.
func (r *runner) reportKTMLErrors(dir, file string, snapshot *compiler.Snapshot, err error) {
	line, column := 1, 1
	message := err.Error()
	for _, diagnostic := range snapshot.Diagnostics(file) {
		if diagnostic.Severity == compiler.SeverityError {
			line, column = lineColumn(snapshot.Source(file), diagnostic.Start)
			message = fmt.Sprintf("[%s] %s", diagnostic.Code, diagnostic.Message)
			break
		}
	}
	r.report(file, line, column, CodeKTMLErrors,
		fmt.Sprintf("KTML package %s has errors, so literals of its props types were not checked: %s", r.relative(dir), message))
}

func (r *runner) report(file string, line, column int, code, message string) {
	r.diagnostics = append(r.diagnostics, Diagnostic{Path: r.relative(file), Line: line, Column: column, Code: code, Message: message})
}

func (r *runner) relative(file string) string {
	relative, err := filepath.Rel(r.workDir, file)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return file
	}
	return relative
}

func lineColumn(source []byte, offset int) (int, int) {
	if offset > len(source) {
		offset = len(source)
	}
	line, column := 1, 1
	for _, current := range source[:max(offset, 0)] {
		if current == '\n' {
			line++
			column = 1
			continue
		}
		column++
	}
	return line, column
}
