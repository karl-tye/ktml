package templmigrate

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"

	parser "github.com/a-h/templ/parser/v2"
	"golang.org/x/tools/imports"
)

// fieldKind classifies a presenter-built field for the measurement.
type fieldKind int

const (
	fieldPassThrough fieldKind = iota
	fieldDerived
	fieldWiring
)

func (k fieldKind) String() string {
	switch k {
	case fieldPassThrough:
		return "pass-through"
	case fieldDerived:
		return "derived"
	}
	return "wiring"
}

// record is one Host Schema record the converter declares.
type record struct {
	name    string
	comment string
	fields  []*field
	used    map[string]bool
	byKey   map[string]*field
	// variants makes the record a Presentation State with nullary
	// Variants.
	variants []string
}

func newRecord(name, comment string) *record {
	return &record{name: name, comment: comment, used: map[string]bool{}, byKey: map[string]*field{}}
}

// field is one record field and how the presenter fills it.
type field struct {
	name     string
	ktmlType string
	kind     fieldKind
	src      Position
}

func (f *field) goName() string { return upperFirst(f.name) }

// fileEmitter emits one templ file's .ktml, .ktmlschema, presenter, and
// source map.
type fileEmitter struct {
	c       *conversion
	file    *sourceFile
	w       *walker
	t       *typedFile
	dir     string // output directory
	base    string // output path without extension
	pkgName string // Go package of the output directory
	pkgPath string
	imports *goImports
	rw      *goRewriter
	helpers map[string]bool
	records []*record
	recUsed map[string]bool
	ktml    *ktmlPrinter
	present strings.Builder
	moved   strings.Builder
	// componentDeps maps each converted file this file calls to the
	// component names it calls; documents resolve them to import paths.
	componentDeps map[*sourceFile]map[string]bool
	// foreignDeps maps the documents of components earlier batches
	// converted to the names this file calls.
	foreignDeps map[string]map[string]bool
	// schemaImports maps the schema document of another output package to
	// the records this file's records use from it.
	schemaImports map[string]map[string]bool
	templates     map[*tmplSig]TemplateOutput
	srcmap        *SourceMap
}

func newFileEmitter(c *conversion, file *sourceFile) (*fileEmitter, error) {
	t := c.run.typed[file]
	fe := &fileEmitter{
		c: c, file: file, w: c.run.walkers[file], t: t,
		dir: c.outDir(file), base: c.outBase(file), pkgName: t.pkg.Name(),
		helpers: map[string]bool{}, recUsed: map[string]bool{},
		componentDeps: map[*sourceFile]map[string]bool{}, foreignDeps: map[string]map[string]bool{}, schemaImports: map[string]map[string]bool{},
		templates: map[*tmplSig]TemplateOutput{},
	}
	pkgPath, err := c.outPackage(fe.dir)
	if err != nil {
		return nil, err
	}
	fe.pkgPath = pkgPath
	fe.imports = newGoImports(fe.pkgName)
	fe.rw = newGoRewriter(c, file, fe.imports)
	fe.ktml = newKTMLPrinter()
	fe.srcmap = &SourceMap{Source: file.rel}
	if c.opts.InPlace {
		for name := range c.handwrittenNames(fe.dir) {
			fe.recUsed[name] = true
		}
	}
	// Records share the Go package with every other file of the directory.
	for _, sig := range c.sigs {
		if c.outDir(sig.file) == fe.dir {
			fe.recUsed[sig.component] = true
			fe.recUsed[sig.component+"Props"] = true
			fe.recUsed[sig.viewType] = sig.view
		}
	}
	return fe, nil
}

// recordName reserves a unique record name in the output package.
func (fe *fileEmitter) recordName(name string) string {
	for _, other := range fe.c.files {
		if other != fe && other.dir == fe.dir {
			for _, r := range other.records {
				fe.recUsed[r.name] = true
			}
		}
	}
	candidate := name
	for i := 2; fe.recUsed[candidate]; i++ {
		candidate = fmt.Sprintf("%s%d", name, i)
	}
	fe.recUsed[candidate] = true
	return candidate
}

func (fe *fileEmitter) importComponent(sig *tmplSig) {
	if sig.foreign != nil {
		if fe.foreignDeps[sig.foreign.doc] == nil {
			fe.foreignDeps[sig.foreign.doc] = map[string]bool{}
		}
		fe.foreignDeps[sig.foreign.doc][sig.component] = true
		return
	}
	if sig.file == fe.file {
		return
	}
	if fe.componentDeps[sig.file] == nil {
		fe.componentDeps[sig.file] = map[string]bool{}
	}
	fe.componentDeps[sig.file][sig.component] = true
}

// importRecord makes a record of another output package visible to this
// file's records. Records of one output package share its schema document.
func (fe *fileEmitter) importRecord(sig *tmplSig, name string) {
	doc := ""
	switch {
	case sig.foreign != nil:
		doc = sig.foreign.schema
	case fe.c.outDir(sig.file) == fe.dir:
		return
	default:
		doc = fe.c.schemaDoc(fe.c.outDir(sig.file))
	}
	if doc == fe.c.schemaDoc(fe.dir) {
		return
	}
	if fe.schemaImports[doc] == nil {
		fe.schemaImports[doc] = map[string]bool{}
	}
	fe.schemaImports[doc][name] = true
}

// presenterPackage returns how this file's presenters spell a call of
// sig's presenter.
func (fe *fileEmitter) presenterRef(sig *tmplSig) string {
	if sig.foreign != nil {
		if sig.foreign.pkgPath == fe.pkgPath {
			return sig.presenter
		}
		return fe.imports.add(sig.foreign.pkgPath, sig.foreign.pkgName) + "." + sig.presenter
	}
	if fe.c.outDir(sig.file) == fe.dir {
		return sig.presenter
	}
	other := fe.c.files[sig.file]
	return fe.imports.add(other.pkgPath, other.pkgName) + "." + sig.presenter
}

// diagnose records a source-ranged diagnostic and attaches its message to
// the node.
func (fe *fileEmitter) diagnose(n *Node, r parser.Range, severity, code, message string) {
	d := Diagnostic{File: fe.file.rel, From: sourcePosition(r.From), To: sourcePosition(r.To), Severity: severity, Code: code, Message: message}
	if n != nil {
		d.From, d.To = n.From, n.To
		n.Notes = append(n.Notes, severity+": "+message)
		if severity == "partial" {
			n.Partial = true
		}
	}
	fe.c.summary.Diagnostics = append(fe.c.summary.Diagnostics, d)
}

func (fe *fileEmitter) security(n *Node, r parser.Range, boundary, output string) {
	at := sourcePosition(r.From)
	if n != nil {
		at = n.From
	}
	fe.c.summary.Security = append(fe.c.summary.Security, SecurityMarker{File: fe.file.rel, At: at, Boundary: boundary, Output: output})
}

func (fe *fileEmitter) emit() {
	for _, node := range fe.file.parsed.Nodes {
		switch n := node.(type) {
		case *parser.HTMLTemplate:
			sig := fe.sigOf(n)
			te := newTemplateEmitter(fe, sig)
			te.emit()
		case *parser.TemplateFileGoExpression:
			fe.moveGo(n)
		}
	}
	for _, header := range fe.file.parsed.Header {
		if n := fe.c.node(fe.file, "TemplateFileGoExpression", header.Expression.Range); n != nil {
			n.Output = &OutputSpan{File: fe.c.rel(fe.base + "_present.go")}
		}
	}
}

func (fe *fileEmitter) sigOf(tmpl *parser.HTMLTemplate) *tmplSig {
	for _, sig := range fe.c.sigs {
		if sig.tmpl == tmpl {
			return sig
		}
	}
	return nil
}

// moveGo copies a templ file's Go declarations into its presenter file.
// Imports become the presenter's imports as references need them; types
// stay in the source package, which presenters import; methods cannot
// move and are reported.
func (fe *fileEmitter) moveGo(n *parser.TemplateFileGoExpression) {
	node := fe.c.node(fe.file, "TemplateFileGoExpression", n.Expression.Range)
	start, end, ok := fe.rw.span(n.Expression)
	if !ok {
		fe.diagnose(node, n.Expression.Range, "partial", "go-not-found", "Go declarations not found in the generated Go")
		return
	}
	for _, decl := range fe.t.file.Decls {
		declStart := fe.t.tok.Offset(decl.Pos())
		if declStart < start || declStart >= end {
			continue
		}
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok.String() == "import" {
				continue
			}
			if d.Tok.String() == "type" && exportedTypes(d) && !fe.c.opts.InPlace {
				continue
			}
		case *ast.FuncDecl:
			if d.Recv != nil && !fe.c.opts.InPlace {
				fe.diagnose(node, n.Expression.Range, "review", "method-stays", fmt.Sprintf("method %s stays in the source package", d.Name.Name))
				continue
			}
		}
		from := decl.Pos()
		if doc := declDoc(decl); doc != nil {
			from = doc.Pos()
		}
		fe.rw.clock = false
		text, problems := fe.rw.rewrite(fe.t.tok.Offset(from), fe.t.tok.Offset(decl.End()))
		for _, problem := range problems {
			fe.diagnose(node, n.Expression.Range, "partial", "presenter-reference", problem)
		}
		if len(problems) > 0 {
			// Keep the scaffold compiling: the declaration stays as a TODO.
			text = "// TODO(ktml-migrate): " + strings.Join(problems, "; ") + "\n// " + strings.ReplaceAll(text, "\n", "\n// ")
		}
		fe.moved.WriteString("\n" + text + "\n")
	}
	if node != nil {
		node.Output = &OutputSpan{File: fe.c.rel(fe.base + "_present.go")}
	}
}

// exportedTypes reports a type declaration whose types are all exported:
// those stay in the source package.
func exportedTypes(d *ast.GenDecl) bool {
	for _, spec := range d.Specs {
		if spec, ok := spec.(*ast.TypeSpec); ok && !spec.Name.IsExported() {
			return false
		}
	}
	return true
}

func declDoc(decl ast.Decl) *ast.CommentGroup {
	switch d := decl.(type) {
	case *ast.GenDecl:
		return d.Doc
	case *ast.FuncDecl:
		return d.Doc
	}
	return nil
}

type emittedFile struct {
	abs     string
	kind    string
	content []byte
	source  string // the templ files a shared document comes from
}

// outputs renders the file's presenter and source map. Its KTML and Host
// Schema are part of the documents the conversion assembles first.
func (fe *fileEmitter) outputs() []emittedFile {
	var outputs []emittedFile
	presentPath := fe.base + "_present.go"
	if fe.present.Len() > 0 || fe.moved.Len() > 0 {
		var g strings.Builder
		fmt.Fprintf(&g, "// Presenters converted from %s by ktml migrate templ. They map the\n", fe.file.rel)
		g.WriteString("// templ parameters into the Host Schema records the KTML components render.\n")
		g.WriteString("// Edit freely: the converter writes them once.\n\n")
		fmt.Fprintf(&g, "package %s\n\n", fe.pkgName)
		if imports := fe.imports.block(); imports != "" {
			g.WriteString(imports + "\n")
		}
		g.WriteString(strings.TrimLeft(fe.present.String(), "\n"))
		if fe.moved.Len() > 0 {
			g.WriteString("\n// Go declarations moved from the templ file.\n")
			g.WriteString(fe.moved.String())
		}
		content := []byte(g.String())
		// Drop imports only commented-out code used, and format.
		if formatted, err := imports.Process(presentPath, content, &imports.Options{Comments: true, TabIndent: true, TabWidth: 8}); err == nil {
			content = formatted
		} else {
			fe.c.summary.Validation = append(fe.c.summary.Validation, fmt.Sprintf("%s: presenter does not parse: %v", fe.c.rel(presentPath), err))
		}
		outputs = append(outputs, emittedFile{abs: presentPath, kind: "presenter", content: content})
		fe.srcmap.Presenter = fe.c.rel(presentPath)
	}
	fe.srcmap.Nodes = nil
	for _, n := range fe.w.nodes {
		if n.File == fe.file.rel {
			fe.srcmap.Nodes = append(fe.srcmap.Nodes, SourceMapEntry{Template: n.Template, Kind: n.Kind, Status: n.Status, Partial: n.Partial, From: n.From, To: n.To, Output: n.Output})
		}
	}
	outputs = append(outputs, emittedFile{abs: fe.c.sourceMapPath(fe), kind: "sourcemap", content: writeSourceMap(fe.srcmap)})
	return outputs
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedSet(m map[string]bool) []string { return sortedMapKeys(m) }

// SourceMap relates one converted templ file's ranges to the emitted KTML
// lines, and to the Host Schema fields the presenters fill.
type SourceMap struct {
	Source    string           `json:"source"`
	KTML      string           `json:"ktml"`
	Schema    string           `json:"schema,omitempty"`
	Presenter string           `json:"presenter,omitempty"`
	Nodes     []SourceMapEntry `json:"nodes"`
	Fields    []FieldMapping   `json:"fields"`
}

// SourceMapEntry maps one templ node to its output lines.
type SourceMapEntry struct {
	Template string      `json:"template,omitempty"`
	Kind     string      `json:"kind"`
	Status   Status      `json:"status"`
	Partial  bool        `json:"partial,omitempty"`
	From     Position    `json:"from"`
	To       Position    `json:"to"`
	Output   *OutputSpan `json:"output,omitempty"`
}

// FieldMapping maps a templ expression to the record field that carries it.
type FieldMapping struct {
	Template string   `json:"template"`
	From     Position `json:"from"`
	To       Position `json:"to"`
	Record   string   `json:"record"`
	Field    string   `json:"field"`
	Kind     string   `json:"kind"`
}

// goStringExpr converts a Go expression of type t to a string the way templ
// renders it: strings unchanged, other values through fmt.Sprint.
func (fe *fileEmitter) goStringExpr(expr string, t types.Type) string {
	if t != nil {
		if basic, ok := t.Underlying().(*types.Basic); ok && basic.Info()&types.IsString != 0 {
			if _, named := types.Unalias(t).(*types.Named); named {
				return "string(" + expr + ")"
			}
			if basic.Kind() == types.UntypedString || basic.Kind() == types.String {
				return expr
			}
			return "string(" + expr + ")"
		}
	}
	return fe.imports.add("fmt", "fmt") + ".Sprint(" + expr + ")"
}
