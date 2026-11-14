package templmigrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	goparser "go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/a-h/templ"
	"github.com/a-h/templ/generator"
	parser "github.com/a-h/templ/parser/v2"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

// CodemodSchema identifies the JSON report of a codemod run.
const CodemodSchema = "ktml.migrate.codemod/v1"

const ktmlPath = "github.com/karl-tye/ktml"

// SiteStatus classifies one call site.
type SiteStatus string

const (
	// SiteRewritable would be rewritten to the KTML spelling (report mode).
	SiteRewritable SiteStatus = "rewritable"
	// SiteRewritten was rewritten (--write).
	SiteRewritten SiteStatus = "rewritten"
	// SiteBridged is a templ call site that calls (or, in report mode,
	// would call) the template's templ bridge.
	SiteBridged SiteStatus = "bridged"
	// SiteManual cannot be rewritten safely; Reason says why. It is never
	// edited.
	SiteManual SiteStatus = "manual"
	// SiteDeferred is left unchanged on purpose: it compiles and behaves
	// the same against KTML components (templ.Component types, which
	// ktml.Component values are assignable to), and is renamed when templ
	// leaves the binary (Phase 8).
	SiteDeferred SiteStatus = "deferred"
)

// CallSite is one Go or templ use of a converted template, or of the templ
// APIs the migration touches.
type CallSite struct {
	File string   `json:"file"`
	From Position `json:"from"`
	To   Position `json:"to"`
	// Kind is call, value (a template used as a Go value), templ-call (a
	// call in an unconverted .templ file), templ-value, with-children,
	// component-type, or handler.
	Kind     string     `json:"kind"`
	Template string     `json:"template,omitempty"`
	Status   SiteStatus `json:"status"`
	Reason   string     `json:"reason,omitempty"`
	// Notes record behavior the rewrite moves, for review: a clock read at
	// construction instead of render, a buffered fragment render.
	Notes       []string `json:"notes,omitempty"`
	Original    string   `json:"original,omitempty"`
	Replacement string   `json:"replacement,omitempty"`
}

// CodemodOptions configure a codemod run.
type CodemodOptions struct {
	// Write applies the rewrites; without it the run only reports.
	Write bool
	// FragmentRender is "<import path>.<Func>", a function with the shape
	// func(ctx context.Context, w http.ResponseWriter, c ktml.Component) error
	// that renders into a buffer and writes only a complete response
	// (ADR-0003: fragments are buffered). A converted component rendered
	// directly into an http.ResponseWriter is routed through it. Without
	// it, such sites are rewritten in place and noted for review.
	FragmentRender string
	// Tags are build tags to load the packages with, so that files built
	// only with them (e2e tests, probes) are rewritten too.
	Tags []string
}

// CodemodReport is the machine-readable result of a codemod run.
type CodemodReport struct {
	Schema   string         `json:"schema"`
	Write    bool           `json:"write"`
	Totals   map[string]int `json:"totals"`
	ByKind   map[string]int `json:"byKind"`
	Files    []string       `json:"filesChanged,omitempty"`
	Problems []string       `json:"problems,omitempty"`
	Sites    []CallSite     `json:"sites"`
}

// Failed reports whether the run left a site that needs a hand edit, or
// could not load a package.
func (r *CodemodReport) Failed() bool {
	return r.Totals[string(SiteManual)] > 0 || len(r.Problems) > 0
}

// WriteJSON writes the report.
func (r *CodemodReport) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// LoadCallMappings reads the call mappings of a conversion report.
func LoadCallMappings(path string) ([]CallMapping, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var report struct {
		Conversion *struct {
			Calls []CallMapping `json:"calls"`
		} `json:"conversion"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if report.Conversion == nil || len(report.Conversion.Calls) == 0 {
		return nil, fmt.Errorf("%s: not a conversion report with call mappings (run ktml migrate templ --in-place or --out first)", path)
	}
	return report.Conversion.Calls, nil
}

// ScanCallSites classifies the call sites without writing anything.
func ScanCallSites(root string, patterns []string, mappings []CallMapping, options CodemodOptions) ([]CallSite, error) {
	options.Write = false
	report, err := Codemod(root, patterns, mappings, options)
	if err != nil {
		return nil, err
	}
	if len(report.Problems) > 0 {
		return report.Sites, fmt.Errorf("%s", strings.Join(report.Problems, "; "))
	}
	return report.Sites, nil
}

// codemod is one run's state.
type codemod struct {
	root     string
	options  CodemodOptions
	mappings map[string]*CallMapping // "<pkg>.[<Receiver>.]<Template>"
	// batch holds the converted templ files, relative to the root.
	batch map[string]bool
	byPkg map[string]bool
	// templFiles holds the generated Go of every .templ file under the
	// root, which the run overlays, by _templ.go path.
	templFiles map[string]*templOverlay
	report     *CodemodReport
	// presenters caches the presenter signatures found in the mapping
	// packages' directories: results and parameter counts.
	presenters map[string]presenterSig
	// inputs caches inputsChanged per mapping.
	inputs   map[*CallMapping]string
	fragment struct{ path, name string }
}

type templOverlay struct {
	templPath string
	source    []byte
	sourceMap *parser.SourceMap
	code      []byte
}

type presenterSig struct {
	found          bool
	params, result int
}

// Codemod rewrites the Go and templ callers of converted templates under
// root (in the packages patterns name) to the Hybrid H API, or, with
// options.Write unset, reports what it would do.
//
// A Go call pkg.X(args) becomes pkg.X{View: pkg.NewXView(args)} (or the
// direct component's fields). A call in an unconverted .templ file becomes
// a call of the template's templ bridge, XTempl, keeping its children. Every
// other use is a manual site with a reason, and is not edited.
func Codemod(root string, patterns []string, mappings []CallMapping, options CodemodOptions) (*CodemodReport, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cm := &codemod{
		root: root, options: options,
		mappings: map[string]*CallMapping{}, byPkg: map[string]bool{}, batch: map[string]bool{},
		templFiles: map[string]*templOverlay{},
		report:     &CodemodReport{Schema: CodemodSchema, Write: options.Write, Totals: map[string]int{}, ByKind: map[string]int{}},
		presenters: map[string]presenterSig{},
		inputs:     map[*CallMapping]string{},
	}
	for i := range mappings {
		m := &mappings[i]
		key := m.Package + "."
		if m.Receiver != "" {
			key += m.Receiver + "."
		}
		cm.mappings[key+m.Template] = m
		cm.byPkg[m.Package] = true
		cm.batch[m.File] = true
	}
	if options.FragmentRender != "" {
		dot := strings.LastIndex(options.FragmentRender, ".")
		if dot <= 0 || dot == len(options.FragmentRender)-1 {
			return nil, fmt.Errorf("codemod: --fragment-render must be <import path>.<Func>, not %q", options.FragmentRender)
		}
		cm.fragment.path, cm.fragment.name = options.FragmentRender[:dot], options.FragmentRender[dot+1:]
	}
	overlay, err := cm.overlay()
	if err != nil {
		return nil, err
	}
	cfg := &packages.Config{
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:     root,
		Overlay: overlay,
		Tests:   true,
		Fset:    token.NewFileSet(),
	}
	if len(options.Tags) > 0 {
		cfg.BuildFlags = []string{"-tags=" + strings.Join(options.Tags, ",")}
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var edits []fileEdit
	for _, pkg := range pkgs {
		for _, pkgErr := range pkg.Errors {
			// A package mid-migration may not type-check; its sites are
			// still found from the partial type information, so errors
			// are only reported when nothing could be typed.
			if pkg.TypesInfo == nil {
				cm.report.Problems = append(cm.report.Problems, fmt.Sprintf("%s: %v", pkg.PkgPath, pkgErr))
			}
		}
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			name := cfg.Fset.File(file.Pos()).Name()
			if seen[name] {
				continue
			}
			seen[name] = true
			if t := cm.templFiles[name]; t != nil {
				if cm.batch[cm.rel(t.templPath)] {
					continue // calls between the batch's own templates
				}
				if edit := cm.templFile(pkg, cfg.Fset, file, t); edit != nil {
					edits = append(edits, *edit)
				}
				continue
			}
			if strings.HasSuffix(name, "_templ.go") || !strings.HasSuffix(name, ".go") {
				continue
			}
			data, err := os.ReadFile(name)
			if err != nil {
				continue
			}
			if converterOutput(name, data[:min(len(data), 512)]) {
				continue
			}
			if edit := cm.goFile(pkg, cfg.Fset, file, name, data); edit != nil {
				edits = append(edits, *edit)
			}
		}
	}
	sort.Slice(cm.report.Sites, func(i, j int) bool {
		a, b := cm.report.Sites[i], cm.report.Sites[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.From.Line != b.From.Line {
			return a.From.Line < b.From.Line
		}
		return a.From.Col < b.From.Col
	})
	for _, site := range cm.report.Sites {
		cm.report.Totals[string(site.Status)]++
		cm.report.ByKind[site.Kind+"/"+string(site.Status)]++
	}
	if options.Write {
		sort.Slice(edits, func(i, j int) bool { return edits[i].path < edits[j].path })
		for _, edit := range edits {
			for _, out := range edit.outputs {
				if err := os.WriteFile(out.path, out.content, 0o644); err != nil {
					return nil, err
				}
			}
			cm.report.Files = append(cm.report.Files, cm.rel(edit.path))
		}
	}
	return cm.report, nil
}

type fileEdit struct {
	path    string
	outputs []struct {
		path    string
		content []byte
	}
}

func (cm *codemod) rel(path string) string {
	rel, err := filepath.Rel(cm.root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// overlay generates the Go of every .templ file under the root, so the run
// sees each template's current signature whatever state the _templ.go
// files are in, and hides converter outputs that share a package with
// templ files.
func (cm *codemod) overlay() (map[string][]byte, error) {
	overlay := map[string][]byte{}
	dirs := map[string]bool{}
	err := filepath.WalkDir(cm.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != cm.root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			if path != cm.root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir // a nested module
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".templ") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		parsed, err := parser.ParseString(string(source))
		if err != nil {
			cm.report.Problems = append(cm.report.Problems, fmt.Sprintf("%s: %v", cm.rel(path), err))
			return nil
		}
		parsed.Filepath = path
		code, sourceMap, err := generate(parsed, cm.rel(path))
		if err != nil {
			cm.report.Problems = append(cm.report.Problems, fmt.Sprintf("%s: %v", cm.rel(path), err))
			return nil
		}
		goPath := strings.TrimSuffix(path, ".templ") + "_templ.go"
		overlay[goPath] = code
		cm.templFiles[goPath] = &templOverlay{templPath: path, source: source, sourceMap: sourceMap, code: code}
		dirs[filepath.Dir(path)] = true
		return nil
	})
	hideConverterOutputs(dirs, overlay)
	return overlay, err
}

// mapping returns the call mapping of a templ function, or nil.
func (cm *codemod) mapping(obj types.Object) *CallMapping {
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil || !cm.byPkg[fn.Pkg().Path()] {
		return nil
	}
	key := fn.Pkg().Path() + "."
	if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
		key += typeBaseName(recv.Type()) + "."
	}
	return cm.mappings[key+fn.Name()]
}

func (cm *codemod) site(site CallSite) {
	cm.report.Sites = append(cm.report.Sites, site)
}

// --- Go files ---

// goSite is one rewritable Go call site.
type goSite struct {
	m     *CallMapping
	call  *ast.CallExpr
	from  int // the replaced range: the call, or the whole Render call
	to    int
	pos   token.Pos
	end   token.Pos
	kind  string
	notes []string
	// render is the enclosing X(...).Render(ctx, w) call routed through
	// the fragment renderer.
	render    *ast.CallExpr
	qualifier string
	receiver  ast.Expr
	ctx       string
	manual    string
	replaced  string
	resolved  bool
	needsTime bool
	needsKTML bool
	needsFrag bool
	children  bool
	// parens is set when the literal would sit in an if, for, or switch
	// header outside any brackets, where Go needs it parenthesized.
	parens bool
}

// inClauseHeader reports whether the last node of stack sits in the header
// of an if, for, range, or switch statement with no enclosing parentheses,
// brackets, or braces in between: there a composite literal must be
// parenthesized.
func inClauseHeader(stack []ast.Node) bool {
	for i := len(stack) - 1; i > 0; i-- {
		child, parent := stack[i], stack[i-1]
		switch p := parent.(type) {
		case *ast.CallExpr:
			if child != p.Fun {
				return false
			}
		case *ast.ParenExpr, *ast.IndexExpr, *ast.IndexListExpr, *ast.CompositeLit, *ast.FuncLit, *ast.BlockStmt, *ast.SliceExpr:
			return false
		case *ast.IfStmt:
			return child != p.Body && child != p.Else
		case *ast.ForStmt:
			return child != p.Body
		case *ast.RangeStmt:
			return child != p.Body
		case *ast.SwitchStmt:
			return child != p.Body
		case *ast.TypeSwitchStmt:
			return child != p.Body
		}
	}
	return false
}

type goFileState struct {
	cm      *codemod
	pkg     *packages.Package
	fset    *token.FileSet
	file    *ast.File
	tok     *token.File
	path    string
	data    []byte
	sites   []*goSite
	imports map[string]string // import path -> name in this file
	// added holds the imports the rewrites need.
	added map[string]bool
}

func (cm *codemod) goFile(pkg *packages.Package, fset *token.FileSet, file *ast.File, path string, data []byte) *fileEdit {
	st := &goFileState{cm: cm, pkg: pkg, fset: fset, file: file, tok: fset.File(file.Pos()), path: path, data: data, imports: map[string]string{}, added: map[string]bool{}}
	if st.tok.Size() != len(data) {
		// The syntax is not this file's on-disk content (an overlay).
		return nil
	}
	for _, spec := range file.Imports {
		importPath, _ := strconv.Unquote(spec.Path.Value)
		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
		} else if imported := pkg.Imports[importPath]; imported != nil {
			name = imported.Name
		} else {
			name = filepath.Base(importPath)
		}
		st.imports[importPath] = name
	}
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		stack = append(stack, n)
		switch node := n.(type) {
		case *ast.Ident:
			st.ident(node, stack)
		case *ast.SelectorExpr:
			st.templAPI(node, stack)
		}
		return true
	})
	if len(st.sites) == 0 {
		return nil
	}
	return st.rewrite()
}

func (st *goFileState) offset(pos token.Pos) int { return st.tok.Offset(pos) }

func (st *goFileState) position(pos token.Pos) Position {
	p := st.fset.Position(pos)
	return Position{Line: p.Line, Col: p.Column}
}

// templAPI reports the templ APIs a migration touches beyond constructor
// calls: context-carried children and templ.Component types.
func (st *goFileState) templAPI(sel *ast.SelectorExpr, stack []ast.Node) {
	obj := st.pkg.TypesInfo.Uses[sel.Sel]
	if obj == nil || obj.Pkg() == nil || obj.Pkg().Path() != templPath {
		return
	}
	switch obj.Name() {
	case "WithChildren":
		st.cm.site(CallSite{File: st.cm.rel(st.path), From: st.position(sel.Pos()), To: st.position(sel.End()), Kind: "with-children", Status: SiteManual,
			Reason: "context-carried children: a KTML component reads children only from its Children field (ADR-0003); set it where the component is built"})
	case "Component":
		if _, isType := obj.(*types.TypeName); isType {
			st.cm.site(CallSite{File: st.cm.rel(st.path), From: st.position(sel.Pos()), To: st.position(sel.End()), Kind: "component-type", Status: SiteDeferred,
				Reason: "ktml.Component values are assignable to templ.Component; renamed when templ leaves the binary (Phase 8)"})
		}
	case "Handler":
		st.cm.site(CallSite{File: st.cm.rel(st.path), From: st.position(sel.Pos()), To: st.position(sel.End()), Kind: "handler", Status: SiteDeferred,
			Reason: "templ.Handler buffers the response and accepts a KTML component unchanged"})
	}
}

// ident classifies a use of a converted template.
func (st *goFileState) ident(ident *ast.Ident, stack []ast.Node) {
	m := st.cm.mapping(st.pkg.TypesInfo.Uses[ident])
	if m == nil {
		return
	}
	// The expression naming the template: X, pkg.X, or recv.X.
	var expr ast.Expr = ident
	parent := len(stack) - 2
	var receiver ast.Expr
	qualifier := ""
	if parent >= 0 {
		if sel, ok := stack[parent].(*ast.SelectorExpr); ok && sel.Sel == ident {
			expr = sel
			parent--
			if selection := st.pkg.TypesInfo.Selections[sel]; selection != nil {
				receiver = sel.X
			} else if x, ok := sel.X.(*ast.Ident); ok {
				qualifier = x.Name + "."
			}
		}
	}
	if parent >= 0 {
		if lit, ok := stack[parent].(*ast.CompositeLit); ok && lit.Type == expr {
			// Already the component's struct literal: a rewritten site,
			// seen while the templ function still shadows the type.
			return
		}
	}
	site := &goSite{m: m, pos: expr.Pos(), end: expr.End(), kind: "call"}
	call, isCall := (*ast.CallExpr)(nil), false
	if parent >= 0 {
		call, isCall = stack[parent].(*ast.CallExpr)
		isCall = isCall && call.Fun == expr
	}
	if !isCall {
		site.kind = "value"
		site.manual = "the template is used as a Go function value; a KTML component is a struct, so the code that stores or calls it needs a hand edit"
		st.sites = append(st.sites, site)
		return
	}
	site.call, site.pos, site.end = call, call.Pos(), call.End()
	site.parens = inClauseHeader(stack[:parent+1])
	site.from, site.to = st.offset(call.Pos()), st.offset(call.End())
	if receiver != nil {
		site.receiver = receiver
		name, ok := st.packageName(m.Package)
		if !ok {
			site.manual = "a method template's component lives in package " + m.Package + ", which this file does not import"
		}
		qualifier = name
	}
	site.qualifier = qualifier
	if m.Children != "" {
		site.children = true
		site.manual = "the template renders { children... }, which Go code passes through templ.WithChildren; set the component's Children field by hand"
	}
	if site.manual == "" {
		site.manual = st.cm.inputsChanged(m)
	}
	if site.manual == "" && m.Mode == "view" {
		if sig := st.cm.presenter(m); sig.found {
			want := len(m.Params) + boolInt(m.Context) + boolInt(m.Clock) + boolInt(m.Receiver != "")
			for _, p := range m.Params {
				if p.Kind != "presenter" {
					want--
				}
			}
			switch {
			case sig.result != 1:
				site.manual = fmt.Sprintf("the presenter %s returns %d results (a hand-finished presenter that can fail); handle its error at the call site", m.Presenter, sig.result)
			case sig.params != want:
				site.manual = fmt.Sprintf("the presenter %s takes %d parameters, not the %d the conversion recorded; it was edited by hand", m.Presenter, sig.params, want)
			}
		}
		if site.manual == "" && m.Context {
			site.ctx = st.contextAt(call.Pos())
			if site.ctx == "" {
				site.manual = "the presenter " + m.Presenter + " takes the request context, and no context.Context, *http.Request, or *testing.T is in scope"
			}
		}
	}
	// X(…).Render(ctx, w) into an http.ResponseWriter.
	if parent >= 2 {
		if sel, ok := stack[parent-1].(*ast.SelectorExpr); ok && sel.X == call && sel.Sel.Name == "Render" {
			if render, ok := stack[parent-2].(*ast.CallExpr); ok && render.Fun == sel && len(render.Args) == 2 && st.responseWriter(render.Args[1]) {
				if st.cm.fragment.name != "" {
					site.render = render
					site.from, site.to = st.offset(render.Pos()), st.offset(render.End())
					site.pos, site.end = render.Pos(), render.End()
					site.notes = append(site.notes, "fragment render routed through "+st.cm.options.FragmentRender+", which buffers it (ADR-0003)")
				} else {
					site.notes = append(site.notes, "renders straight into the http.ResponseWriter, unbuffered: ADR-0003 buffers fragments; route it through the application renderer")
				}
			}
		}
	}
	st.sites = append(st.sites, site)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// packageName returns how this file refers to a package: "" for its own
// package, "name." for an import.
func (st *goFileState) packageName(path string) (string, bool) {
	if st.pkg.Types != nil && st.pkg.Types.Path() == path {
		return "", true
	}
	if name, ok := st.imports[path]; ok && name != "_" && name != "." {
		return name + ".", true
	}
	return "", false
}

// responseWriter reports an expression of type net/http.ResponseWriter.
func (st *goFileState) responseWriter(expr ast.Expr) bool {
	t := st.pkg.TypesInfo.TypeOf(expr)
	return t != nil && isNamed(t, "net/http", "ResponseWriter")
}

// contextAt finds a context for a presenter at pos: a context.Context
// variable in scope (ctx preferred), else r.Context() of an *http.Request,
// else t.Context() of a *testing.T.
func (st *goFileState) contextAt(pos token.Pos) string {
	scope := st.pkg.Types.Scope().Innermost(pos)
	var request, test string
	for s := scope; s != nil && s != st.pkg.Types.Scope(); s = s.Parent() {
		var contexts []string
		for _, name := range s.Names() {
			v, ok := s.Lookup(name).(*types.Var)
			if !ok || v.Pos() > pos && s != st.pkg.Types.Scope() {
				continue
			}
			switch {
			case isContext(v.Type()):
				contexts = append(contexts, name)
			case request == "" && isPointerTo(v.Type(), "net/http", "Request"):
				request = name + ".Context()"
			case test == "" && (isPointerTo(v.Type(), "testing", "T") || isPointerTo(v.Type(), "testing", "B")):
				test = name + ".Context()"
			}
		}
		for _, name := range contexts {
			if name == "ctx" {
				return name
			}
		}
		if len(contexts) > 0 {
			return contexts[0]
		}
	}
	if request != "" {
		return request
	}
	return test
}

func isPointerTo(t types.Type, pkg, name string) bool {
	pointer, ok := t.(*types.Pointer)
	return ok && isNamed(pointer.Elem(), pkg, name)
}

// presenter finds the presenter function of a mapping in its package's
// directory, to check that its signature still matches the mapping.
func (cm *codemod) presenter(m *CallMapping) presenterSig {
	key := m.Package + "." + m.Presenter
	if sig, ok := cm.presenters[key]; ok {
		return sig
	}
	sig := presenterSig{}
	dir := filepath.Join(cm.root, filepath.FromSlash(filepath.Dir(m.File)))
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, goparser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != m.Presenter {
				continue
			}
			sig.found = true
			for _, field := range fn.Type.Params.List {
				sig.params += max(len(field.Names), 1)
			}
			if fn.Type.Results != nil {
				for _, field := range fn.Type.Results.List {
					sig.result += max(len(field.Names), 1)
				}
			}
		}
	}
	cm.presenters[key] = sig
	return sig
}

// replacement spells a site's rewrite, rewriting nested sites in its
// arguments first.
func (st *goFileState) replacement(site *goSite) (string, error) {
	if site.resolved {
		if site.manual != "" {
			return "", fmt.Errorf("%s", site.manual)
		}
		return site.replaced, nil
	}
	site.resolved = true
	if site.manual != "" {
		return "", fmt.Errorf("%s", site.manual)
	}
	call := site.call
	args := make([]string, len(call.Args))
	for i, arg := range call.Args {
		args[i] = st.render(st.offset(arg.Pos()), st.offset(arg.End()), site)
	}
	receiver := ""
	if site.receiver != nil {
		receiver = st.render(st.offset(site.receiver.Pos()), st.offset(site.receiver.End()), site)
	}
	join := func(parts []string) string {
		site.needsKTML = true
		return st.ktmlName() + ".Join(" + strings.Join(parts, ", ") + ")"
	}
	now := ""
	if site.m.Clock {
		now = st.importName("time", "time") + ".Now()"
		site.needsTime = true
		site.notes = append(site.notes, "the clock is read when the component is built, not when it renders")
	}
	// A nil templ.Attributes is an empty bag, given explicitly: ktml vet
	// requires every input. Any other templ.Attributes needs a hand edit.
	attributes := func(arg string) (string, error) {
		if arg != "nil" {
			return "", fmt.Errorf("the templ.Attributes argument %s needs a ktml.Attributes built with the context-checked constructors", arg)
		}
		site.needsKTML = true
		return st.ktmlName() + ".Attributes{}", nil
	}
	literal, err := site.m.Literal(site.qualifier, receiver, args, call.Ellipsis.IsValid(), site.ctx, now, join, attributes)
	if err != nil {
		site.manual = err.Error()
		site.needsTime, site.needsKTML = false, false
		return "", err
	}
	if site.parens && site.render == nil {
		literal = "(" + literal + ")"
	}
	if site.render != nil {
		ctx := st.render(st.offset(site.render.Args[0].Pos()), st.offset(site.render.Args[0].End()), site)
		w := st.render(st.offset(site.render.Args[1].Pos()), st.offset(site.render.Args[1].End()), site)
		name, _ := st.packageName(st.cm.fragment.path)
		if name == "" && st.pkg.Types.Path() != st.cm.fragment.path {
			name = st.importName(st.cm.fragment.path, filepath.Base(st.cm.fragment.path)) + "."
			site.needsFrag = true
		}
		literal = name + st.cm.fragment.name + "(" + ctx + ", " + w + ", " + literal + ")"
	}
	site.replaced = literal
	return literal, nil
}

// render returns the source text of [from, to) with the rewrites of the
// sites inside it, other than outer, applied.
func (st *goFileState) render(from, to int, outer *goSite) string {
	var b strings.Builder
	cursor := from
	for _, site := range st.sites {
		if site == outer || site.call == nil || site.from < cursor || site.to > to || site.from < from {
			continue
		}
		text, err := st.replacement(site)
		if err != nil {
			continue
		}
		b.Write(st.data[cursor:site.from])
		b.WriteString(text)
		cursor = site.to
	}
	b.Write(st.data[cursor:to])
	return b.String()
}

// importName returns the name this file uses for an import path, adding
// the import when the rewrite needs it.
func (st *goFileState) importName(path, name string) string {
	if existing, ok := st.imports[path]; ok && existing != "_" && existing != "." {
		return existing
	}
	st.added[path] = true
	st.imports[path] = name
	return name
}

func (st *goFileState) ktmlName() string { return st.importName(ktmlPath, "ktml") }

// rewrite records every site and returns the file's edit.
func (st *goFileState) rewrite() *fileEdit {
	sort.Slice(st.sites, func(i, j int) bool {
		if st.sites[i].from != st.sites[j].from {
			return st.sites[i].from < st.sites[j].from
		}
		return st.sites[i].to > st.sites[j].to
	})
	// Top-level sites are those no other rewritable site contains.
	text := st.render(0, len(st.data), nil)
	changed := false
	for _, site := range st.sites {
		original := string(st.data[st.offset(site.pos):st.offset(site.end)])
		cs := CallSite{File: st.cm.rel(st.path), From: st.position(site.pos), To: st.position(site.end), Kind: site.kind, Template: site.m.Package + "." + site.m.Template, Original: original, Notes: site.notes}
		if site.call != nil && !site.resolved {
			_, _ = st.replacement(site)
		}
		if site.manual != "" {
			cs.Status, cs.Reason = SiteManual, site.manual
		} else {
			cs.Status, cs.Replacement = SiteRewritable, site.replaced
			if st.cm.options.Write {
				cs.Status = SiteRewritten
			}
			changed = true
		}
		st.cm.site(cs)
	}
	if !changed {
		return nil
	}
	content, err := st.finish(text)
	if err != nil {
		st.cm.report.Problems = append(st.cm.report.Problems, fmt.Sprintf("%s: the rewrite does not parse; nothing written: %v", st.cm.rel(st.path), err))
		return nil
	}
	return &fileEdit{path: st.path, outputs: []struct {
		path    string
		content []byte
	}{{st.path, content}}}
}

// finish adds the imports the rewrites need and formats the file.
func (st *goFileState) finish(text string) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, st.path, text, goparser.ParseComments)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(st.added))
	for path := range st.added {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		name := st.imports[path]
		if name == filepath.Base(path) {
			astutil.AddImport(fset, file, path)
		} else {
			astutil.AddNamedImport(fset, file, name, path)
		}
	}
	var b bytes.Buffer
	if err := format.Node(&b, fset, file); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// --- templ files ---

// templFile renames the calls of converted templates in an unconverted
// .templ file to their bridges, and regenerates its Go.
func (cm *codemod) templFile(pkg *packages.Package, fset *token.FileSet, file *ast.File, t *templOverlay) *fileEdit {
	type rename struct {
		offset int
		name   string
	}
	var renames []rename
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		stack = append(stack, n)
		ident, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		m := cm.mapping(pkg.TypesInfo.Uses[ident])
		if m == nil {
			return true
		}
		target := fset.Position(ident.Pos())
		src, ok := t.sourceMap.SourcePositionFromTarget(uint32(target.Line-1), uint32(target.Column-1))
		site := CallSite{File: cm.rel(t.templPath), Kind: "templ-call", Template: m.Package + "." + m.Template, Original: ident.Name}
		if !ok {
			// Generated code of the template declaration itself, not a
			// use in this file's expressions.
			return true
		}
		offset, found := sourceOffset(t.source, src, ident.Name)
		site.From = Position{Line: int(src.Line) + 1, Col: int(src.Col) + 1}
		site.To = Position{Line: site.From.Line, Col: site.From.Col + len(ident.Name)}
		var expr ast.Node = ident
		if len(stack) >= 2 {
			if sel, ok := stack[len(stack)-2].(*ast.SelectorExpr); ok && sel.Sel == ident {
				expr = sel
			}
		}
		isCall := false
		for i := len(stack) - 2; i >= 0; i-- {
			if call, ok := stack[i].(*ast.CallExpr); ok {
				isCall = call.Fun == expr
				break
			}
			if stack[i] == expr {
				continue
			}
			if _, ok := stack[i].(*ast.SelectorExpr); !ok {
				break
			}
		}
		switch {
		case !found:
			site.Status, site.Reason = SiteManual, "the call's source text could not be located in the .templ file"
		case !isCall:
			site.Kind, site.Status, site.Reason = "templ-value", SiteManual, "the template is used as a Go value in templ code"
		case m.Bridge == "":
			site.Status, site.Reason = SiteManual, "no templ bridge was emitted for this template (unexported or a method); convert this file in the same batch"
		default:
			site.Status, site.Replacement = SiteBridged, m.Bridge
			renames = append(renames, rename{offset: offset, name: ident.Name})
		}
		cm.site(site)
		return true
	})
	if len(renames) == 0 || !cm.options.Write {
		return nil
	}
	sort.Slice(renames, func(i, j int) bool { return renames[i].offset > renames[j].offset })
	source := append([]byte(nil), t.source...)
	seen := map[int]bool{}
	for _, r := range renames {
		if seen[r.offset] {
			continue
		}
		seen[r.offset] = true
		end := r.offset + len(r.name)
		source = append(source[:end], append([]byte("Templ"), source[end:]...)...)
	}
	parsed, err := parser.ParseString(string(source))
	if err != nil {
		cm.report.Problems = append(cm.report.Problems, fmt.Sprintf("%s: the renamed file does not parse; nothing written: %v", cm.rel(t.templPath), err))
		return nil
	}
	var code bytes.Buffer
	if _, err := generator.Generate(parsed, &code, generator.WithFileName(cm.rel(t.templPath)), generator.WithVersion(templ.Version())); err != nil {
		cm.report.Problems = append(cm.report.Problems, fmt.Sprintf("%s: regenerating its Go failed; nothing written: %v", cm.rel(t.templPath), err))
		return nil
	}
	formatted, err := format.Source(code.Bytes())
	if err != nil {
		formatted = code.Bytes()
	}
	return &fileEdit{path: t.templPath, outputs: []struct {
		path    string
		content []byte
	}{{t.templPath, source}, {strings.TrimSuffix(t.templPath, ".templ") + "_templ.go", formatted}}}
}

// sourceOffset converts a templ source position to a byte offset, and
// checks that the template's name is spelled there.
func sourceOffset(source []byte, p parser.Position, name string) (int, bool) {
	line := 0
	start := 0
	for i := 0; i < len(source) && line < int(p.Line); i++ {
		if source[i] == '\n' {
			line++
			start = i + 1
		}
	}
	// templ's source map counts bytes; the column fallback covers a
	// position found by its backwards search.
	candidates := []int{int(p.Index), start + int(p.Col)}
	offset, col := start, 0
	for offset < len(source) && col < int(p.Col) {
		_, size := utf8.DecodeRune(source[offset:])
		offset += size
		col++
	}
	candidates = append(candidates, offset)
	for _, candidate := range candidates {
		if candidate >= 0 && candidate+len(name) <= len(source) && string(source[candidate:candidate+len(name)]) == name {
			return candidate, true
		}
	}
	return 0, false
}

// inputsChanged compares a component's declared inputs, in its KTML
// document, with the ones its mapping fills. They differ when the outputs
// were finished by hand after the conversion recorded the mapping: an input
// added (a slot Go must fill), renamed, or removed (a view replaced by
// Presentation State inputs). It returns why the site is manual, or "".
// Without the document (a mapping from --out) nothing is checked.
func (cm *codemod) inputsChanged(m *CallMapping) string {
	if reason, ok := cm.inputs[m]; ok {
		return reason
	}
	reason := ""
	dir := filepath.Join(cm.root, filepath.FromSlash(filepath.Dir(m.File)))
	if declared, ok := declaredInputs(dir, m.Component); ok {
		filled := map[string]bool{}
		if m.ViewField != "" {
			filled[lowerFirst(m.ViewField)] = true
		}
		if m.Children != "" {
			filled["children"] = true
		}
		for _, p := range m.Params {
			if p.Field != "" {
				filled[lowerFirst(p.Field)] = true
			}
		}
		var added, removed []string
		for name := range declared {
			if !filled[name] {
				added = append(added, name)
			}
		}
		for name := range filled {
			if !declared[name] {
				removed = append(removed, name)
			}
		}
		sort.Strings(added)
		sort.Strings(removed)
		if len(added) > 0 || len(removed) > 0 {
			reason = fmt.Sprintf("the component %s's inputs changed after the conversion (declared but not filled: %v; filled but not declared: %v); build it by hand", m.Component, added, removed)
		}
	}
	cm.inputs[m] = reason
	return reason
}

// declaredInputs reads the input names of a component's first declaration
// header in the KTML documents of dir.
func declaredInputs(dir, component string) (map[string]bool, bool) {
	document := componentDocument(dir, component)
	if document == "" {
		return nil, false
	}
	data, err := os.ReadFile(document)
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, component) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, component))
		inputs := map[string]bool{}
		if !strings.HasPrefix(rest, "[") {
			if rest == "{" || rest == "" {
				return inputs, true
			}
			continue
		}
		depth, start := 0, 1
		for i, r := range rest {
			switch r {
			case '[':
				depth++
			case ']':
				depth--
				if depth == 0 {
					addInput(inputs, rest[start:i])
					return inputs, true
				}
			case ',':
				if depth == 1 {
					addInput(inputs, rest[start:i])
					start = i + 1
				}
			}
		}
		return nil, false // a header continued over lines
	}
	return nil, false
}

func addInput(inputs map[string]bool, declaration string) {
	name := strings.TrimSpace(declaration)
	if i := strings.IndexAny(name, ":=?"); i >= 0 {
		name = strings.TrimSpace(name[:i])
	}
	if name != "" {
		inputs[name] = true
	}
}
