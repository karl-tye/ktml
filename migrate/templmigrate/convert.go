package templmigrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	parser "github.com/a-h/templ/parser/v2"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
	"github.com/karl-tye/ktml/internal/gopkg"
)

// ConversionSchema identifies the JSON report of a conversion run.
const ConversionSchema = "ktml.migrate.templ.conversion/v1"

// ConvertOptions configure a conversion run.
type ConvertOptions struct {
	Options
	// Out is the directory that receives the converted tree. Each templ
	// file's outputs go to its own relative directory under Out. The
	// source .templ files are never written.
	Out string
	// ScaffoldPresenters emits Host Schemas and presenter scaffolds. Without
	// it, a template that needs a presenter is partially converted.
	ScaffoldPresenters bool
	// InPlace writes every output into its templ file's own directory, so
	// presenters live in the source Go package (Phase 7). Out is ignored.
	// Existing outputs are kept unless Force is set: presenters are edited
	// by hand after the first run.
	InPlace bool
	// Force overwrites existing in-place outputs.
	Force bool
	// RemoveTempl deletes the batch's .templ and _templ.go files after an
	// in-place conversion, but only when the batch is complete: no node is
	// partial or blocked (unless HandFinished), the KTML validates, and no
	// Go or templ code outside the batch still calls its templates.
	RemoveTempl bool
	// HandFinished declares the batch's partial nodes finished by hand in
	// the kept outputs, so RemoveTempl does not refuse because of them.
	HandFinished bool
	// Bridges controls the templ bridges of converted templates that
	// unconverted templ files call: "auto" (default) emits one for each
	// such template, "all" for every exported template, "none" for none.
	Bridges string
}

// Conversion summarizes what a conversion run emitted.
type Conversion struct {
	Out         string           `json:"out"`
	Outputs     []OutputFile     `json:"outputs"`
	Templates   []TemplateOutput `json:"templates"`
	Diagnostics []Diagnostic     `json:"diagnostics"`
	Security    []SecurityMarker `json:"securityMarkers"`
	Partial     int              `json:"partialNodes"`
	Validation  []string         `json:"validation,omitempty"`
	Fields      FieldCounts      `json:"fields"`
	// Switches counts the templ switches converted to Render Clauses over
	// a Presentation State, and those kept as presenter booleans.
	Switches SwitchCounts      `json:"switches"`
	Lines    map[string]int    `json:"lines"`
	Helpers  map[string]string `json:"helpers,omitempty"`
	// InPlace is set when the outputs were written into the source
	// packages.
	InPlace bool `json:"inPlace,omitempty"`
	// Kept lists the in-place outputs that already existed and were left
	// as they are.
	Kept []string `json:"kept,omitempty"`
	// Outdated lists the kept outputs that the converter now emits
	// differently than when they were written: a later converter fix, or a
	// changed templ source. The current emission is
	// .ktml/migrate/kept/<output>.fresh, beside the original one,
	// <output>.pristine, for a three-way merge.
	Outdated []string `json:"outdated,omitempty"`
	// Calls maps every converted template to its KTML spelling, for the Go
	// and templ call-site codemod.
	Calls []CallMapping `json:"calls,omitempty"`
	// Removed lists the .templ and _templ.go files deleted by RemoveTempl,
	// and RemoveRefused why they were not deleted.
	Removed       []string `json:"removed,omitempty"`
	RemoveRefused []string `json:"removeRefused,omitempty"`
}

// SwitchCounts counts the conversions of templ switches.
type SwitchCounts struct {
	Clauses  int `json:"clauses"`
	Booleans int `json:"booleans"`
}

// OutputFile is one written file.
type OutputFile struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"` // ktml, ktmlschema, presenter, helpers, sourcemap
	Source string `json:"source"`
	Lines  int    `json:"lines"`
}

// TemplateOutput describes the KTML component one templ declaration became.
type TemplateOutput struct {
	File      string      `json:"file"`
	Template  string      `json:"template"`
	Component string      `json:"component"`
	Mode      string      `json:"mode"` // direct or view
	View      string      `json:"view,omitempty"`
	Presenter string      `json:"presenter,omitempty"`
	Fields    FieldCounts `json:"fields"`
	Partial   int         `json:"partialNodes"`
}

// FieldCounts classifies presenter-built Host Schema fields.
type FieldCounts struct {
	// PassThrough fields copy a Go path or presence check unchanged.
	PassThrough int `json:"passThrough"`
	// Derived fields compute a value: formatting, calls, comparisons, URL or
	// script construction.
	Derived int `json:"derived"`
	// Wiring fields hold a child view, a collection, or an item key.
	Wiring int `json:"wiring"`
	Total  int `json:"total"`
}

func (f *FieldCounts) add(kind fieldKind) {
	f.Total++
	switch kind {
	case fieldPassThrough:
		f.PassThrough++
	case fieldDerived:
		f.Derived++
	default:
		f.Wiring++
	}
}

func (f *FieldCounts) merge(other FieldCounts) {
	f.PassThrough += other.PassThrough
	f.Derived += other.Derived
	f.Wiring += other.Wiring
	f.Total += other.Total
}

// Diagnostic is a source-ranged conversion diagnostic.
type Diagnostic struct {
	File     string   `json:"file"`
	From     Position `json:"from"`
	To       Position `json:"to"`
	Severity string   `json:"severity"` // partial, review, security
	Code     string   `json:"code"`
	Message  string   `json:"message"`
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%s:%d:%d: %s [%s] %s", d.File, d.From.Line, d.From.Col, d.Severity, d.Code, d.Message)
}

// SecurityMarker records a trust boundary the converter emitted.
type SecurityMarker struct {
	File     string   `json:"file"`
	At       Position `json:"at"`
	Boundary string   `json:"boundary"` // trusted-url, trusted-script, json, css, attributes
	Output   string   `json:"output"`
}

// conversion is one run's state.
type conversion struct {
	run     *run
	opts    ConvertOptions
	out     string
	report  *Report
	summary *Conversion

	sigs   []*tmplSig
	sigFor map[string]*tmplSig // "<pkg path>.<name>"
	files  map[*sourceFile]*fileEmitter
	// convertedSource holds the import paths of the converted Go packages.
	convertedSource map[string]bool
	// convertedFiles holds the generated file names of converted templ files.
	convertedFiles map[string]bool
	// nodes indexes each file's classified nodes by kind and range.
	nodes map[*sourceFile]map[string][]int
	// broken holds the moved declarations that cannot compile in a
	// presenter package, with the reason.
	broken map[types.Object]string
	// poisoned holds the locals of presenter statements commented out as
	// unresolved; code that uses them is unresolved too.
	poisoned map[types.Object]string
	// foreign caches the signatures behind templ bridges, by function.
	foreign map[string]*tmplSig
	// bridged holds the templ bridge emitted for a template.
	bridged map[*tmplSig]string
	// handwritten caches handwrittenNames per directory.
	handwritten map[string]map[string]bool
}

// Convert classifies and converts the given .templ files into KTML, Host
// Schemas, presenter scaffolds, and source maps under options.Out.
func Convert(paths []string, options ConvertOptions) (*Report, error) {
	if options.Out == "" && !options.InPlace {
		return nil, errors.New("convert: no output directory")
	}
	if (options.RemoveTempl || options.HandFinished || options.Force) && !options.InPlace {
		return nil, errors.New("convert: --remove-templ, --hand-finished, and --force need --in-place")
	}
	switch options.Bridges {
	case "":
		options.Bridges = "auto"
	case "auto", "all", "none":
	default:
		return nil, fmt.Errorf("convert: --bridges must be auto, all, or none, not %q", options.Bridges)
	}
	if options.NoTypes {
		return nil, errors.New("convert: conversion needs go/types; --no-types is report-only")
	}
	report, r, err := inventoryRun(paths, options.Options)
	if err != nil {
		return nil, err
	}
	outDir := options.Out
	if options.InPlace {
		outDir = r.root
	}
	out, err := filepath.Abs(outDir)
	if err != nil {
		return nil, err
	}
	c := &conversion{
		run: r, opts: options, out: out, report: report,
		summary:         &Conversion{Out: out, Lines: map[string]int{}, InPlace: options.InPlace},
		sigFor:          map[string]*tmplSig{},
		files:           map[*sourceFile]*fileEmitter{},
		convertedSource: map[string]bool{},
		convertedFiles:  map[string]bool{},
		nodes:           map[*sourceFile]map[string][]int{},
		poisoned:        map[types.Object]string{},
		handwritten:     map[string]map[string]bool{},
		bridged:         map[*tmplSig]string{},
		foreign:         map[string]*tmplSig{},
	}
	if err := c.convert(); err != nil {
		return nil, err
	}
	report.Schema = ConversionSchema
	report.Conversion = c.summary
	return report, nil
}

func (c *conversion) convert() error {
	for _, file := range c.run.files {
		if !c.convertible(file) {
			continue
		}
		t := c.run.typed[file]
		c.convertedSource[t.pkg.Path()] = true
		c.convertedFiles[t.tok.Name()] = true
		index := map[string][]int{}
		for i, n := range c.run.walkers[file].nodes {
			key := nodeKey(n.Kind, n.From, n.To)
			index[key] = append(index[key], i)
		}
		c.nodes[file] = index
	}
	for _, file := range c.run.files {
		if c.convertible(file) {
			if err := c.signatures(file); err != nil {
				return err
			}
		}
	}
	c.decideModes()
	c.checkMoves()
	for _, file := range c.run.files {
		if !c.convertible(file) {
			c.skipped(file)
			continue
		}
		fe, err := newFileEmitter(c, file)
		if err != nil {
			return err
		}
		c.files[file] = fe
	}
	// Emission order is file order; every signature is known up front.
	for _, file := range c.run.files {
		if fe := c.files[file]; fe != nil {
			fe.emit()
		}
	}
	if err := c.write(); err != nil {
		return err
	}
	c.validate()
	c.finish()
	if c.opts.RemoveTempl {
		c.removeTempl()
	}
	return nil
}

// convertible reports whether a file is emitted: parsed, typed, and not
// excluded.
func (c *conversion) convertible(file *sourceFile) bool {
	return file.parsed != nil && file.excluded == "" && c.run.typed[file] != nil
}

// skipped records why a file was not emitted.
func (c *conversion) skipped(file *sourceFile) {
	reason := "excluded: " + file.excluded
	switch {
	case file.parsed == nil:
		reason = "parse failure"
	case file.excluded == "" && c.run.typed[file] == nil:
		reason = "types unavailable"
	}
	if file.excluded != "" {
		c.summary.Diagnostics = append(c.summary.Diagnostics, Diagnostic{File: file.rel, Severity: "review", Code: "file-not-converted", Message: reason})
		return
	}
	c.summary.Diagnostics = append(c.summary.Diagnostics, Diagnostic{File: file.rel, Severity: "partial", Code: "file-not-converted", Message: reason})
	c.summary.Partial++
}

func nodeKey(kind string, from, to Position) string {
	return fmt.Sprintf("%s@%d:%d-%d:%d", kind, from.Line, from.Col, to.Line, to.Col)
}

// node returns the classified node of a templ construct, or nil.
func (c *conversion) node(file *sourceFile, kind string, r parser.Range) *Node {
	from, to := sourcePosition(r.From), sourcePosition(r.To)
	indexes := c.nodes[file][nodeKey(kind, from, to)]
	if len(indexes) == 0 {
		return nil
	}
	return &c.run.walkers[file].nodes[indexes[0]]
}

// movedDeclaration reports whether obj is declared by Go code in a
// converted templ file. Such functions, constants, variables, and
// unexported types move into the presenters; exported types stay in their
// source package, which presenters import.
func (c *conversion) movedDeclaration(obj types.Object) bool {
	// In place, the templ file is removed, so every declaration moves.
	if _, isType := obj.(*types.TypeName); isType && obj.Exported() && !c.opts.InPlace {
		return false
	}
	for _, t := range c.run.typed {
		if obj.Pkg() == t.pkg {
			return c.convertedFiles[t.fset.Position(obj.Pos()).Filename]
		}
	}
	return false
}

// unreachableType returns the first unexported type in t that a presenter
// package can neither import nor declare itself, or "".
func (c *conversion) unreachableType(t types.Type) string {
	switch u := types.Unalias(t).(type) {
	case *types.Named:
		obj := u.Obj()
		if obj.Pkg() != nil && !obj.Exported() && !(c.opts.InPlace && c.convertedSource[obj.Pkg().Path()]) {
			if !c.movedDeclaration(obj) {
				return obj.Name()
			}
			if _, bad := c.broken[obj]; bad {
				return obj.Name()
			}
		}
		if args := u.TypeArgs(); args != nil {
			for i := 0; i < args.Len(); i++ {
				if name := c.unreachableType(args.At(i)); name != "" {
					return name
				}
			}
		}
	case *types.Pointer:
		return c.unreachableType(u.Elem())
	case *types.Slice:
		return c.unreachableType(u.Elem())
	case *types.Array:
		return c.unreachableType(u.Elem())
	case *types.Map:
		if name := c.unreachableType(u.Key()); name != "" {
			return name
		}
		return c.unreachableType(u.Elem())
	}
	return ""
}

// movedDecl is one Go declaration of a converted templ file.
type movedDecl struct {
	file       *sourceFile
	start, end int
	objects    []types.Object
}

// movedDecls lists the declarations a file's Go blocks move into its
// presenter: functions without receivers, constants, variables, and
// unexported types.
func (c *conversion) movedDecls(file *sourceFile) []movedDecl {
	t := c.run.typed[file]
	rw := newGoRewriter(c, file, newGoImports(""))
	var decls []movedDecl
	for _, node := range file.parsed.Nodes {
		block, ok := node.(*parser.TemplateFileGoExpression)
		if !ok || block.BeforePackage {
			continue
		}
		start, end, ok := rw.span(block.Expression)
		if !ok {
			continue
		}
		for _, decl := range t.file.Decls {
			from := t.tok.Offset(decl.Pos())
			if from < start || from >= end {
				continue
			}
			d := movedDecl{file: file, start: from, end: t.tok.Offset(decl.End())}
			if doc := declDoc(decl); doc != nil {
				d.start = t.tok.Offset(doc.Pos())
			}
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Recv != nil && !c.opts.InPlace {
					continue
				}
				d.objects = append(d.objects, t.info.Defs[decl.Name])
			case *ast.GenDecl:
				if decl.Tok == token.IMPORT {
					continue
				}
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							d.objects = append(d.objects, t.info.Defs[name])
						}
					case *ast.TypeSpec:
						if !spec.Name.IsExported() || c.opts.InPlace {
							d.objects = append(d.objects, t.info.Defs[spec.Name])
						}
					}
				}
			}
			if len(d.objects) > 0 {
				decls = append(decls, d)
			}
		}
	}
	return decls
}

// checkMoves finds the moved declarations that cannot compile in a
// presenter package, because they use unexported identifiers of their
// source package, and then everything that uses those, transitively.
// References to them make presenter code partial.
func (c *conversion) checkMoves() {
	c.broken = map[types.Object]string{}
	var all []movedDecl
	for _, file := range c.run.files {
		if c.convertible(file) {
			all = append(all, c.movedDecls(file)...)
		}
	}
	for _, d := range all {
		_, problems := newGoRewriter(c, d.file, newGoImports("")).rewrite(d.start, d.end)
		if len(problems) > 0 {
			for _, obj := range d.objects {
				c.broken[obj] = problems[0]
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, d := range all {
			if _, done := c.broken[d.objects[0]]; done {
				continue
			}
			t := c.run.typed[d.file]
			for _, ident := range newGoRewriter(c, d.file, newGoImports("")).idents {
				offset := t.tok.Offset(ident.Pos())
				if offset < d.start || offset >= d.end {
					continue
				}
				if used := t.info.Uses[ident]; used != nil {
					if reason, bad := c.broken[used]; bad {
						for _, obj := range d.objects {
							c.broken[obj] = fmt.Sprintf("uses %s, which cannot move (%s)", used.Name(), reason)
						}
						changed = true
						break
					}
				}
			}
		}
	}
}

// templateObject reports whether obj is a templ template.
func (c *conversion) templateObject(obj types.Object) bool {
	if obj.Pkg() == nil {
		return false
	}
	_, ok := c.sigFor[obj.Pkg().Path()+"."+obj.Name()]
	return ok
}

// tmplSig is the KTML signature of one templ declaration.
type tmplSig struct {
	file *sourceFile
	tmpl *parser.HTMLTemplate
	name string // templ name
	// component is the KTML component name: the templ name, or, when a
	// same-named template of another package is called in this package,
	// the name qualified by the package (KTML has no component aliases).
	component string
	key       string
	fn        *types.Func
	decl      *ast.FuncDecl
	receiver  *sigParam
	params    []sigParam
	view      bool
	// viewInput is the KTML input holding the view record.
	viewInput string
	viewType  string // Host Schema record
	presenter string // Go constructor
	children  bool   // the body renders { children... }
	// clock is set when the template, or a view template it calls, reads
	// time.Now(): its presenter takes now time.Time.
	clock bool
	// clockName is the presenter's clock parameter: now, or clockNow when
	// the template declares a now of its own.
	clockName string
	// context is set when the template, or a view template it calls, reads
	// templ's implicit ctx: its presenter takes ctx context.Context, since
	// request context is never an implicit KTML scope.
	context bool
	reasons []string
	// foreign is set for a component an earlier batch converted, called
	// through its templ bridge.
	foreign *foreignComponent
}

type sigParam struct {
	name string
	kind string // see typedParam
	t    types.Type
	obj  *types.Var
	// ktml is the KTML input name. A content input that would shadow an
	// HTML element (button, label) is renamed with a Content suffix.
	ktml string
}

// ktmlScalar maps a direct input's Go type to a KTML scalar type.
func ktmlScalar(t types.Type) (string, bool) {
	if _, named := types.Unalias(t).(*types.Named); named {
		return "", false
	}
	basic, ok := t.Underlying().(*types.Basic)
	if !ok {
		return "", false
	}
	switch {
	case basic.Info()&types.IsString != 0:
		return "string", true
	case basic.Info()&types.IsBoolean != 0:
		return "boolean", true
	case basic.Info()&types.IsInteger != 0:
		return "integer", true
	}
	return "", false
}

// presenterParams are the parameters the presenter constructor takes: the
// receiver and every parameter that is not content or an attribute bag.
func (s *tmplSig) presenterParams() []sigParam {
	var params []sigParam
	if s.receiver != nil {
		params = append(params, *s.receiver)
	}
	for _, p := range s.params {
		if p.kind != "component" && p.kind != "components" && p.kind != "attributes" {
			params = append(params, p)
		}
	}
	return params
}

// variadicPresenter reports whether the presenter's last parameter is the
// template's variadic one. The clock parameter then goes before it, since
// a variadic parameter must stay last.
func (s *tmplSig) variadicPresenter() bool {
	if !s.fn.Type().(*types.Signature).Variadic() || len(s.params) == 0 {
		return false
	}
	kind := s.params[len(s.params)-1].kind
	return kind != "component" && kind != "components" && kind != "attributes"
}

// signatures records the signature of every template in a file.
func (c *conversion) signatures(file *sourceFile) error {
	t := c.run.typed[file]
	for _, node := range file.parsed.Nodes {
		tmpl, ok := node.(*parser.HTMLTemplate)
		if !ok {
			continue
		}
		ident, ok := nodeAt[*ast.Ident](t, tmpl.Expression)
		if !ok {
			return fmt.Errorf("%s: template %q not found in the generated Go", file.rel, tmpl.Expression.Value)
		}
		fn, ok := t.info.Defs[ident].(*types.Func)
		if !ok {
			return fmt.Errorf("%s: template %q has no Go function", file.rel, ident.Name)
		}
		sig := &tmplSig{file: file, tmpl: tmpl, name: fn.Name(), fn: fn}
		for _, decl := range t.file.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name == ident {
				sig.decl = fd
			}
		}
		signature := fn.Type().(*types.Signature)
		key := fn.Pkg().Path() + "."
		if recv := signature.Recv(); recv != nil {
			sig.receiver = &sigParam{name: recv.Name(), kind: "receiver", t: recv.Type(), obj: recv}
			recvType := recv.Type()
			if pointer, ok := recvType.(*types.Pointer); ok {
				recvType = pointer.Elem()
			}
			if named, ok := types.Unalias(recvType).(*types.Named); ok {
				key += named.Obj().Name() + "."
			}
		}
		sig.key = key + fn.Name()
		for i := 0; i < signature.Params().Len(); i++ {
			v := signature.Params().At(i)
			pt := v.Type()
			if signature.Variadic() && i == signature.Params().Len()-1 {
				pt = types.NewSlice(pt.(*types.Slice).Elem())
			}
			p := typedParam(v.Name(), pt)
			ktmlName := v.Name()
			if (p.kind == "component" || p.kind == "components") && (htmlElements[ktmlName] || ktmlKeywords[ktmlName]) {
				ktmlName += "Content"
			}
			sig.params = append(sig.params, sigParam{name: v.Name(), kind: p.kind, t: v.Type(), obj: v, ktml: ktmlName})
		}
		sig.children = usesChildren(tmpl.Children)
		c.sigs = append(c.sigs, sig)
		c.sigFor[sig.key] = sig
	}
	return nil
}

func usesChildren(nodes []parser.Node) bool {
	for _, node := range nodes {
		switch n := node.(type) {
		case *parser.ChildrenExpression:
			return true
		case *parser.TemplElementExpression:
			// Children passed to a call are the caller's content.
			if usesChildren(n.Children) {
				return true
			}
		case parser.CompositeNode:
			if usesChildren(n.ChildNodes()) {
				return true
			}
		}
	}
	return false
}

// decideModes chooses, per template, between a direct component with scalar
// inputs and a view component fed by a presenter. A template is direct only
// when every value it renders is a literal, a constant, or one of its own
// scalar inputs, and every template it calls is direct too.
func (c *conversion) decideModes() {
	for _, sig := range c.sigs {
		sig.view = !c.directParams(sig)
	}
	for changed := true; changed; {
		changed = false
		for _, sig := range c.sigs {
			if sig.view {
				continue
			}
			d := &directCheck{c: c, sig: sig, w: c.run.walkers[sig.file]}
			if !d.nodes(sig.tmpl.Children) {
				sig.view = true
				sig.reasons = append(sig.reasons, d.reason)
				changed = true
			}
		}
	}
	c.decideClocks()
	c.componentNames()
	// gogen uppercases the first letter of a component's Go type, so an
	// unexported template whose name differs from another only in that
	// letter (messageBubble, MessageBubble) gets an Inner suffix.
	upper := map[string]map[string]int{}
	for _, sig := range c.sigs {
		dir := c.outDir(sig.file)
		if upper[dir] == nil {
			upper[dir] = map[string]int{}
		}
		upper[dir][upperFirst(sig.component)]++
	}
	for _, sig := range c.sigs {
		if upper[c.outDir(sig.file)][upperFirst(sig.component)] > 1 && !goExported(sig.component) {
			sig.component += "Inner"
		}
	}
	// View records share the output package with every component of its
	// directory; templates that differ only in case (Table and table) get
	// numbered view names.
	taken := map[string]map[string]bool{}
	for _, sig := range c.sigs {
		dir := c.outDir(sig.file)
		if taken[dir] == nil {
			taken[dir] = map[string]bool{}
		}
		taken[dir][sig.component] = true
	}
	if c.opts.InPlace {
		// In place, generated names share the package with its handwritten
		// Go.
		for dir := range taken {
			for name := range c.handwrittenNames(dir) {
				taken[dir][name] = true
			}
		}
	}
	for _, sig := range c.sigs {
		if !sig.view {
			continue
		}
		sig.viewInput = "view"
		for _, p := range sig.params {
			if p.name == "view" && (p.kind == "component" || p.kind == "components" || p.kind == "attributes") {
				sig.viewInput = "model"
			}
		}
		sig.viewType = upperFirst(sig.component) + "View"
		if sig.receiver != nil {
			sig.viewType = upperFirst(typeBaseName(sig.receiver.t)) + upperFirst(sig.component) + "View"
		}
		dir := c.outDir(sig.file)
		base := strings.TrimSuffix(sig.viewType, "View")
		for i := 2; taken[dir][sig.viewType]; i++ {
			sig.viewType = fmt.Sprintf("%sView%d", base, i)
		}
		taken[dir][sig.viewType] = true
	}
	// Presenters are New<View>, or Present<Component> where that names
	// another template's record or component.
	for _, sig := range c.sigs {
		if !sig.view {
			continue
		}
		sig.presenter = "New" + sig.viewType
		if taken[c.outDir(sig.file)][sig.presenter] {
			sig.presenter = "Present" + upperFirst(sig.component)
		}
		if !goExported(sig.name) {
			sig.presenter = strings.ToLower(sig.presenter[:1]) + sig.presenter[1:]
		}
	}
}

// handwrittenNames returns the package-level names an in-place output
// directory's Go package declares outside the converted templ files and
// the converter's own outputs.
func (c *conversion) handwrittenNames(dir string) map[string]bool {
	if names, ok := c.handwritten[dir]; ok {
		return names
	}
	names := map[string]bool{}
	// The Go declarations of the converted templ files move into their
	// presenters, so they stay in the package; only their templates go.
	templates := map[string]bool{}
	for _, sig := range c.sigs {
		if filepath.Dir(sig.file.abs) == dir && sig.receiver == nil {
			templates[sig.name] = true
		}
	}
	// The records an earlier run wrote for these templ files into the
	// package schema are this conversion's own: their generated Go does not
	// take the names.
	own := c.ownSchemaRecords(dir)
	for file, t := range c.run.typed {
		if filepath.Dir(file.abs) != dir {
			continue
		}
		scope := t.pkg.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			// Generated schema Go reports its .ktmlschema position (line
			// directives).
			if at := t.fset.Position(obj.Pos()).Filename; own[name] && (strings.HasSuffix(at, ".ktmlschema") || strings.HasSuffix(at, "_ktmlschema.go")) {
				continue
			}
			if !c.convertedFiles[t.fset.Position(obj.Pos()).Filename] || !templates[name] {
				names[name] = true
			}
		}
		break
	}
	c.handwritten[dir] = names
	return names
}

// ownSchemaRecords lists the records of dir's package schema that cite
// one of this conversion's templ files: an earlier run of the same batch
// wrote them.
func (c *conversion) ownSchemaRecords(dir string) map[string]bool {
	own := map[string]bool{}
	data, err := os.ReadFile(c.schemaDoc(dir))
	if err != nil {
		return own
	}
	converting := map[string]bool{}
	for _, file := range c.run.files {
		converting[file.rel] = true
	}
	for _, block := range strings.Split(string(data), "\n\n") {
		m := schemaDecl.FindStringSubmatch(block)
		source := schemaSource.FindStringSubmatch(block)
		if m != nil && source != nil && converting[source[1]] {
			own[m[1]] = true
		}
	}
	return own
}

// componentNames qualifies a template's KTML name by its package when its
// package also calls a same-named template of another package: KTML imports
// components by name and has no aliases.
func (c *conversion) componentNames() {
	for _, sig := range c.sigs {
		sig.component = sig.name
	}
	calledFrom := map[string]map[string]bool{} // package path -> called template keys
	for _, file := range c.run.files {
		if !c.convertible(file) {
			continue
		}
		t, w := c.run.typed[file], c.run.walkers[file]
		ast.Inspect(t.file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if callee := c.callee(w, call); callee != nil {
					if calledFrom[t.pkg.Path()] == nil {
						calledFrom[t.pkg.Path()] = map[string]bool{}
					}
					calledFrom[t.pkg.Path()][callee.key] = true
				}
			}
			return true
		})
	}
	for _, sig := range c.sigs {
		pkg := sig.fn.Pkg()
		for key := range calledFrom[pkg.Path()] {
			callee := c.sigFor[key]
			if callee == nil {
				callee = c.foreign[key]
			}
			if callee.fn.Pkg() != pkg && callee.component == sig.name {
				sig.component = upperFirst(pkg.Name()) + upperFirst(sig.name)
			}
		}
	}
}

// decideClocks marks the view templates whose presenter needs the clock:
// those that read time.Now() in their body and, transitively, those that
// call one.
func (c *conversion) decideClocks() {
	for _, sig := range c.sigs {
		sig.clockName = "now"
		ast.Inspect(sig.decl, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok && ident.Name == "now" && c.run.typed[sig.file].info.Defs[ident] != nil {
				sig.clockName = "clockNow"
			}
			return true
		})
	}
	for _, sig := range c.sigs {
		if !sig.view {
			continue
		}
		t := c.run.typed[sig.file]
		start, end := t.tok.Offset(sig.decl.Pos()), t.tok.Offset(sig.decl.End())
		for _, call := range newGoRewriter(c, sig.file, newGoImports("")).clockCalls {
			if call[0] >= start && call[1] <= end {
				sig.clock = true
			}
		}
	}
	for _, sig := range c.sigs {
		if sig.view {
			sig.context = c.readsImplicitContext(sig)
		}
	}
	for changed := true; changed; {
		changed = false
		for _, sig := range c.sigs {
			if !sig.view || sig.clock && sig.context {
				continue
			}
			w := c.run.walkers[sig.file]
			ast.Inspect(sig.decl, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if callee := c.callee(w, call); callee != nil && callee.view {
						if callee.clock && !sig.clock {
							sig.clock, changed = true, true
						}
						if callee.context && !sig.context {
							sig.context, changed = true, true
						}
					}
				}
				return true
			})
		}
	}
}

// readsImplicitContext reports whether a template's own expressions read
// the ctx templ's generated code declares.
func (c *conversion) readsImplicitContext(sig *tmplSig) bool {
	t := c.run.typed[sig.file]
	rw := newGoRewriter(c, sig.file, newGoImports(""))
	params := map[types.Object]bool{}
	for _, p := range sig.params {
		params[p.obj] = true
	}
	found := false
	for _, e := range templExpressions(sig.tmpl.Children) {
		start, end, ok := rw.span(e)
		if !ok {
			continue
		}
		for _, ident := range rw.idents {
			offset := t.tok.Offset(ident.Pos())
			if offset < start || offset >= end || ident.Name != "ctx" {
				continue
			}
			if v, ok := t.info.Uses[ident].(*types.Var); ok && !params[v] && isContext(v.Type()) {
				found = true
			}
		}
	}
	return found
}

// templExpressions lists the Go expressions of a template body.
func templExpressions(nodes []parser.Node) []parser.Expression {
	var list []parser.Expression
	var attributes func([]parser.Attribute)
	attributes = func(attrs []parser.Attribute) {
		for _, attr := range attrs {
			switch a := attr.(type) {
			case *parser.BoolExpressionAttribute:
				list = append(list, a.Expression)
			case *parser.ExpressionAttribute:
				list = append(list, a.Expression)
			case *parser.SpreadAttributes:
				list = append(list, a.Expression)
			case *parser.ConditionalAttribute:
				list = append(list, a.Expression)
				attributes(a.Then)
				attributes(a.Else)
			}
		}
	}
	var visit func([]parser.Node)
	visit = func(nodes []parser.Node) {
		for _, node := range nodes {
			switch n := node.(type) {
			case *parser.StringExpression:
				list = append(list, n.Expression)
			case *parser.GoCode:
				list = append(list, n.Expression)
			case *parser.Element:
				attributes(n.Attributes)
				visit(n.Children)
			case *parser.ScriptElement:
				attributes(n.Attributes)
				for _, content := range n.Contents {
					if content.GoCode != nil {
						list = append(list, content.GoCode.Expression)
					}
				}
			case *parser.RawElement:
				attributes(n.Attributes)
			case *parser.IfExpression:
				list = append(list, n.Expression)
				visit(n.Then)
				for _, elseIf := range n.ElseIfs {
					list = append(list, elseIf.Expression)
					visit(elseIf.Then)
				}
				visit(n.Else)
			case *parser.ForExpression:
				list = append(list, n.Expression)
				visit(n.Children)
			case *parser.SwitchExpression:
				list = append(list, n.Expression)
				for _, c := range n.Cases {
					list = append(list, c.Expression)
					visit(c.Children)
				}
			case *parser.CallTemplateExpression:
				list = append(list, n.Expression)
			case *parser.TemplElementExpression:
				list = append(list, n.Expression)
				visit(n.Children)
			}
		}
	}
	visit(nodes)
	return list
}

func typeBaseName(t types.Type) string {
	if pointer, ok := t.(*types.Pointer); ok {
		t = pointer.Elem()
	}
	if named, ok := types.Unalias(t).(*types.Named); ok {
		return named.Obj().Name()
	}
	return "Receiver"
}

func (c *conversion) directParams(sig *tmplSig) bool {
	if sig.receiver != nil {
		sig.reasons = append(sig.reasons, "method template")
		return false
	}
	for _, p := range sig.params {
		switch p.kind {
		case "component", "components", "attributes":
			continue
		case "scalar":
			if _, ok := ktmlScalar(p.t); ok {
				continue
			}
		}
		sig.reasons = append(sig.reasons, fmt.Sprintf("input %s (%s)", p.name, p.kind))
		return false
	}
	return true
}

// outDir returns the output directory of a templ file.
func (c *conversion) outDir(file *sourceFile) string {
	return filepath.Join(c.out, filepath.FromSlash(path.Dir(file.rel)))
}

func (c *conversion) outBase(file *sourceFile) string {
	return filepath.Join(c.outDir(file), strings.TrimSuffix(path.Base(file.rel), ".templ"))
}

// outPackage resolves the Go import path of an output directory from the
// nearest go.mod above it.
func (c *conversion) outPackage(dir string) (string, error) {
	root, modulePath, err := gopkg.FindModule(dir)
	if err != nil {
		return "", fmt.Errorf("output directory %s is not inside a Go module (presenters import the application's packages, so --out needs a go.mod above it): %w", dir, err)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return modulePath, nil
	}
	return path.Join(modulePath, filepath.ToSlash(rel)), nil
}

// write writes every emitted file.
func (c *conversion) write() error {
	files := make([]*fileEmitter, 0, len(c.files))
	for _, fe := range c.files {
		files = append(files, fe)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].file.rel < files[j].file.rel })
	helpers := map[string]*fileEmitter{}
	emit := func(output emittedFile, source string) error {
		if output.kind == "ktmlschema" {
			// A package converted in an earlier batch already has its
			// schema: the batch's records are added to it.
			if existing, ok := c.existing(output.abs); ok {
				merged, changed := mergeSchema(existing, output.content)
				if !changed {
					c.summary.Kept = append(c.summary.Kept, c.rel(output.abs))
					c.summary.Outputs = append(c.summary.Outputs, OutputFile{Path: c.rel(output.abs), Kind: output.kind, Source: source, Lines: strings.Count(string(existing), "\n")})
					return nil
				}
				output.content = merged
			}
		} else if c.keep(output.abs, output.kind) {
			// A kept output is validated as it stands, hand edits included.
			if existing, ok := c.existing(output.abs); ok {
				c.summary.Outputs = append(c.summary.Outputs, OutputFile{Path: c.rel(output.abs), Kind: output.kind, Source: source, Lines: strings.Count(string(existing), "\n")})
			}
			return c.checkKept(output)
		}
		if err := os.MkdirAll(filepath.Dir(output.abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(output.abs, output.content, 0o644); err != nil {
			return err
		}
		if err := c.recordPristine(output); err != nil {
			return err
		}
		lines := strings.Count(string(output.content), "\n")
		c.summary.Outputs = append(c.summary.Outputs, OutputFile{Path: c.rel(output.abs), Kind: output.kind, Source: source, Lines: lines})
		c.summary.Lines[output.kind] += lines
		return nil
	}
	// Documents first: they fix the output lines the source maps record.
	for _, output := range c.documents() {
		if err := emit(output, output.source); err != nil {
			return err
		}
	}
	for _, fe := range files {
		for _, output := range fe.outputs() {
			if err := emit(output, fe.file.rel); err != nil {
				return err
			}
		}
	}
	for _, output := range c.bridges() {
		if err := emit(output, ""); err != nil {
			return err
		}
	}
	for _, fe := range files {
		if len(fe.helpers) > 0 {
			helpers[fe.dir] = fe
		}
	}
	// One helper file per output package, holding the union of helpers its
	// presenters use.
	dirs := make([]string, 0, len(helpers))
	for dir := range helpers {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		used := map[string]bool{}
		for _, fe := range files {
			if fe.dir == dir {
				for name := range fe.helpers {
					used[name] = true
				}
			}
		}
		abs := filepath.Join(dir, "migrate_helpers.go")
		if existing, ok := c.existing(abs); ok {
			// The helpers of an earlier batch stay; the file is written
			// again only when this batch uses a helper it lacks.
			missing := false
			present := definedHelpers(existing)
			for name := range used {
				missing = missing || !present[name]
			}
			if !missing {
				c.summary.Kept = append(c.summary.Kept, c.rel(abs))
				continue
			}
			for name := range present {
				used[name] = true
			}
		}
		content := helperFile(helpers[dir].pkgName, used)
		if err := os.WriteFile(abs, content, 0o644); err != nil {
			return err
		}
		rel, _ := filepath.Rel(c.out, abs)
		lines := strings.Count(string(content), "\n")
		c.summary.Outputs = append(c.summary.Outputs, OutputFile{Path: filepath.ToSlash(rel), Kind: "helpers", Lines: lines})
		c.summary.Lines["helpers"] += lines
	}
	return nil
}

// validate checks every emitted .ktml and .ktmlschema with the KTML
// compiler, and generates their Go in memory, so a run that writes invalid
// KTML fails.
func (c *conversion) validate() {
	var docs []string
	for _, output := range c.summary.Outputs {
		if output.Kind == "ktml" || output.Kind == "ktmlschema" {
			docs = append(docs, filepath.Join(c.out, filepath.FromSlash(output.Path)))
		}
	}
	sort.Strings(docs)
	workspace := compiler.NewWorkspace(c.out)
	packages := func(dir string) (gogen.GoPackage, error) {
		resolved, err := gopkg.Resolve(dir)
		return gogen.GoPackage{ImportPath: resolved.ImportPath, Name: resolved.Name}, err
	}
	for _, doc := range docs {
		snapshot, err := workspace.Load(doc)
		if err != nil {
			c.summary.Validation = append(c.summary.Validation, fmt.Sprintf("%s: %v", c.rel(doc), err))
			continue
		}
		for _, diagnostic := range snapshot.Diagnostics(doc) {
			if diagnostic.Severity != compiler.SeverityError {
				continue
			}
			c.summary.Validation = append(c.summary.Validation, fmt.Sprintf("%s: [%s] %s", c.rel(doc), diagnostic.Code, diagnostic.Message))
		}
		if !snapshot.Valid() {
			continue
		}
		pkg, err := gopkg.Resolve(filepath.Dir(doc))
		if err != nil {
			c.summary.Validation = append(c.summary.Validation, fmt.Sprintf("%s: %v", c.rel(doc), err))
			continue
		}
		options := gogen.Options{Package: pkg.Name, Packages: packages}
		if strings.HasSuffix(doc, ".ktmlschema") {
			_, _, err = gogen.GenerateSchema(snapshot, doc, options)
		} else {
			_, err = gogen.GenerateFile(snapshot, doc, options)
		}
		if err != nil {
			c.summary.Validation = append(c.summary.Validation, fmt.Sprintf("%s: generate: %v", c.rel(doc), err))
		}
	}
}

func (c *conversion) rel(abs string) string {
	rel, err := filepath.Rel(c.out, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

// finish copies the per-file results into the report.
func (c *conversion) finish() {
	c.report.Nodes = c.report.Nodes[:0]
	for _, file := range c.run.files {
		if w := c.run.walkers[file]; w != nil {
			c.report.Nodes = append(c.report.Nodes, w.nodes...)
		}
	}
	for _, sig := range c.sigs {
		fe := c.files[sig.file]
		if fe == nil {
			continue
		}
		if out, ok := fe.templates[sig]; ok {
			c.summary.Templates = append(c.summary.Templates, out)
			c.summary.Fields.merge(out.Fields)
			m := c.callMapping(sig)
			m.Bridge = c.bridged[sig]
			c.summary.Calls = append(c.summary.Calls, m)
		}
	}
	for _, n := range c.report.Nodes {
		if n.Partial {
			c.summary.Partial++
		}
	}
	sort.SliceStable(c.summary.Diagnostics, func(i, j int) bool {
		a, b := c.summary.Diagnostics[i], c.summary.Diagnostics[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.From.Line != b.From.Line {
			return a.From.Line < b.From.Line
		}
		return a.From.Col < b.From.Col
	})
}

// Failed reports whether a conversion must exit nonzero: a node was
// dropped, partially converted, or blocked, a file failed to parse, or the
// emitted KTML failed validation.
func (c *Conversion) Failed() bool {
	return c.Partial > 0 || len(c.Validation) > 0
}

// writeSourceMap renders a file's source map as JSON.
func writeSourceMap(m *SourceMap) []byte {
	data, _ := json.MarshalIndent(m, "", "  ")
	return append(data, '\n')
}
