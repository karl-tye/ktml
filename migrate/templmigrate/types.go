package templmigrate

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/a-h/templ/generator"
	parser "github.com/a-h/templ/parser/v2"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
	"github.com/karl-tye/ktml/internal/gopkg"
	"golang.org/x/tools/go/packages"
)

// typedFile is the in-memory generated Go for one templ file, type-checked
// with its package. The templ source map relates templ expressions to the
// identical text in the generated file.
type typedFile struct {
	sourceMap *parser.SourceMap
	tok       *token.File
	info      *types.Info
	pkg       *types.Package
	// nodes holds every AST node of the generated file by start offset, in
	// pre-order (outermost first).
	nodes map[int][]ast.Node
	// code and file are the generated Go source and its AST; fset holds its
	// positions. The converter copies and rewrites expressions from them.
	code []byte
	file *ast.File
	fset *token.FileSet
}

// generate renders the templ file to Go in memory, as templ generate does.
func generate(file *parser.TemplateFile, name string) ([]byte, *parser.SourceMap, error) {
	var buf bytes.Buffer
	out, err := generator.Generate(file, &buf, generator.WithFileName(name))
	if err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), out.SourceMap, nil
}

// loadTypes type-checks the packages of the given templ files with their
// generated Go supplied as overlays. It returns the typed files that could
// be located; files missing from the result are classified syntactically.
func loadTypes(files []*sourceFile) (map[*sourceFile]*typedFile, []string) {
	typed := make(map[*sourceFile]*typedFile)
	var problems []string
	byModule := make(map[string][]*sourceFile)
	for _, file := range files {
		if file.parsed == nil {
			continue
		}
		root := findModuleRoot(filepath.Dir(file.abs))
		if root == "" {
			problems = append(problems, fmt.Sprintf("%s: no go.mod found; types unavailable", file.rel))
			continue
		}
		byModule[root] = append(byModule[root], file)
	}
	roots := make([]string, 0, len(byModule))
	for root := range byModule {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for _, root := range roots {
		moduleFiles := byModule[root]
		overlay := make(map[string][]byte)
		maps := make(map[string]*parser.SourceMap)
		codes := make(map[string][]byte)
		owners := make(map[string]*sourceFile)
		dirs := make(map[string]bool)
		for _, file := range moduleFiles {
			goName := strings.TrimSuffix(file.abs, ".templ") + "_templ.go"
			rel, _ := filepath.Rel(root, file.abs)
			code, sourceMap, err := generate(file.parsed, filepath.ToSlash(rel))
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: templ generation failed; types unavailable: %v", file.rel, err))
				continue
			}
			overlay[goName] = code
			codes[goName] = code
			maps[goName] = sourceMap
			owners[goName] = file
			dirs[filepath.Dir(file.abs)] = true
		}
		// The other templ files of the module get their Go generated too,
		// so a dependency's types never depend on a stale or missing
		// _templ.go.
		hidden := map[string]bool{}
		for dir := range dirs {
			hidden[dir] = true
		}
		for path, code := range moduleTemplGo(root, overlay) {
			overlay[path] = code
			hidden[filepath.Dir(path)] = true
		}
		// Outputs of an earlier in-place run share these packages with the
		// templ files; hide them so the templ Go type-checks alone. A
		// package without templ files keeps its outputs: its bridges and
		// generated KTML Go are what later batches call.
		hideConverterOutputs(hidden, overlay)
		// Earlier batches' KTML Go is generated afresh, so their bridges
		// and presenters type-check before ktml generate has run.
		for path, code := range moduleKTMLGo(root, overlay) {
			overlay[path] = code
		}
		patterns := make([]string, 0, len(dirs))
		for dir := range dirs {
			rel, _ := filepath.Rel(root, dir)
			patterns = append(patterns, "./"+filepath.ToSlash(rel))
		}
		sort.Strings(patterns)
		cfg := &packages.Config{
			Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
			Dir:     root,
			Overlay: overlay,
			Tests:   false,
		}
		pkgs, err := packages.Load(cfg, patterns...)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: loading packages failed; types unavailable: %v", root, err))
			continue
		}
		for _, pkg := range pkgs {
			for _, pkgErr := range pkg.Errors {
				problems = append(problems, fmt.Sprintf("%s: %v", pkg.PkgPath, pkgErr))
			}
			for _, syntax := range pkg.Syntax {
				tok := pkg.Fset.File(syntax.Pos())
				owner, ok := owners[tok.Name()]
				if !ok {
					continue
				}
				typed[owner] = &typedFile{
					sourceMap: maps[tok.Name()],
					tok:       tok,
					info:      pkg.TypesInfo,
					pkg:       pkg.Types,
					nodes:     indexNodes(tok, syntax),
					code:      codes[tok.Name()],
					file:      syntax,
					fset:      pkg.Fset,
				}
			}
		}
	}
	for _, file := range files {
		if file.parsed != nil && typed[file] == nil && !hasProblemFor(problems, file.rel) {
			problems = append(problems, fmt.Sprintf("%s: generated Go was not type-checked; types unavailable", file.rel))
		}
	}
	return typed, problems
}

func hasProblemFor(problems []string, rel string) bool {
	for _, problem := range problems {
		if strings.HasPrefix(problem, rel+":") {
			return true
		}
	}
	return false
}

func indexNodes(tok *token.File, file *ast.File) map[int][]ast.Node {
	nodes := make(map[int][]ast.Node)
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		offset := tok.Offset(n.Pos())
		nodes[offset] = append(nodes[offset], n)
		return true
	})
	return nodes
}

func findModuleRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// target returns the generated-file offset of a templ expression.
func (t *typedFile) target(e parser.Expression) (int, bool) {
	if t == nil {
		return 0, false
	}
	position, ok := t.sourceMap.TargetPositionFromSource(e.Range.From.Line, e.Range.From.Col)
	if !ok {
		return 0, false
	}
	return int(position.Index), true
}

// expr returns the generated Go expression that spells the templ
// expression, or nil.
func (t *typedFile) expr(e parser.Expression) ast.Expr {
	start, ok := t.target(e)
	if !ok {
		return nil
	}
	end := start + len(strings.TrimRight(e.Value, " \t\r\n"))
	for _, n := range t.nodes[start] {
		expr, ok := n.(ast.Expr)
		if ok && t.tok.Offset(expr.End()) == end {
			return expr
		}
	}
	return nil
}

// nodeAt returns the outermost generated node of type T starting where the
// templ expression maps.
func nodeAt[T ast.Node](t *typedFile, e parser.Expression) (T, bool) {
	var zero T
	start, ok := t.target(e)
	if !ok {
		return zero, false
	}
	for _, n := range t.nodes[start] {
		if typed, ok := n.(T); ok {
			return typed, true
		}
	}
	return zero, false
}

// stmtContaining returns the innermost generated statement of type T whose
// range contains the mapped start of the templ expression.
func stmtContaining[T ast.Node](t *typedFile, e parser.Expression) (T, bool) {
	var zero T
	start, ok := t.target(e)
	if !ok {
		return zero, false
	}
	var found T
	var foundOK bool
	for offset := start; offset >= 0 && start-offset < 256; offset-- {
		for _, n := range t.nodes[offset] {
			typed, ok := n.(T)
			if !ok {
				continue
			}
			if t.tok.Offset(n.End()) > start {
				found, foundOK = typed, true
			}
		}
		if foundOK {
			return found, true
		}
	}
	return zero, false
}

// moduleKTMLGo generates the Go of every valid .ktml and .ktmlschema
// document of the module at root, by generated path. A document whose
// templ file still exists is skipped, since its components would shadow
// that file's templ Go; so is a path that already has an overlay.
func moduleKTMLGo(root string, overlay map[string][]byte) map[string][]byte {
	var docs []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if compiler.IsStoryPath(path) {
			return nil // ktml generate writes no Go for a story Document
		}
		if stem, ok := strings.CutSuffix(path, ".ktml"); ok {
			if _, err := os.Stat(stem + ".templ"); err != nil {
				docs = append(docs, path)
			}
		} else if strings.HasSuffix(path, ".ktmlschema") {
			docs = append(docs, path)
		}
		return nil
	})
	generated := map[string][]byte{}
	if len(docs) == 0 {
		return generated
	}
	workspace := compiler.NewWorkspace(root)
	packages := func(dir string) (gogen.GoPackage, error) {
		resolved, err := gopkg.Resolve(dir)
		return gogen.GoPackage{ImportPath: resolved.ImportPath, Name: resolved.Name}, err
	}
	for _, doc := range docs {
		target := strings.TrimSuffix(doc, ".ktml") + "_ktml.go"
		if stem, ok := strings.CutSuffix(doc, ".ktmlschema"); ok {
			target = stem + "_ktmlschema.go"
		}
		if overlay[target] != nil {
			continue
		}
		snapshot, err := workspace.Load(doc)
		if err != nil || !snapshot.Valid() {
			continue // the Go on disk, if any, stays
		}
		pkg, err := gopkg.Resolve(filepath.Dir(doc))
		if err != nil {
			continue
		}
		options := gogen.Options{Package: pkg.Name, Packages: packages}
		var code []byte
		if strings.HasSuffix(doc, ".ktmlschema") {
			code, _, err = gogen.GenerateSchema(snapshot, doc, options)
		} else {
			code, err = gogen.GenerateFile(snapshot, doc, options)
		}
		if err == nil {
			generated[target] = code
		}
	}
	return generated
}

// moduleTemplGo generates the Go of every .templ file of the module at root
// that has no overlay yet, by _templ.go path. Files that fail to parse or
// generate are left to their _templ.go on disk.
func moduleTemplGo(root string, overlay map[string][]byte) map[string][]byte {
	generated := map[string][]byte{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		goPath := strings.TrimSuffix(path, ".templ") + "_templ.go"
		if !strings.HasSuffix(path, ".templ") || overlay[goPath] != nil {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		parsed, err := parser.ParseString(string(source))
		if err != nil {
			return nil
		}
		parsed.Filepath = path
		rel, _ := filepath.Rel(root, path)
		code, _, err := generate(parsed, filepath.ToSlash(rel))
		if err == nil {
			generated[goPath] = code
		}
		return nil
	})
	return generated
}
