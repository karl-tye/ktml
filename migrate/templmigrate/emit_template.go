package templmigrate

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"

	parser "github.com/a-h/templ/parser/v2"
)

// recScope is a record scope of a view template: the view itself, a loop
// item, or a Variant payload. Fields of expressions that depend on the
// scope's Go locals live in its record.
type recScope struct {
	rec    *record
	goVar  string // the presenter variable holding the record
	root   string // the KTML path root
	parent *recScope
	depth  int
}

// tmplEmitter emits one templ declaration.
type tmplEmitter struct {
	fe   *fileEmitter
	c    *conversion
	sig  *tmplSig
	w    *walker
	decl *Node

	root   *recScope
	scope  *recScope
	locals map[types.Object]*recScope
	goVars map[string]bool
	ktml   map[string]bool // KTML names in use (inputs, items)

	// groups are the clause groups the template's switches added.
	groups []*clauseGroup

	body   strings.Builder
	indent int
	blocks []map[string]bool
	out    TemplateOutput
}

func newTemplateEmitter(fe *fileEmitter, sig *tmplSig) *tmplEmitter {
	te := &tmplEmitter{
		fe: fe, c: fe.c, sig: sig, w: fe.w,
		locals: map[types.Object]*recScope{}, goVars: map[string]bool{}, ktml: map[string]bool{},
		indent: 1, blocks: []map[string]bool{{}},
	}
	te.decl = fe.c.node(fe.file, "HTMLTemplate", sig.tmpl.Range)
	te.out = TemplateOutput{File: fe.file.rel, Template: sig.name, Component: sig.component, Mode: "direct"}
	return te
}

func (te *tmplEmitter) view() bool { return te.sig.view }

func (te *tmplEmitter) emit() {
	sig := te.sig
	te.fe.rw.clock, te.fe.rw.clockName = sig.view, sig.clockName
	line := int(sig.tmpl.Range.From.Line) + 1
	if te.c.opts.InPlace && te.c.handwrittenNames(te.fe.dir)[upperFirst(sig.component)] {
		te.fe.diagnose(te.decl, sig.tmpl.Range, "partial", "generated-name-collision",
			fmt.Sprintf("the component's Go type %s collides with a handwritten declaration of package %s; rename one", upperFirst(sig.component), te.fe.pkgName))
	}
	if sig.view {
		if !te.c.opts.ScaffoldPresenters {
			te.fe.diagnose(te.decl, sig.tmpl.Range, "partial", "presenter-required", fmt.Sprintf("%s needs a presenter (%s); rerun with --scaffold-presenters", sig.name, strings.Join(sig.reasons, ", ")))
			return
		}
		te.out.Mode, te.out.View, te.out.Presenter = "view", sig.viewType, sig.presenter
		rec := newRecord(sig.viewType, fmt.Sprintf("%s (%s:%d).", sig.name, te.fe.file.rel, line))
		te.fe.records = append(te.fe.records, rec)
		for _, p := range sig.presenterParams() {
			te.goVars[p.name] = true
		}
		// The view variable is v unless a parameter already has that name.
		te.root = &recScope{rec: rec, goVar: te.goVar("v"), root: sig.viewInput}
		te.scope = te.root
		te.ktml[sig.viewInput] = true
		for _, p := range sig.presenterParams() {
			te.locals[p.obj] = te.root
		}
	} else {
		te.goVars["v"] = true
	}
	for _, p := range sig.params {
		te.ktml[p.name] = true
	}
	for _, p := range sig.params {
		if p.kind == "component" || p.kind == "components" || p.kind == "attributes" {
			te.locals[p.obj] = nil
		}
	}
	body := te.children(sig.tmpl.Children, nil, stripAll)

	p := te.fe.ktml
	from := p.line
	p.writeLine(0, fmt.Sprintf("// %s (%s:%d).", sig.name, te.fe.file.rel, line))
	header := sig.component
	if inputs := te.inputs(); inputs != "" {
		header += " [" + inputs + "]"
	}
	p.writeLine(0, header+" {")
	p.nodes(1, body)
	p.writeLine(0, "}")
	p.writeLine(0, "")
	// Lines are relative to this file's component bodies until the
	// document is assembled (bodyOutput).
	if te.decl != nil {
		te.decl.Output = &OutputSpan{File: bodyOutput, From: from, To: p.line - 2}
	}
	te.printGroups(p)
	for n, span := range p.spans {
		if n.Output == nil {
			n.Output = &OutputSpan{File: bodyOutput, From: span[0], To: span[1]}
		}
	}
	p.spans = map[*Node][2]int{}
	// Nodes that print nothing of their own (stripped whitespace, local Go,
	// switch cases) map to the presenter or to their component.
	for i := range te.w.nodes {
		n := &te.w.nodes[i]
		if n.Template != sig.name || n.Output != nil || n.File != te.fe.file.rel {
			continue
		}
		switch n.Kind {
		case "GoCode", "CaseExpression", "IfExpression", "ElseIfExpression", "ForExpression", "SwitchExpression":
			// Blocks left with only local Go are presenter code.
			if sig.view {
				n.Output = &OutputSpan{File: te.c.rel(te.fe.base + "_present.go")}
				continue
			}
		}
		if te.decl != nil && te.decl.Output != nil {
			span := *te.decl.Output
			n.Output = &span
		}
	}

	if sig.view {
		te.presenter()
	}
	for _, r := range te.records() {
		for _, f := range r.fields {
			te.out.Fields.add(f.kind)
		}
	}
	for _, n := range te.w.nodes {
		if n.Template == sig.name && n.Partial {
			te.out.Partial++
		}
	}
	te.fe.templates[sig] = te.out
}

// records lists the view record and the records nested under it.
func (te *tmplEmitter) records() []*record {
	if te.root == nil {
		return nil
	}
	var records []*record
	seen := false
	for _, r := range te.fe.records {
		if r == te.root.rec {
			seen = true
		}
		if seen {
			records = append(records, r)
		}
	}
	return records
}

// inputs spells the component's input list.
func (te *tmplEmitter) inputs() string {
	var inputs []string
	if te.sig.view {
		inputs = append(inputs, te.sig.viewInput+": "+te.sig.viewType)
	}
	for _, p := range te.sig.params {
		switch p.kind {
		case "component", "components":
			inputs = append(inputs, p.ktml+"?: content")
		case "attributes":
			inputs = append(inputs, p.name+": Attributes")
		default:
			if !te.sig.view {
				kind, _ := ktmlScalar(p.t)
				inputs = append(inputs, p.name+": "+kind)
			}
		}
	}
	if te.sig.children {
		inputs = append(inputs, "children?: content")
	}
	return strings.Join(inputs, ", ")
}

// presenter writes the presenter constructor.
func (te *tmplEmitter) presenter() {
	sig := te.sig
	var params []string
	unreachable := ""
	for _, p := range sig.presenterParams() {
		if name := te.c.unreachableType(p.t); name != "" {
			unreachable = name
			te.fe.diagnose(te.decl, sig.tmpl.Range, "partial", "presenter-reference", fmt.Sprintf("parameter %s has the unexported type %s, which a presenter in another package cannot name", p.name, name))
			params = append(params, p.name+" any")
			continue
		}
		typeText := te.fe.rw.typeText(p.t)
		if p.obj != nil && sig.fn.Type().(*types.Signature).Variadic() && p.obj == sig.fn.Type().(*types.Signature).Params().At(sig.fn.Type().(*types.Signature).Params().Len()-1) {
			typeText = "..." + strings.TrimPrefix(typeText, "[]")
		}
		params = append(params, p.name+" "+typeText)
	}
	if sig.context {
		params = append([]string{"ctx " + te.fe.imports.add("context", "context") + ".Context"}, params...)
	}
	if sig.clock {
		clock := sig.clockName + " " + te.fe.imports.add("time", "time") + ".Time"
		if sig.variadicPresenter() {
			params = slices.Insert(params, len(params)-1, clock)
		} else {
			params = append(params, clock)
		}
	}
	g := &te.fe.present
	fmt.Fprintf(g, "\n// %s presents %s (%s:%d).\n", sig.presenter, sig.name, te.fe.file.rel, int(sig.tmpl.Range.From.Line)+1)
	fmt.Fprintf(g, "func %s(%s) %s {\n", sig.presenter, strings.Join(params, ", "), sig.viewType)
	fmt.Fprintf(g, "\tvar %s %s\n", te.root.goVar, sig.viewType)
	body := foldEmptyBranches(removeEmptyElses(resolveLocals(te.body.String())))
	if unreachable != "" {
		// The body cannot compile without the parameter's type.
		fmt.Fprintf(g, "\t// TODO(ktml-migrate): the presenter needs the unexported type %s.\n", unreachable)
		for _, p := range sig.presenterParams() {
			fmt.Fprintf(g, "\t_ = %s\n", p.name)
		}
		for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			g.WriteString("\t// " + strings.TrimPrefix(line, "\t") + "\n")
		}
	} else {
		g.WriteString(body)
	}
	fmt.Fprintf(g, "\treturn %s\n}\n", te.root.goVar)
}

// --- presenter statements ---

func (te *tmplEmitter) line(text string) {
	te.body.WriteString(strings.Repeat("\t", te.indent) + text + "\n")
}

func (te *tmplEmitter) open(text string) {
	te.line(text)
	te.indent++
	te.blocks = append(te.blocks, map[string]bool{})
}

func (te *tmplEmitter) close(text string) {
	te.indent--
	te.blocks = te.blocks[:len(te.blocks)-1]
	te.line(text)
}

func (te *tmplEmitter) assign(line string) {
	for _, block := range te.blocks {
		if block[line] {
			return
		}
	}
	te.blocks[len(te.blocks)-1][line] = true
	if strings.Contains(line, unresolvedMarker) {
		commented := strings.ReplaceAll(strings.ReplaceAll(line, unresolvedMarker, ""), "\n", "\n// ")
		te.line("// TODO(ktml-migrate): " + commented)
		return
	}
	te.line(line)
}

// goVar reserves a presenter variable name.
func (te *tmplEmitter) goVar(name string) string {
	candidate := name
	for i := 2; te.goVars[candidate] || goKeyword(candidate); i++ {
		candidate = name + strconv.Itoa(i)
	}
	te.goVars[candidate] = true
	return candidate
}

// declare records the Go locals a generated range defines in scope.
func (te *tmplEmitter) declare(start, end int, scope *recScope) {
	for _, ident := range te.fe.rw.idents {
		offset := te.fe.t.tok.Offset(ident.Pos())
		if offset < start || offset >= end {
			continue
		}
		if obj := te.fe.t.info.Defs[ident]; obj != nil {
			te.locals[obj] = scope
			te.goVars[obj.Name()] = true
		}
	}
}

// scopeOf returns the deepest record scope whose locals the generated
// range uses.
func (te *tmplEmitter) scopeOf(start, end int) *recScope {
	deepest := te.root
	for _, ident := range te.fe.rw.idents {
		offset := te.fe.t.tok.Offset(ident.Pos())
		if offset < start || offset >= end {
			continue
		}
		if scope, ok := te.locals[te.fe.t.info.Uses[ident]]; ok && scope != nil && scope.depth > deepest.depth {
			deepest = scope
		}
	}
	return deepest
}

// fieldValue declares (or reuses) a field for a presenter-built value in
// the scope its Go expression depends on, assigns it at the current
// presenter position, and returns its KTML path.
func (te *tmplEmitter) fieldValue(start, end int, ktmlType, goExpr string, kind fieldKind, hint []string, src *Node) string {
	scope := te.scopeOf(start, end)
	return te.fieldIn(scope, ktmlType, goExpr, te.localIdentity(start, end), kind, hint, src)
}

// localIdentity names the declarations of the presenter locals a generated
// range uses, so that the same text over different locals (if err, ok :=
// … in two branches) does not share a field.
func (te *tmplEmitter) localIdentity(start, end int) string {
	var b strings.Builder
	for _, ident := range te.fe.rw.idents {
		offset := te.fe.t.tok.Offset(ident.Pos())
		if offset < start || offset >= end {
			continue
		}
		obj := te.fe.t.info.Uses[ident]
		if _, ok := te.locals[obj]; ok && obj != nil {
			fmt.Fprintf(&b, "@%d", obj.Pos())
		}
	}
	return b.String()
}

func (te *tmplEmitter) fieldIn(scope *recScope, ktmlType, goExpr, identity string, kind fieldKind, hint []string, src *Node) string {
	key := ktmlType + "|" + goExpr + "|" + identity
	f, ok := scope.rec.byKey[key]
	if !ok {
		f = &field{name: uniqueName(scope.rec.used, lowerCamel(hint)), ktmlType: ktmlType, kind: kind}
		if src != nil {
			f.src = src.From
		}
		scope.rec.fields = append(scope.rec.fields, f)
		scope.rec.byKey[key] = f
	}
	te.assign(scope.goVar + "." + f.goName() + " = " + goExpr)
	if src != nil {
		te.fe.srcmap.Fields = append(te.fe.srcmap.Fields, FieldMapping{Template: te.sig.name, From: src.From, To: src.To, Record: scope.rec.name, Field: f.name, Kind: f.kind.String()})
	}
	return scope.root + "." + f.name
}

// span returns the generated range of an AST node.
func (te *tmplEmitter) span(n ast.Node) (int, int) {
	return te.fe.t.tok.Offset(n.Pos()), te.fe.t.tok.Offset(n.End())
}

// goText rewrites an AST node of the generated Go for the presenter,
// reporting problems on src.
func (te *tmplEmitter) goText(n ast.Node, src *Node, r parser.Range) string {
	text, problems := te.fe.rw.node(n)
	problems = append(problems, te.contentReferences(n)...)
	for _, problem := range problems {
		te.fe.diagnose(src, r, "partial", "presenter-reference", problem)
	}
	if len(problems) > 0 {
		// assign comments out a statement that carries this marker, so the
		// scaffold still compiles; the node is reported partial.
		text += unresolvedMarker
	}
	return text
}

// contentReferences reports each content input of the template that n
// reads: KTML renders content through the component's slots, so the
// presenter does not receive it (a class list's len(actions) > 0, say).
func (te *tmplEmitter) contentReferences(n ast.Node) []string {
	content := map[types.Object]string{}
	for _, p := range te.sig.params {
		if p.obj != nil && (p.kind == "component" || p.kind == "components") {
			content[p.obj] = p.name
		}
	}
	if len(content) == 0 {
		return nil
	}
	var problems []string
	seen := map[string]bool{}
	ast.Inspect(n, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		if name, ok := content[te.fe.t.info.Uses[ident]]; ok && !seen[name] {
			seen[name] = true
			problems = append(problems, fmt.Sprintf("%s is a content input: the presenter does not receive it, so a value that depends on it is computed by hand (a presenter parameter, or a condition on %s? in the document)", name, name))
		}
		return true
	})
	return problems
}

// unresolvedMarker tags Go the presenter package cannot compile.
const unresolvedMarker = "/* ktml-migrate: unresolved */"

func (te *tmplEmitter) partial(src *Node, r parser.Range, code, message string) {
	te.fe.diagnose(src, r, "partial", code, message)
}

func (te *tmplEmitter) review(src *Node, r parser.Range, code, message string) {
	te.fe.diagnose(src, r, "review", code, message)
}

func (te *tmplEmitter) typeOf(expr ast.Expr) types.Type {
	return te.fe.t.info.TypeOf(expr)
}

// constant returns the value of a literal or Go constant expression.
func (te *tmplEmitter) constant(expr ast.Expr) (types.TypeAndValue, bool) {
	tv, ok := te.fe.t.info.Types[unparen(expr)]
	return tv, ok && tv.Value != nil
}

// literalText returns the text a constant renders as, the way templ's
// fmt.Sprint renders it.
func (te *tmplEmitter) literalText(expr ast.Expr) (string, bool) {
	tv, ok := te.constant(expr)
	if !ok {
		return "", false
	}
	switch tv.Value.Kind() {
	case 1: // constant.Bool
		return tv.Value.ExactString(), true
	case 2: // constant.String
		s, err := strconv.Unquote(tv.Value.ExactString())
		return s, err == nil
	case 3: // constant.Int
		return tv.Value.ExactString(), true
	}
	return "", false
}

// paramPath returns the KTML path of a direct template's parameter.
func (te *tmplEmitter) paramPath(expr ast.Expr) (sigParam, bool) {
	d := &directCheck{c: te.c, sig: te.sig, w: te.w}
	return d.paramKind(expr)
}

// --- body ---

const (
	stripAll = iota
	stripEnds
)

// children converts a node list the way templ's generator writes it:
// whitespace nodes are dropped (stripAll) or trimmed at both ends
// (stripEnds) and written as one space in between, and a node's trailing
// space is written when it and the next node are inline or text.
func (te *tmplEmitter) children(nodes []parser.Node, next parser.Node, mode int) []kNode {
	if mode == stripAll {
		var kept []parser.Node
		for _, n := range nodes {
			if _, ok := n.(*parser.Whitespace); !ok {
				kept = append(kept, n)
			}
		}
		nodes = kept
	} else {
		nodes = trimWhitespace(nodes)
	}
	var out []kNode
	var text *kText
	add := func(s segment, src *Node) {
		if text == nil {
			text = &kText{}
		}
		last := len(text.segments) - 1
		if s.path == "" && last >= 0 && text.segments[last].path == "" {
			text.segments[last].literal += s.literal
		} else {
			text.segments = append(text.segments, s)
		}
		if src != nil {
			text.src = append(text.src, src)
		}
	}
	flush := func() {
		if text != nil {
			out = append(out, text)
			text = nil
		}
	}
	for i, node := range nodes {
		nextNode := next
		if i+1 < len(nodes) {
			nextNode = nodes[i+1]
		}
		switch n := node.(type) {
		case *parser.Text:
			add(segment{literal: html.UnescapeString(n.Value)}, te.c.node(te.fe.file, "Text", n.Range))
		case *parser.Whitespace:
			if n.Value != "" {
				add(segment{literal: " "}, te.c.node(te.fe.file, "Whitespace", n.Range))
			}
		case *parser.StringExpression:
			src := te.c.node(te.fe.file, "StringExpression", n.Range)
			for i, seg := range te.textSegments(n.Expression, src) {
				if i == 0 {
					add(seg, src)
				} else {
					add(seg, nil)
				}
			}
		case *parser.GoComment:
			// Not rendered; it stays a source comment.
			flush()
			te.c.node(te.fe.file, "GoComment", n.Range)
			out = append(out, &kNote{text: strings.TrimSpace(n.Contents)})
		default:
			flush()
			out = append(out, withoutEmptyBlocks(te.node(node, nextNode))...)
		}
		if trailer, ok := node.(parser.WhitespaceTrailer); ok && inlineOrText(node) && inlineOrText(nextNode) && trailer.Trailing() != parser.SpaceNone {
			add(segment{literal: " "}, nil)
		}
	}
	flush()
	return out
}

// withoutEmptyBlocks drops the if and for blocks that render nothing: a
// block whose body held only local Go ({{ x = y }}) keeps that Go in the
// presenter and has nothing left for KTML. Trailing empty else and
// else-if branches go too; an empty branch before a non-empty one stays,
// since it decides which branch renders.
func withoutEmptyBlocks(nodes []kNode) []kNode {
	kept := nodes[:0:0]
	for _, node := range nodes {
		switch n := node.(type) {
		case *kIf:
			if n.hasElse && len(n.elseBody) == 0 {
				n.hasElse = false
			}
			end := len(n.branches)
			for !n.hasElse && end > 0 && len(n.branches[end-1].body) == 0 {
				end--
			}
			n.branches = n.branches[:end]
			if len(n.branches) == 0 {
				continue
			}
		case *kFor:
			if len(n.body) == 0 {
				continue
			}
		}
		kept = append(kept, node)
	}
	return kept
}

func trimWhitespace(nodes []parser.Node) []parser.Node {
	start, end := 0, len(nodes)
	for start < end {
		if _, ok := nodes[start].(*parser.Whitespace); !ok {
			break
		}
		start++
	}
	for end > start {
		if _, ok := nodes[end-1].(*parser.Whitespace); !ok {
			break
		}
		end--
	}
	return nodes[start:end]
}

// inlineOrText mirrors templ's generator: the nodes around which it keeps a
// trailing space.
func inlineOrText(node parser.Node) bool {
	switch n := node.(type) {
	case *parser.IfExpression, *parser.SwitchExpression, *parser.ForExpression, *parser.Text, *parser.StringExpression:
		return true
	case *parser.Element:
		return !n.IsBlockElement()
	}
	return false
}

func (te *tmplEmitter) node(node parser.Node, next parser.Node) []kNode {
	file := te.fe.file
	switch n := node.(type) {
	case *parser.DocType:
		src := te.c.node(file, "DocType", n.Range)
		if !strings.EqualFold(strings.TrimSpace(n.Value), "html") {
			te.partial(src, n.Range, "doctype", "only <!DOCTYPE html> converts")
			return nil
		}
		return []kNode{&kDoctype{src: src}}
	case *parser.Element:
		src := te.c.node(file, "Element", n.Range)
		attrs := te.attributes(n.Name, n.Attributes)
		children := te.children(n.Children, nil, stripAll)
		return []kNode{&kElement{name: n.Name, attrs: attrs, children: children, src: src}}
	case *parser.HTMLComment:
		return []kNode{&kComment{text: n.Contents, src: te.c.node(file, "HTMLComment", n.Range)}}
	case *parser.ChildrenExpression:
		return []kNode{&kOutlet{name: "children", src: te.c.node(file, "ChildrenExpression", n.Range)}}
	case *parser.RawElement:
		src := te.c.node(file, "RawElement", n.Range)
		attrs := te.attributes(n.Name, n.Attributes)
		el := &kElement{name: n.Name, attrs: attrs, src: src}
		switch {
		case strings.TrimSpace(n.Contents) == "":
		case strings.Contains(n.Contents, "```"):
			te.partial(src, n.Range, "raw-text-fence", "the body contains ``` and cannot be a raw-text literal; move it to a static file")
		default:
			el.children = []kNode{&kRaw{text: n.Contents}}
		}
		return []kNode{el}
	case *parser.ScriptElement:
		return []kNode{te.script(n)}
	case *parser.GoCode:
		te.goCode(n)
		return nil
	case *parser.IfExpression:
		return []kNode{te.ifExpr(n, next)}
	case *parser.ForExpression:
		return te.forExpr(n, next)
	case *parser.SwitchExpression:
		return te.switchExpr(n, next)
	case *parser.CallTemplateExpression:
		src := te.c.node(file, "CallTemplateExpression", n.Range)
		return te.call(n.Expression, nil, false, src, n.Range)
	case *parser.TemplElementExpression:
		src := te.c.node(file, "TemplElementExpression", n.Range)
		return te.call(n.Expression, n.Children, len(trimWhitespace(n.Children)) > 0, src, n.Range)
	case *parser.Fallthrough:
		src := te.c.node(file, "Fallthrough", n.Range)
		te.partial(src, n.Range, "fallthrough", "switch fallthrough has no KTML equivalent; merge the cases")
		return nil
	}
	te.fe.c.run.walkers[file].dropped++
	te.partial(nil, parser.Range{}, "unknown-node", fmt.Sprintf("unhandled templ node %T", node))
	return nil
}

// textSegments converts a text interpolation. Literal text that a Go
// string template carries ("n" + x, fmt.Sprintf("%d items", n)) stays in
// KTML; only its values become presenter fields.
func (te *tmplEmitter) textSegments(e parser.Expression, src *Node) []segment {
	expr, typed := te.w.goExpr(e)
	if expr == nil || !typed {
		te.partial(src, e.Range, "untyped-expression", "expression not type-checked")
		return nil
	}
	if text, ok := te.literalText(expr); ok {
		return []segment{{literal: text}}
	}
	if !te.view() {
		p, _ := te.paramPath(expr)
		return []segment{{path: p.name}}
	}
	if parts, ok := te.templateParts(expr); ok {
		return te.partSegments(parts, src, e.Range, nil)
	}
	return []segment{te.valueSegment(expr, src, e.Range, hintOf(expr))}
}

// valueSegment is one presenter field holding a value as templ renders it:
// integers as integers, anything else as a string.
func (te *tmplEmitter) valueSegment(expr ast.Expr, src *Node, r parser.Range, hint []string) segment {
	start, end := te.span(expr)
	goExpr := te.goText(expr, src, r)
	t := te.typeOf(expr)
	if isInteger(t) {
		return segment{path: te.fieldValue(start, end, "integer", int64Expr(goExpr, t), te.kindOf(expr), hint, src)}
	}
	return segment{path: te.fieldValue(start, end, "string", te.fe.goStringExpr(goExpr, t), te.kindOf(expr), hint, src)}
}

// templatePart is literal text or a Go value of a string template.
type templatePart struct {
	literal string
	expr    ast.Expr
}

// templateParts splits a string expression into its literal text and Go
// values: concatenations of literals and string values, and fmt.Sprintf
// with a constant format whose only verbs are %s of strings and %d of
// integers. It reports false for any other shape, or when there is no
// literal text to keep.
func (te *tmplEmitter) templateParts(expr ast.Expr) ([]templatePart, bool) {
	var parts []templatePart
	var split func(ast.Expr) bool
	split = func(expr ast.Expr) bool {
		expr = unparen(expr)
		if text, ok := te.literalText(expr); ok {
			parts = append(parts, templatePart{literal: text})
			return true
		}
		switch e := expr.(type) {
		case *ast.BinaryExpr:
			if e.Op == token.ADD && isString(te.typeOf(e)) {
				return split(e.X) && split(e.Y)
			}
		case *ast.CallExpr:
			if name, _ := te.w.callee(e, true); name == "fmt.Sprintf" && len(e.Args) > 0 {
				format, ok := te.literalText(e.Args[0])
				if !ok {
					return false
				}
				args := e.Args[1:]
				var literal strings.Builder
				for i := 0; i < len(format); i++ {
					if format[i] != '%' {
						literal.WriteByte(format[i])
						continue
					}
					if i+1 >= len(format) {
						return false
					}
					i++
					verb := format[i]
					if verb == '%' {
						literal.WriteByte('%')
						continue
					}
					if len(args) == 0 {
						return false
					}
					arg := args[0]
					args = args[1:]
					t := te.typeOf(arg)
					if verb == 's' && isString(t) || verb == 'd' && isInteger(t) {
						if literal.Len() > 0 {
							parts = append(parts, templatePart{literal: literal.String()})
							literal.Reset()
						}
						parts = append(parts, templatePart{expr: arg})
						continue
					}
					return false
				}
				if len(args) > 0 {
					return false
				}
				if literal.Len() > 0 {
					parts = append(parts, templatePart{literal: literal.String()})
				}
				return true
			}
		}
		if isString(te.typeOf(expr)) {
			parts = append(parts, templatePart{expr: expr})
			return true
		}
		return false
	}
	if !split(expr) {
		return nil, false
	}
	for _, part := range parts {
		if part.expr == nil && part.literal != "" {
			return parts, true
		}
	}
	return nil, false
}

// partSegments turns template parts into KTML segments, each Go value a
// presenter field.
func (te *tmplEmitter) partSegments(parts []templatePart, src *Node, r parser.Range, hint []string) []segment {
	var segments []segment
	for _, part := range parts {
		if part.expr == nil {
			segments = append(segments, segment{literal: part.literal})
			continue
		}
		words := hintOf(part.expr)
		if len(hint) > 0 && !te.isPath(part.expr) {
			words = append(append([]string{}, hint...), words...)
		}
		segments = append(segments, te.valueSegment(part.expr, src, r, words))
	}
	return segments
}

func isInteger(t types.Type) bool {
	if t == nil {
		return false
	}
	basic, ok := t.Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsInteger != 0
}

func int64Expr(expr string, t types.Type) string {
	if basic, ok := t.(*types.Basic); ok && basic.Kind() == types.Int64 {
		return expr
	}
	return "int64(" + expr + ")"
}

// kindOf classifies a presenter field by the templ expression it copies.
func (te *tmplEmitter) kindOf(expr ast.Expr) fieldKind {
	if te.isPath(expr) {
		return fieldPassThrough
	}
	switch e := unparen(expr).(type) {
	case *ast.UnaryExpr:
		if e.Op == token.NOT && te.isPath(e.X) {
			return fieldPassThrough
		}
	case *ast.BinaryExpr:
		if (e.Op == token.NEQ || e.Op == token.EQL) && (isNil(e.Y) && te.isPath(e.X) || isNil(e.X) && te.isPath(e.Y)) {
			return fieldPassThrough
		}
	case *ast.CallExpr:
		// A conversion of a path, such as string(id).
		if tv, ok := te.fe.t.info.Types[e.Fun]; ok && tv.IsType() && len(e.Args) == 1 && te.isPath(e.Args[0]) {
			return fieldPassThrough
		}
	}
	return fieldDerived
}

// isPath reports a selector path of locals and fields.
func (te *tmplEmitter) isPath(expr ast.Expr) bool {
	switch e := unparen(expr).(type) {
	case *ast.Ident:
		v, ok := te.fe.t.info.Uses[e].(*types.Var)
		return ok && !v.IsField()
	case *ast.SelectorExpr:
		selection := te.fe.t.info.Selections[e]
		return selection != nil && selection.Kind() == types.FieldVal && te.isPath(e.X)
	case *ast.StarExpr:
		return te.isPath(e.X)
	}
	return false
}

// conditionHint names a boolean field: has/no for presence and emptiness
// tests, the path itself for a boolean path, and is for anything else.
func conditionHint(expr ast.Expr) []string {
	switch e := unparen(expr).(type) {
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			return append([]string{"not"}, conditionHint(e.X)...)
		}
	case *ast.BinaryExpr:
		subject := e.X
		empty := isNil(e.Y)
		if lit, ok := e.Y.(*ast.BasicLit); ok && (lit.Value == `""` || lit.Value == "0") {
			empty = true
		}
		if call, ok := unparen(e.X).(*ast.CallExpr); ok && len(call.Args) == 1 {
			if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "len" {
				subject = call.Args[0]
			}
		}
		if empty {
			switch e.Op {
			case token.NEQ, token.GTR:
				return append([]string{"has"}, lastWords(hintOf(subject), 2)...)
			case token.EQL:
				return append([]string{"no"}, lastWords(hintOf(subject), 2)...)
			}
		}
		return append([]string{"is"}, lastWords(hintOf(expr), 3)...)
	case *ast.Ident, *ast.SelectorExpr:
		return lastWords(hintOf(expr), 2)
	}
	return append([]string{"is"}, lastWords(hintOf(expr), 3)...)
}

func lastWords(words []string, n int) []string {
	if len(words) > n {
		return words[len(words)-n:]
	}
	return words
}

// hintOf names a field after an expression's identifiers.
func hintOf(expr ast.Expr) []string {
	var words []string
	ast.Inspect(expr, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if x, ok := node.X.(*ast.Ident); ok && isPackageLike(x.Name) {
				words = append(words, node.Sel.Name)
				return false
			}
		case *ast.Ident:
			if !skipWord[node.Name] {
				words = append(words, node.Name)
			}
		case *ast.BasicLit:
			return false
		}
		return true
	})
	if len(words) > 3 {
		words = words[len(words)-3:]
	}
	if len(words) == 0 {
		words = []string{"value"}
	}
	return words
}

var skipWord = map[string]bool{"fmt": true, "Sprintf": true, "Sprint": true, "String": true, "string": true, "int": true, "int64": true, "len": true, "strconv": true, "Itoa": true, "templ": true, "nil": true, "true": true, "false": true, "URL": true}

func isPackageLike(name string) bool {
	switch name {
	case "fmt", "strconv", "strings", "templ", "time", "url":
		return true
	}
	return false
}

// --- local Go ---

func (te *tmplEmitter) goCode(n *parser.GoCode) {
	src := te.c.node(te.fe.file, "GoCode", n.Range)
	if !te.view() {
		te.partial(src, n.Range, "local-initialization", "local Go in a direct template")
		return
	}
	start, end, ok := te.fe.rw.span(n.Expression)
	if !ok {
		te.partial(src, n.Range, "go-not-found", "local Go not found in the generated Go")
		return
	}
	text, problems := te.fe.rw.rewrite(start, end)
	for _, problem := range problems {
		te.partial(src, n.Range, "presenter-reference", problem)
	}
	te.declare(start, end, te.scope)
	if len(problems) > 0 {
		// Comment the statements out; whatever uses their locals is
		// unresolved in turn.
		te.line("// TODO(ktml-migrate): " + problems[0])
		for _, line := range dedent(text) {
			te.line("// " + line)
		}
		te.poison(start, end, "its statement is commented out")
		return
	}
	lines := dedent(text)
	inRaw := rawContinuations(text)
	for i, line := range lines {
		if inRaw[i] {
			// Inside a raw string literal the text is the value: neither
			// dedented nor indented.
			te.body.WriteString(line + "\n")
			continue
		}
		te.line(line)
	}
	te.localsMarker(start, end)

}

// poison marks the locals a generated range defines as unresolved.
func (te *tmplEmitter) poison(start, end int, reason string) {
	for _, ident := range te.fe.rw.idents {
		offset := te.fe.t.tok.Offset(ident.Pos())
		if offset >= start && offset < end {
			if obj := te.fe.t.info.Defs[ident]; obj != nil {
				te.c.poisoned[obj] = reason
			}
		}
	}
}

// localsMarker records the locals a presenter statement declares. When the
// presenter is assembled, a local that nothing reads any more (its readers
// were template-only or commented out) gets a blank assignment, which Go
// requires.
func (te *tmplEmitter) localsMarker(start, end int) {
	// Only the statement's own locals, not those of blocks inside it.
	scope := te.fe.t.pkg.Scope().Innermost(te.fe.t.tok.Pos(start))
	var names []string
	for _, ident := range te.fe.rw.idents {
		offset := te.fe.t.tok.Offset(ident.Pos())
		if offset >= start && offset < end && ident.Name != "_" && te.fe.t.info.Defs[ident] != nil {
			if v, isVar := te.fe.t.info.Defs[ident].(*types.Var); isVar && v.Parent() == scope {
				names = append(names, ident.Name)
			}
		}
	}
	if len(names) > 0 {
		te.line(localsPrefix + strings.Join(names, ","))
	}
}

const localsPrefix = "//ktml:locals "

// resolveLocals turns each locals marker into blank assignments for the
// locals no later code line reads, and drops it otherwise.
func resolveLocals(body string) string {
	lines := strings.Split(body, "\n")
	var out []string
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, localsPrefix) {
			out = append(out, line)
			continue
		}
		indent := leading(line)
		for _, name := range strings.Split(strings.TrimPrefix(trimmed, localsPrefix), ",") {
			pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
			// Search the rest of the marker's block only.
			used, depth := false, 0
			for _, later := range lines[i+1:] {
				code := strings.TrimSpace(later)
				if strings.HasPrefix(code, "//") {
					continue
				}
				if pattern.MatchString(code) {
					used = true
					break
				}
				depth += strings.Count(code, "{") - strings.Count(code, "}")
				if depth < 0 {
					break
				}
			}
			if !used {
				out = append(out, indent+"_ = "+name)
			}
		}
	}
	return strings.Join(out, "\n")
}

// rawContinuations reports, for each line of strings.Trim(text, "\n"), whether
// it starts inside a raw string literal.
func rawContinuations(text string) []bool {
	text = strings.Trim(text, "\n")
	inside := make([]bool, strings.Count(text, "\n")+1)
	file := token.NewFileSet().AddFile("", -1, len(text))
	var sc scanner.Scanner
	sc.Init(file, []byte(text), nil, 0)
	for {
		pos, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.STRING && strings.HasPrefix(lit, "`") {
			first := file.Line(pos)
			for line := first + 1; line <= first+strings.Count(lit, "\n"); line++ {
				inside[line-1] = true
			}
		}
	}
	return inside
}

// dedent splits Go source into lines without their common indentation. Lines
// inside a raw string literal are kept as they are.
func dedent(text string) []string {
	lines := strings.Split(strings.Trim(text, "\n"), "\n")
	inRaw := rawContinuations(text)
	common := -1
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" || inRaw[i] {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, "\t "))
		if common < 0 || indent < common {
			common = indent
		}
	}
	for i := range lines {
		if inRaw[i] {
			continue
		}
		lines[i] = strings.TrimSpace(lines[i][:min(len(lines[i]), max(common, 0))]) + lines[i][min(len(lines[i]), max(common, 0)):]
		if i == 0 {
			lines[i] = strings.TrimSpace(lines[i])
		}
	}
	return lines
}

// --- conditions ---

// condition returns the KTML condition of a templ condition: in a view
// template a boolean field of the presenter (whose Go reference it also
// returns); in a direct template the same boolean grammar over inputs.
func (te *tmplEmitter) condition(expr ast.Expr, src *Node, r parser.Range, hint string) (string, string) {
	// Content presence has no presenter side: the branch's presenter
	// statements run unconditionally (goRef "").
	if cond, ok := contentPresence(te.paramPath, expr); ok {
		return cond, ""
	}
	if !te.view() {
		return te.directCondition(expr), ""
	}
	start, end := te.span(expr)
	goExpr := te.goText(expr, src, r)
	scope := te.scopeOf(start, end)
	words := conditionHint(expr)
	if hint != "" {
		words = append([]string{hint}, hintOf(expr)...)
	}
	path := te.fieldIn(scope, "boolean", goExpr, te.localIdentity(start, end), te.kindOf(expr), words, src)
	field := path[strings.LastIndex(path, ".")+1:]
	return path, scope.goVar + "." + upperFirst(field)
}

func (te *tmplEmitter) directCondition(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return "(" + te.directCondition(e.X) + ")"
	case *ast.UnaryExpr:
		return "!" + te.directCondition(e.X)
	case *ast.BinaryExpr:
		return te.directCondition(e.X) + " " + e.Op.String() + " " + te.directCondition(e.Y)
	case *ast.Ident:
		return e.Name
	}
	return "false"
}

func (te *tmplEmitter) ifExpr(n *parser.IfExpression, next parser.Node) kNode {
	src := te.c.node(te.fe.file, "IfExpression", n.Range)
	result := &kIf{src: src}
	type branch struct {
		e     parser.Expression
		nodes []parser.Node
		src   *Node
		r     parser.Range
	}
	branches := []branch{{n.Expression, n.Then, src, n.Range}}
	for _, elseIf := range n.ElseIfs {
		branches = append(branches, branch{elseIf.Expression, elseIf.Then, te.c.node(te.fe.file, "ElseIfExpression", elseIf.Range), elseIf.Range})
	}
	// The presenter nests each later condition in the previous else, so a
	// condition is evaluated only when templ would evaluate it.
	closers := 0
	inElse := false
	for i, b := range branches {
		cond, goRef, opened := te.branchCondition(b.e, b.src, b.r)
		closers += opened
		more := i < len(branches)-1 || len(n.Else) > 0
		if te.view() {
			if goRef == "" {
				te.open("{")
			} else {
				te.open("if " + goRef + " {")
			}
		}
		body := te.children(b.nodes, next, stripEnds)
		result.branches = append(result.branches, kBranch{cond: cond, body: body})
		if !te.view() {
			continue
		}
		inElse = false
		if goRef != "" && more {
			te.close("} else {")
			te.indent++
			te.blocks = append(te.blocks, map[string]bool{})
			closers++
			inElse = true
		} else {
			te.close("}")
		}
	}
	if len(n.Else) > 0 {
		result.hasElse = true
		if te.view() && !inElse {
			te.open("{")
			closers++
		}
		result.elseBody = te.children(n.Else, next, stripEnds)
	}
	for ; closers > 0; closers-- {
		te.close("}")
	}
	return result
}

// branchCondition evaluates an if or else-if condition, with its init
// statement in an enclosing presenter block. It returns how many blocks it
// opened.
func (te *tmplEmitter) branchCondition(e parser.Expression, src *Node, r parser.Range) (string, string, int) {
	expr, typed := te.w.goExpr(e)
	if expr != nil && typed {
		cond, goRef := te.condition(expr, src, r, "")
		return cond, goRef, 0
	}
	if !te.view() || !hasInitializer(e.Value) {
		te.partial(src, r, "condition", "condition not type-checked")
		return "false", "false", 0
	}
	// if init; cond: the generated Go spells the clause verbatim.
	start, _, ok := te.fe.rw.span(e)
	if !ok {
		te.partial(src, r, "condition", "condition not found in the generated Go")
		return "false", "false", 0
	}
	file, err := goparser.ParseFile(token.NewFileSet(), "", "package p\nfunc _() {\nif "+e.Value+" {}\n}", 0)
	if err != nil {
		te.partial(src, r, "condition", err.Error())
		return "false", "false", 0
	}
	stmt := file.Decls[0].(*ast.FuncDecl).Body.List[0].(*ast.IfStmt)
	base := len("package p\nfunc _() {\nif ") + 1
	initStart, initEnd := start+int(stmt.Init.Pos())-base, start+int(stmt.Init.End())-base
	condStart, condEnd := start+int(stmt.Cond.Pos())-base, start+int(stmt.Cond.End())-base
	te.open("{")
	initText, problems := te.fe.rw.rewrite(initStart, initEnd)
	for _, problem := range problems {
		te.partial(src, r, "presenter-reference", problem)
	}
	te.declare(initStart, initEnd, te.scope)
	if len(problems) > 0 {
		te.line("// TODO(ktml-migrate): " + initText)
		te.poison(initStart, initEnd, "its statement is commented out")
	} else {
		te.line(initText)
		te.localsMarker(initStart, initEnd)
	}
	var condExpr ast.Expr
	for _, candidate := range te.fe.t.nodes[condStart] {
		if ce, ok := candidate.(ast.Expr); ok && te.fe.t.tok.Offset(ce.End()) == condEnd {
			condExpr = ce
			break
		}
	}
	if condExpr == nil {
		te.partial(src, r, "condition", "condition not found in the generated Go")
		return "false", "false", 1
	}
	cond, goRef := te.condition(condExpr, src, r, "")
	return cond, goRef, 1
}

// removeEmptyElses rewrites "} else {\n<indent>}" anywhere into "}".
func removeEmptyElses(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "} else {" && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "}" &&
			strings.TrimLeft(lines[i], "\t") != lines[i+1] && leading(lines[i]) == leading(lines[i+1]) {
			out = append(out, lines[i][:len(leading(lines[i]))]+"}")
			i++
			continue
		}
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n")
}

// foldEmptyBranches drops presenter if statements whose branch assigns
// nothing (the condition field is still set for the template), and the
// empty scope blocks of conditions with no presenter field (a content
// slot's presence), and turns an empty then-branch with an else into a
// negated condition.
func foldEmptyBranches(text string) string {
	for {
		lines := strings.Split(text, "\n")
		var out []string
		changed := false
		for i := 0; i < len(lines); i++ {
			line := strings.TrimSpace(lines[i])
			if line == "{" && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "}" && leading(lines[i]) == leading(lines[i+1]) {
				i++
				changed = true
				continue
			}
			if strings.HasPrefix(line, "if ") && strings.HasSuffix(line, " {") && i+1 < len(lines) && leading(lines[i]) == leading(lines[i+1]) {
				next := strings.TrimSpace(lines[i+1])
				cond := strings.TrimSuffix(strings.TrimPrefix(line, "if "), " {")
				switch next {
				case "}":
					i++
					changed = true
					continue
				case "} else {":
					out = append(out, leading(lines[i])+"if !"+cond+" {")
					i++
					changed = true
					continue
				}
			}
			out = append(out, lines[i])
		}
		result := strings.Join(out, "\n")
		if !changed {
			return result
		}
		text = result
	}
}

func leading(line string) string { return line[:len(line)-len(strings.TrimLeft(line, "\t"))] }
