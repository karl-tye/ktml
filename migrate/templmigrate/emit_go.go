package templmigrate

import (
	"fmt"
	"go/ast"
	"go/types"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	parser "github.com/a-h/templ/parser/v2"
)

// goImports is the import set of one generated Go presenter file.
type goImports struct {
	byPath  map[string]string // import path to alias
	byAlias map[string]string // alias to import path
	natural map[string]string // import path to its package name
}

func newGoImports(ownPackage string) *goImports {
	return &goImports{byPath: map[string]string{}, byAlias: map[string]string{ownPackage: ""}, natural: map[string]string{}}
}

// add returns the alias under which the file refers to the package,
// preferring name and falling back to name plus a suffix.
func (g *goImports) add(importPath, name string) string {
	if alias, ok := g.byPath[importPath]; ok {
		return alias
	}
	alias := name
	for i := 2; ; i++ {
		if _, taken := g.byAlias[alias]; !taken && !goKeyword(alias) {
			break
		}
		alias = name + strconv.Itoa(i)
	}
	g.byPath[importPath] = alias
	g.byAlias[alias] = importPath
	g.natural[importPath] = name
	return alias
}

func goKeyword(name string) bool {
	switch name {
	case "break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct", "switch", "type", "var":
		return true
	}
	return false
}

// block renders the import declaration.
func (g *goImports) block() string {
	if len(g.byPath) == 0 {
		return ""
	}
	paths := make([]string, 0, len(g.byPath))
	for p := range g.byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	b.WriteString("import (\n")
	for _, p := range paths {
		alias := g.byPath[p]
		if alias == path.Base(p) && alias == g.natural[p] {
			fmt.Fprintf(&b, "\t%q\n", p)
			continue
		}
		fmt.Fprintf(&b, "\t%s %q\n", alias, p)
	}
	b.WriteString(")\n")
	return b.String()
}

// goRewriter copies ranges of one file's generated Go into a presenter in
// the output package. Identifiers of the source package become qualified,
// package references go through the presenter's import set, and Go
// declarations of the converted templ files stay unqualified because they
// move into the presenters.
type goRewriter struct {
	c       *conversion
	file    *sourceFile
	t       *typedFile
	imports *goImports
	idents  []*ast.Ident
	sels    map[*ast.Ident]bool
	// clockCalls are the generated ranges of time.Now() calls. A presenter
	// takes the clock as its now parameter instead (PLAN: clock reads are
	// presenter-provided values).
	clockCalls [][2]int
	// clock enables the now rewrite, to clockName; it is off for moved
	// declarations.
	clock     bool
	clockName string
}

func newGoRewriter(c *conversion, file *sourceFile, imports *goImports) *goRewriter {
	r := &goRewriter{c: c, file: file, t: c.run.typed[file], imports: imports, sels: map[*ast.Ident]bool{}}
	if r.t == nil {
		return r
	}
	ast.Inspect(r.t.file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Ident:
			r.idents = append(r.idents, node)
		case *ast.SelectorExpr:
			r.sels[node.Sel] = true
		case *ast.CallExpr:
			if isClockCall(r.t.info, node) {
				r.clockCalls = append(r.clockCalls, [2]int{r.t.tok.Offset(node.Pos()), r.t.tok.Offset(node.End())})
			}
		}
		return true
	})
	return r
}

// isClockCall reports a call of time.Now.
func isClockCall(info *types.Info, call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	fn, ok := info.Uses[selector.Sel].(*types.Func)
	return ok && fn.Pkg() != nil && fn.Pkg().Path() == "time" && fn.Name() == "Now"
}

// span returns the generated-file byte range of a templ expression.
func (r *goRewriter) span(e parser.Expression) (int, int, bool) {
	if r.t == nil {
		return 0, 0, false
	}
	start, ok := r.t.target(e)
	if !ok {
		return 0, 0, false
	}
	return start, start + len(strings.TrimRight(e.Value, " \t\r\n")), true
}

// expr rewrites one templ expression.
func (r *goRewriter) expr(e parser.Expression) (string, []string) {
	start, end, ok := r.span(e)
	if !ok {
		return strings.TrimSpace(e.Value), []string{"expression not found in the generated Go; copied unrewritten"}
	}
	return r.rewrite(start, end)
}

// node rewrites the generated text of an AST node.
func (r *goRewriter) node(n ast.Node) (string, []string) {
	return r.rewrite(r.t.tok.Offset(n.Pos()), r.t.tok.Offset(n.End()))
}

// rewrite returns the generated Go in [start, end) with its identifiers
// qualified for the presenter's package, and the problems that keep the
// result from compiling there.
func (r *goRewriter) rewrite(start, end int) (string, []string) {
	type edit struct {
		from, to int
		text     string
	}
	var edits []edit
	var problems []string
	inClock := func(offset int) bool {
		for _, call := range r.clockCalls {
			if offset >= call[0] && offset < call[1] {
				return true
			}
		}
		return false
	}
	if r.clock {
		for _, call := range r.clockCalls {
			if call[0] >= start && call[1] <= end {
				edits = append(edits, edit{call[0], call[1], r.clockName})
			}
		}
	}
	for _, ident := range r.idents {
		offset := r.t.tok.Offset(ident.Pos())
		if offset < start || offset >= end || r.clock && inClock(offset) {
			continue
		}
		obj := r.t.info.Uses[ident]
		if obj == nil {
			continue
		}
		if r.sels[ident] {
			if problem := r.unexportedMember(obj); problem != "" {
				problems = append(problems, problem)
			}
			continue
		}
		if reason, ok := r.c.poisoned[obj]; ok {
			problems = append(problems, fmt.Sprintf("%s is unresolved (%s)", obj.Name(), reason))
			continue
		}
		replacement, problem := r.reference(ident, obj)
		if problem != "" {
			problems = append(problems, problem)
		}
		if replacement != "" && replacement != ident.Name {
			edits = append(edits, edit{offset, offset + len(ident.Name), replacement})
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].from < edits[j].from })
	var b strings.Builder
	cursor := start
	for _, e := range edits {
		b.Write(r.t.code[cursor:e.from])
		b.WriteString(e.text)
		cursor = e.to
	}
	b.Write(r.t.code[cursor:end])
	return b.String(), problems
}

// unexportedMember reports a selected field or method that the presenter
// package cannot reach: unexported, of another package, and not of a type
// the presenters declare themselves.
func (r *goRewriter) unexportedMember(obj types.Object) string {
	if obj.Exported() || obj.Pkg() == nil {
		return ""
	}
	switch obj.(type) {
	case *types.Var, *types.Func:
	default:
		return ""
	}
	if v, ok := obj.(*types.Var); ok && !v.IsField() {
		return ""
	}
	if r.c.opts.InPlace && obj.Pkg() == r.t.pkg {
		// In place, the presenter is in the member's own package.
		return ""
	}
	if fn, ok := obj.(*types.Func); ok && fn.Type().(*types.Signature).Recv() == nil {
		return ""
	}
	if r.c.convertedFiles[r.t.fset.Position(obj.Pos()).Filename] {
		if v, ok := obj.(*types.Var); ok && v.IsField() {
			// A field of a type declared in a templ file: an unexported
			// type moves with the presenters.
			return ""
		}
	}
	return fmt.Sprintf("unexported %s %s of package %s is not reachable from the presenter package", objectKind(obj), obj.Name(), obj.Pkg().Name())
}

// reference returns how the presenter spells a reference to obj.
func (r *goRewriter) reference(ident *ast.Ident, obj types.Object) (string, string) {
	if pkgName, ok := obj.(*types.PkgName); ok {
		imported := pkgName.Imported()
		return r.packageAlias(imported.Path(), imported.Name()), ""
	}
	if obj.Pkg() == nil || obj.Pkg().Scope().Lookup(obj.Name()) != obj {
		// Builtins, locals, parameters, fields, and methods.
		return "", ""
	}
	if r.c.movedDeclaration(obj) {
		if r.c.templateObject(obj) {
			return "", fmt.Sprintf("template %s used as a Go value", obj.Name())
		}
		if reason, bad := r.c.broken[obj]; bad && r.c.broken != nil {
			return "", fmt.Sprintf("%s cannot move into the presenter package: %s", obj.Name(), reason)
		}
		return "", ""
	}
	if r.c.opts.InPlace && obj.Pkg() == r.t.pkg {
		// In place, the presenter shares the source package's scope.
		return "", ""
	}
	if !obj.Exported() {
		if constant, ok := obj.(*types.Const); ok {
			return constant.Val().ExactString(), ""
		}
		return "", fmt.Sprintf("unexported %s %s of package %s is not reachable from the presenter package; export it or move it", objectKind(obj), obj.Name(), obj.Pkg().Name())
	}
	return r.packageAlias(obj.Pkg().Path(), obj.Pkg().Name()) + "." + obj.Name(), ""
}

// packageAlias returns the presenter's alias for a source Go package. A
// package converted in this run has a KTML output package of the same
// name, so its source package is imported as <name>src.
func (r *goRewriter) packageAlias(importPath, name string) string {
	if r.c.convertedSource[importPath] && !r.c.opts.InPlace {
		return r.imports.add(importPath, name+"src")
	}
	return r.imports.add(importPath, name)
}

func objectKind(obj types.Object) string {
	switch obj.(type) {
	case *types.Func:
		return "function"
	case *types.Var:
		return "variable"
	case *types.TypeName:
		return "type"
	case *types.Const:
		return "constant"
	}
	return "identifier"
}

// typeText spells a Go type for the presenter, qualifying packages through
// the import set.
func (r *goRewriter) typeText(t types.Type) string {
	text := types.TypeString(t, func(p *types.Package) string {
		if r.c.opts.InPlace && p == r.t.pkg {
			return ""
		}
		if r.c.convertedSource[p.Path()] && !r.c.opts.InPlace {
			return r.imports.add(p.Path(), p.Name()+"src")
		}
		return r.imports.add(p.Path(), p.Name())
	})
	// An unexported type is never reachable through its package: it is a
	// moved declaration, which the presenter declares itself.
	return unexportedQualifier.ReplaceAllString(text, "$1")
}

var unexportedQualifier = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\.([a-z_][A-Za-z0-9_]*)`)
