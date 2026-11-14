package templmigrate

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	parser "github.com/a-h/templ/parser/v2"
	"github.com/karl-tye/ktml/internal/compiler"
)

// directCheck decides whether a template converts to a component whose
// inputs are its own scalar parameters, with no presenter. reason names the
// first construct that needs one.
type directCheck struct {
	c      *conversion
	sig    *tmplSig
	w      *walker
	reason string
}

func (d *directCheck) fail(reason string) bool {
	if d.reason == "" {
		d.reason = reason
	}
	return false
}

func (d *directCheck) nodes(nodes []parser.Node) bool {
	for _, node := range nodes {
		if !d.node(node) {
			return false
		}
	}
	return true
}

func (d *directCheck) node(node parser.Node) bool {
	switch n := node.(type) {
	case *parser.Text, *parser.Whitespace, *parser.HTMLComment, *parser.GoComment, *parser.ChildrenExpression:
		return true
	case *parser.DocType:
		return strings.EqualFold(strings.TrimSpace(n.Value), "html") || d.fail("doctype")
	case *parser.Element:
		return d.attributes(n.Name, n.Attributes) && d.nodes(n.Children)
	case *parser.RawElement:
		return d.attributes(n.Name, n.Attributes) && (!strings.Contains(n.Contents, "```") || d.fail("raw text fence"))
	case *parser.ScriptElement:
		for _, content := range n.Contents {
			if content.GoCode != nil {
				return d.fail("script with Go values")
			}
			if content.Value != nil && strings.Contains(*content.Value, "```") {
				return d.fail("raw text fence")
			}
		}
		return d.attributes("script", n.Attributes)
	case *parser.StringExpression:
		expr, _ := d.w.goExpr(n.Expression)
		return d.text(expr) || d.fail("text "+n.Expression.Value)
	case *parser.IfExpression:
		if !d.condition(n.Expression) || !d.nodes(n.Then) || !d.nodes(n.Else) {
			return false
		}
		for _, elseIf := range n.ElseIfs {
			if !d.condition(elseIf.Expression) || !d.nodes(elseIf.Then) {
				return false
			}
		}
		return true
	case *parser.CallTemplateExpression:
		return d.call(n.Expression)
	case *parser.TemplElementExpression:
		return d.call(n.Expression) && d.nodes(n.Children)
	case *parser.ForExpression:
		if _, ok := contentLoop(d.w, d.paramKind, n); ok {
			return true
		}
	}
	return d.fail("construct " + nodeKind(node))
}

func nodeKind(node parser.Node) string {
	switch node.(type) {
	case *parser.GoCode:
		return "local initialization"
	case *parser.ForExpression:
		return "loop"
	case *parser.SwitchExpression:
		return "switch"
	}
	return "node"
}

// paramKind returns the kind of a template parameter named by expr.
func (d *directCheck) paramKind(expr ast.Expr) (sigParam, bool) {
	ident, ok := unparen(expr).(*ast.Ident)
	if !ok {
		return sigParam{}, false
	}
	v, ok := d.w.typed.info.Uses[ident].(*types.Var)
	if !ok {
		return sigParam{}, false
	}
	for _, p := range d.sig.params {
		if p.obj == v {
			return p, true
		}
	}
	return sigParam{}, false
}

func unparen(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.X
	}
}

// literal reports a string, number, or boolean literal, or a Go constant.
func (d *directCheck) literal(expr ast.Expr) bool {
	expr = unparen(expr)
	if _, ok := expr.(*ast.BasicLit); ok {
		return true
	}
	if ident, ok := expr.(*ast.Ident); ok && (ident.Name == "true" || ident.Name == "false") {
		return true
	}
	tv, ok := d.w.typed.info.Types[expr]
	return ok && tv.Value != nil
}

func (d *directCheck) text(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	if d.literal(expr) {
		return isString(d.w.typed.info.TypeOf(expr))
	}
	p, ok := d.paramKind(expr)
	if !ok {
		return false
	}
	kind, _ := ktmlScalar(p.t)
	return kind == "string" || kind == "integer"
}

func (d *directCheck) condition(e parser.Expression) bool {
	expr, typed := d.w.goExpr(e)
	if expr == nil || !typed {
		return d.fail("condition " + e.Value)
	}
	return d.boolExpr(expr) || d.fail("condition "+e.Value)
}

func (d *directCheck) boolExpr(expr ast.Expr) bool {
	if _, ok := contentPresence(d.paramKind, expr); ok {
		return true
	}
	switch e := unparen(expr).(type) {
	case *ast.UnaryExpr:
		return e.Op == token.NOT && d.boolExpr(e.X)
	case *ast.BinaryExpr:
		return (e.Op == token.LAND || e.Op == token.LOR) && d.boolExpr(e.X) && d.boolExpr(e.Y)
	}
	p, ok := d.paramKind(expr)
	if !ok {
		return false
	}
	kind, _ := ktmlScalar(p.t)
	return kind == "boolean"
}

func (d *directCheck) attributes(element string, attrs []parser.Attribute) bool {
	for _, attr := range attrs {
		if !d.attribute(element, attr) {
			return false
		}
	}
	return true
}

func (d *directCheck) attribute(element string, attribute parser.Attribute) bool {
	switch a := attribute.(type) {
	case *parser.ConstantAttribute:
		_, ok := constantKey(a.Key)
		return ok || d.fail("dynamic attribute name")
	case *parser.BoolConstantAttribute:
		_, ok := constantKey(a.Key)
		return ok || d.fail("dynamic attribute name")
	case *parser.BoolExpressionAttribute:
		expr, _ := d.w.goExpr(a.Expression)
		return expr != nil && d.boolExpr(expr) || d.fail("boolean attribute "+a.Expression.Value)
	case *parser.ExpressionAttribute:
		name, ok := constantKey(a.Key)
		if !ok {
			return d.fail("dynamic attribute name")
		}
		lower := strings.ToLower(name)
		if lower == "class" {
			if elements, _ := d.w.listElements(a.Expression); elements != nil {
				for _, element := range elements {
					if !d.literal(element) && !d.text(element) {
						return d.fail("class " + a.Expression.Value)
					}
				}
				return true
			}
		}
		expr, _ := d.w.goExpr(a.Expression)
		if expr == nil {
			return d.fail("attribute " + name)
		}
		switch {
		case d.literal(expr):
			return true
		case lower == "style", templURLAttribute(element, lower), compiler.URLAttribute(name) && !compiler.ScriptAttribute(name):
			return d.fail("attribute " + name)
		case compiler.ScriptAttribute(name):
			return d.boolExpr(expr) || d.fail("script attribute "+name)
		}
		return d.text(expr) || d.fail("attribute "+name)
	case *parser.SpreadAttributes:
		expr, _ := d.w.goExpr(a.Expression)
		p, ok := d.paramKind(expr)
		return ok && p.kind == "attributes" || d.fail("spread "+a.Expression.Value)
	case *parser.ConditionalAttribute:
		return d.condition(a.Expression) && d.attributes(element, a.Then) && d.attributes(element, a.Else)
	}
	return d.fail("attribute")
}

// call accepts a call of a direct template whose arguments are literals,
// the caller's inputs, nil content, or direct template calls (as content).
func (d *directCheck) call(e parser.Expression) bool {
	expr, typed := d.w.goExpr(e)
	if expr == nil || !typed {
		return d.fail("call " + e.Value)
	}
	if p, ok := d.paramKind(expr); ok && p.kind == "component" {
		return true
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return d.fail("dynamic component " + e.Value)
	}
	return d.callExpr(call)
}

func (d *directCheck) callExpr(call *ast.CallExpr) bool {
	callee := d.c.callee(d.w, call)
	if callee == nil {
		if name, _ := d.w.callee(call, true); name == templPath+".JSFuncCall" {
			for _, arg := range call.Args {
				if _, ok := arg.(*ast.BasicLit); !ok {
					return d.fail("script call with data")
				}
			}
			return true
		}
		return d.fail("call of a non-template")
	}
	if callee.view {
		return d.fail("call of " + callee.name + ", which needs a presenter")
	}
	for i, arg := range call.Args {
		if i >= len(callee.params) {
			break
		}
		param := callee.params[i]
		args := []ast.Expr{arg}
		if param.kind == "components" {
			args = call.Args[i:]
		}
		for _, arg := range args {
			if !d.argument(param, arg) {
				return false
			}
		}
		if param.kind == "components" {
			break
		}
	}
	return true
}

func (d *directCheck) argument(param sigParam, arg ast.Expr) bool {
	arg = unparen(arg)
	if ident, ok := arg.(*ast.Ident); ok && ident.Name == "nil" {
		return param.kind != "scalar" || d.fail("nil scalar argument")
	}
	switch param.kind {
	case "component", "components":
		if p, ok := d.paramKind(arg); ok && (p.kind == "component") {
			return true
		}
		if call, ok := arg.(*ast.CallExpr); ok {
			return d.callExpr(call)
		}
		return d.fail("component argument")
	case "attributes":
		if p, ok := d.paramKind(arg); ok && p.kind == "attributes" {
			return true
		}
		if _, ok := staticAttributes(arg); ok {
			return true
		}
		return d.fail("attributes argument")
	}
	if d.literal(arg) {
		return true
	}
	p, ok := d.paramKind(arg)
	if !ok {
		return d.fail("argument")
	}
	got, _ := ktmlScalar(p.t)
	want, _ := ktmlScalar(param.t)
	return got == want || d.fail("argument type")
}

// staticAttributes reads a templ.Attributes composite literal with constant
// keys and literal values, in templ's rendering order (sorted by key).
func staticAttributes(expr ast.Expr) ([]staticAttribute, bool) {
	literal, ok := unparen(expr).(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	selector, ok := literal.Type.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Attributes" {
		return nil, false
	}
	if x, ok := selector.X.(*ast.Ident); !ok || x.Name != "templ" {
		return nil, false
	}
	var entries []staticAttribute
	for _, element := range literal.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return nil, false
		}
		key, ok := kv.Key.(*ast.BasicLit)
		if !ok || key.Kind != token.STRING {
			return nil, false
		}
		name, err := strconv.Unquote(key.Value)
		if err != nil {
			return nil, false
		}
		switch value := kv.Value.(type) {
		case *ast.BasicLit:
			text := value.Value
			if value.Kind == token.STRING {
				if text, err = strconv.Unquote(value.Value); err != nil {
					return nil, false
				}
			}
			entries = append(entries, staticAttribute{name: name, value: text})
		case *ast.Ident:
			switch value.Name {
			case "true":
				entries = append(entries, staticAttribute{name: name, boolean: true, present: true})
			case "false":
				entries = append(entries, staticAttribute{name: name, boolean: true})
			default:
				return nil, false
			}
		default:
			return nil, false
		}
	}
	sortStatic(entries)
	return entries, true
}

// staticAttribute is one entry of a static templ.Attributes literal: a
// string value, or a boolean that renders the bare name when present.
type staticAttribute struct {
	name    string
	value   string
	boolean bool
	present bool
}

func sortStatic(entries []staticAttribute) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].name < entries[j-1].name; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

// contentPresence reads a presence test of a content input: c != nil,
// c == nil, and len(c) > 0 or == 0 for a variadic component list. It
// returns the KTML condition (c? or !c?).
func contentPresence(param func(ast.Expr) (sigParam, bool), expr ast.Expr) (string, bool) {
	content := func(x ast.Expr) (string, bool) {
		p, ok := param(x)
		if !ok || p.kind != "component" && p.kind != "components" {
			return "", false
		}
		return p.ktml, true
	}
	switch e := unparen(expr).(type) {
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			if inner, ok := contentPresence(param, e.X); ok {
				return negatePresence(inner), true
			}
		}
	case *ast.BinaryExpr:
		if (e.Op == token.NEQ || e.Op == token.EQL) && isNil(e.Y) {
			if name, ok := content(e.X); ok {
				if e.Op == token.NEQ {
					return name + "?", true
				}
				return "!" + name + "?", true
			}
		}
		if call, ok := unparen(e.X).(*ast.CallExpr); ok && len(call.Args) == 1 {
			if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "len" {
				if name, ok := content(call.Args[0]); ok {
					if lit, ok := e.Y.(*ast.BasicLit); ok && lit.Value == "0" {
						switch e.Op {
						case token.GTR, token.NEQ:
							return name + "?", true
						case token.EQL:
							return "!" + name + "?", true
						}
					}
				}
			}
		}
	}
	return "", false
}

func negatePresence(cond string) string {
	if strings.HasPrefix(cond, "!") {
		return cond[1:]
	}
	return "!" + cond
}

// contentLoop reads for _, c := range content { @c }, a loop that renders
// every component of a variadic content input: in KTML, its outlet.
func contentLoop(w *walker, param func(ast.Expr) (sigParam, bool), n *parser.ForExpression) (sigParam, bool) {
	stmt, ok := stmtContaining[*ast.RangeStmt](w.typed, n.Expression)
	if !ok {
		return sigParam{}, false
	}
	p, ok := param(stmt.X)
	if !ok || p.kind != "components" {
		return sigParam{}, false
	}
	value, ok := stmt.Value.(*ast.Ident)
	if !ok {
		return sigParam{}, false
	}
	body := trimWhitespace(n.Children)
	if len(body) != 1 {
		return sigParam{}, false
	}
	var e parser.Expression
	switch call := body[0].(type) {
	case *parser.TemplElementExpression:
		if len(trimWhitespace(call.Children)) > 0 {
			return sigParam{}, false
		}
		e = call.Expression
	case *parser.CallTemplateExpression:
		e = call.Expression
	default:
		return sigParam{}, false
	}
	return p, strings.TrimSpace(e.Value) == value.Name
}
