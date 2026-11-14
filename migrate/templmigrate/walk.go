package templmigrate

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"go/types"
	"regexp"
	"strings"

	parser "github.com/a-h/templ/parser/v2"
	"github.com/karl-tye/ktml/internal/scanner"
)

// PresenterNeed records, per templ declaration, the presenter and Host
// Schema work its scaffolded constructs imply.
type PresenterNeed struct {
	File     string `json:"file"`
	Template string `json:"template"`
	Category string `json:"category"`
	Excluded bool   `json:"excluded,omitempty"`
	// Derived counts scaffolded expressions: each becomes a presenter-built
	// Host Schema field.
	Derived int `json:"derived"`
	// States counts switch statements that become closed Presentation States.
	States int `json:"states"`
	// Records lists the Go types that need Host Schema records.
	Records []string `json:"records,omitempty"`
}

type walker struct {
	file    *sourceFile
	typed   *typedFile
	symbols *symbols

	nodes      []Node
	dropped    int
	templates  int
	presenters []PresenterNeed

	template string
	need     *PresenterNeed
	imports  map[string]string
	scopes   []map[string]string
}

func (w *walker) walkFile() {
	// templ imports its runtime implicitly as "templ".
	w.imports = map[string]string{"templ": templPath}
	for _, header := range w.file.parsed.Header {
		w.goDeclarations("TemplateFileGoExpression", header.Expression, true)
	}
	pkg := w.file.parsed.Package.Expression
	w.add("Package", "package", Converted, nil, "", pkg.Range, pkg.Value)
	for _, node := range w.file.parsed.Nodes {
		w.fileNode(node)
	}
}

func (w *walker) fileNode(node parser.TemplateFileNode) {
	switch n := node.(type) {
	case *parser.TemplateFileGoExpression:
		w.goDeclarations("TemplateFileGoExpression", n.Expression, n.BeforePackage)
	case *parser.HTMLTemplate:
		w.htmlTemplate(n)
	case *parser.CSSTemplate:
		w.template = n.Name
		w.add("CSSTemplate", "css-template", Blocked, []string{"css-template"}, "css-templates", n.Range, n.Name)
		for _, property := range n.Properties {
			switch p := property.(type) {
			case *parser.ConstantCSSProperty:
				w.add("ConstantCSSProperty", "css-property", Blocked, []string{"css-template"}, "css-templates", n.Range, p.Name)
			case *parser.ExpressionCSSProperty:
				w.add("ExpressionCSSProperty", "css-property", Blocked, []string{"css-template"}, "css-templates", n.Range, p.Name)
				w.add("StringExpression", "css-property-value", Blocked, []string{"css-value"}, "css-value", p.Value.Range, p.Value.Expression.Value)
			default:
				w.drop(fmt.Sprintf("%T", property), n.Range)
			}
		}
		w.template = ""
	case *parser.ScriptTemplate:
		w.template = n.Name.Value
		w.add("ScriptTemplate", "script-template", Blocked, []string{"script-template"}, "script-templates", n.Range, n.Name.Value)
		w.template = ""
	default:
		w.drop(fmt.Sprintf("%T", node), parser.Range{})
	}
}

// goDeclarations classifies top-level Go in a templ file.
func (w *walker) goDeclarations(kind string, expression parser.Expression, header bool) {
	src := expression.Value
	if header {
		w.add(kind, "file-header", Converted, nil, "", expression.Range, src)
		return
	}
	file, err := goparser.ParseFile(token.NewFileSet(), "", "package p\n"+src, goparser.ParseComments)
	if err != nil {
		w.add(kind, "go-declaration", ReviewRequired, []string{"unparsed-go"}, "", expression.Range, src)
		return
	}
	status := Converted
	construct := "go-comment"
	var reasons []string
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				if construct == "go-comment" {
					construct = "go-imports"
				}
				for _, spec := range d.Specs {
					w.addImport(spec.(*ast.ImportSpec))
				}
				continue
			}
			construct = "go-declaration"
			status = maxStatus(status, PresentationScaffolded)
			reasons = appendUnique(reasons, "go-"+strings.ToLower(d.Tok.String())+"-moves-to-presenter")
		case *ast.FuncDecl:
			construct = "go-declaration"
			status = maxStatus(status, PresentationScaffolded)
			reasons = appendUnique(reasons, "go-func-moves-to-presenter")
		}
	}
	w.add(kind, construct, status, reasons, "", expression.Range, src)
}

func (w *walker) addImport(spec *ast.ImportSpec) {
	path := strings.Trim(spec.Path.Value, `"`)
	name := path[strings.LastIndex(path, "/")+1:]
	if spec.Name != nil {
		name = spec.Name.Name
	}
	w.imports[name] = path
}

func (w *walker) htmlTemplate(n *parser.HTMLTemplate) {
	w.templates++
	name, receiver, params := w.signature(n.Expression)
	w.template = name
	w.presenters = append(w.presenters, PresenterNeed{File: w.file.rel, Template: name, Category: w.file.category, Excluded: w.file.excluded != ""})
	w.need = &w.presenters[len(w.presenters)-1]
	w.scopes = []map[string]string{{}}
	status := Converted
	var reasons []string
	if receiver != "" {
		status = PresentationScaffolded
		reasons = append(reasons, "method-template")
		w.need.Records = appendUnique(w.need.Records, receiver)
	}
	for _, param := range params {
		w.declare(param.name, param.kind)
		paramStatus, reason := paramStatus(param)
		status = maxStatus(status, paramStatus)
		if reason != "" {
			reasons = appendUnique(reasons, reason)
		}
		for _, record := range param.records {
			w.need.Records = appendUnique(w.need.Records, record)
		}
	}
	w.add("HTMLTemplate", "component-declaration", status, reasons, "", n.Range, n.Expression.Value)
	w.nodesOf(n.Children, "")
	w.template = ""
	w.need = nil
}

type param struct {
	name    string
	kind    string // "scalar", "optional-scalar", "named-scalar", "component", "components", "func", "context", "record"
	records []string
}

func paramStatus(p param) (Status, string) {
	switch p.kind {
	case "scalar", "optional-scalar":
		return Converted, ""
	case "named-scalar":
		return PresentationScaffolded, "named-scalar-input"
	case "component", "components":
		return ReviewRequired, "component-valued-input"
	case "func":
		return ReviewRequired, "function-valued-input"
	case "attributes":
		// A templ.Attributes input becomes an Attributes input, forwarded
		// with ...name (migration-language-v1).
		return Converted, "attributes-input"
	case "context":
		return PresentationScaffolded, "request-context-input"
	default:
		return PresentationScaffolded, "host-schema-record-input"
	}
}

// signature returns a template's name, receiver type, and parameters, with
// go/types when available and syntactically otherwise.
func (w *walker) signature(expression parser.Expression) (string, string, []param) {
	if w.typed != nil {
		if ident, ok := nodeAt[*ast.Ident](w.typed, expression); ok {
			if fn, ok := w.typed.info.Defs[ident].(*types.Func); ok {
				signature := fn.Type().(*types.Signature)
				receiver := ""
				if recv := signature.Recv(); recv != nil {
					receiver = typeName(recv.Type())
				}
				var params []param
				for i := 0; i < signature.Params().Len(); i++ {
					v := signature.Params().At(i)
					t := v.Type()
					if signature.Variadic() && i == signature.Params().Len()-1 {
						t = types.NewSlice(t.(*types.Slice).Elem())
					}
					params = append(params, typedParam(v.Name(), t))
				}
				return fn.Name(), receiver, params
			}
		}
	}
	file, err := goparser.ParseFile(token.NewFileSet(), "", "package p\nfunc "+expression.Value+" {}", 0)
	if err != nil || len(file.Decls) == 0 {
		return strings.TrimSpace(strings.SplitN(expression.Value, "(", 2)[0]), "", nil
	}
	fd := file.Decls[0].(*ast.FuncDecl)
	receiver := ""
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		receiver = exprString(fd.Recv.List[0].Type)
	}
	var params []param
	for _, field := range fd.Type.Params.List {
		typeText := exprString(field.Type)
		for _, name := range field.Names {
			params = append(params, syntacticParam(name.Name, typeText))
		}
	}
	return fd.Name.Name, receiver, params
}

func typedParam(name string, t types.Type) param {
	switch {
	case isComponent(t):
		return param{name: name, kind: "component"}
	case isSlice(t) && isComponent(t.Underlying().(*types.Slice).Elem()):
		return param{name: name, kind: "components"}
	case isContext(t):
		return param{name: name, kind: "context"}
	case isNamed(t, templPath, "Attributes") || isNamed(t, templPath, "Attributer"):
		return param{name: name, kind: "attributes"}
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		if _, named := t.(*types.Named); named {
			return param{name: name, kind: "named-scalar"}
		}
		return param{name: name, kind: "scalar"}
	case *types.Pointer:
		if basic, ok := u.Elem().(*types.Basic); ok && basic.Info()&types.IsConstType != 0 {
			return param{name: name, kind: "optional-scalar"}
		}
	case *types.Signature:
		return param{name: name, kind: "func"}
	}
	return param{name: name, kind: "record", records: recordTypes(t, nil)}
}

func syntacticParam(name, typeText string) param {
	switch strings.TrimPrefix(typeText, "...") {
	case "templ.Component":
		return param{name: name, kind: "component"}
	case "[]templ.Component":
		return param{name: name, kind: "components"}
	case "context.Context":
		return param{name: name, kind: "context"}
	case "templ.Attributes", "templ.Attributer":
		return param{name: name, kind: "attributes"}
	case "string", "bool", "int", "int64", "int32", "float64", "float32", "uint", "uint64":
		return param{name: name, kind: "scalar"}
	case "*string", "*bool", "*int", "*int64", "*float64":
		return param{name: name, kind: "optional-scalar"}
	}
	if strings.HasPrefix(typeText, "func(") {
		return param{name: name, kind: "func"}
	}
	return param{name: name, kind: "record", records: []string{strings.TrimLeft(typeText, "*[]")}}
}

// recordTypes lists the named struct types reachable through pointers,
// slices, arrays, and maps of t.
func recordTypes(t types.Type, records []string) []string {
	switch u := t.(type) {
	case *types.Pointer:
		return recordTypes(u.Elem(), records)
	case *types.Slice:
		return recordTypes(u.Elem(), records)
	case *types.Array:
		return recordTypes(u.Elem(), records)
	case *types.Map:
		return recordTypes(u.Elem(), records)
	case *types.Named, *types.Alias:
		name := typeName(t)
		if _, ok := t.Underlying().(*types.Struct); ok || types.IsInterface(t) {
			return appendUnique(records, name)
		}
		return recordTypes(t.Underlying(), records)
	}
	return records
}

func typeName(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}

func isNamed(t types.Type, pkgPath, name string) bool {
	t = types.Unalias(t)
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Pkg().Path() == pkgPath && named.Obj().Name() == name
}

const templPath = "github.com/a-h/templ"

func isComponent(t types.Type) bool { return t != nil && isNamed(t, templPath, "Component") }
func isSafeURL(t types.Type) bool   { return t != nil && isNamed(t, templPath, "SafeURL") }
func isContext(t types.Type) bool   { return t != nil && isNamed(t, "context", "Context") }

func isSlice(t types.Type) bool {
	_, ok := t.Underlying().(*types.Slice)
	return ok
}

// scopes track locals syntactically, for classification without types.

func (w *walker) declare(name, kind string) {
	if name == "" || name == "_" {
		return
	}
	w.scopes[len(w.scopes)-1][name] = kind
}

func (w *walker) lookup(name string) (string, bool) {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if kind, ok := w.scopes[i][name]; ok {
			return kind, true
		}
	}
	return "", false
}

func (w *walker) push() { w.scopes = append(w.scopes, map[string]string{}) }
func (w *walker) pop()  { w.scopes = w.scopes[:len(w.scopes)-1] }

func (w *walker) nodesOf(nodes []parser.Node, element string) {
	for _, node := range nodes {
		w.node(node, element)
	}
}

func (w *walker) node(node parser.Node, element string) {
	switch n := node.(type) {
	case *parser.Text:
		reasons := []string(nil)
		if strings.Contains(n.Value, "&") {
			reasons = []string{"html-entity-decoded"}
		}
		w.add("Text", "text", Converted, reasons, "", n.Range, n.Value)
	case *parser.Whitespace:
		w.add("Whitespace", "whitespace", Converted, nil, "", n.Range, "")
	case *parser.DocType:
		if strings.EqualFold(strings.TrimSpace(n.Value), "html") {
			w.add("DocType", "doctype", Converted, nil, "", n.Range, n.Value)
		} else {
			w.add("DocType", "doctype", Blocked, []string{"non-html-doctype"}, "non-html-doctype", n.Range, n.Value)
		}
	case *parser.HTMLComment:
		if problem := htmlCommentProblem(n.Contents); problem != "" {
			w.add("HTMLComment", "html-comment", ReviewRequired, []string{problem}, "", n.Range, n.Contents)
		} else {
			w.add("HTMLComment", "html-comment", Converted, nil, "", n.Range, n.Contents)
		}
	case *parser.GoComment:
		w.add("GoComment", "source-comment", Converted, nil, "", n.Range, n.Contents)
	case *parser.ChildrenExpression:
		w.add("ChildrenExpression", "children-outlet", Converted, nil, "", n.Range, "children...")
	case *parser.Fallthrough:
		w.add("Fallthrough", "switch-fallthrough", PresentationScaffolded, []string{"closed-presentation-state"}, "", n.Range, "fallthrough")
	case *parser.Element:
		w.element(n)
	case *parser.ScriptElement:
		w.attributes(n.Attributes, "script")
		w.scriptElement(n)
	case *parser.RawElement:
		w.attributes(n.Attributes, n.Name)
		switch {
		case strings.TrimSpace(n.Contents) == "":
			w.add("RawElement", "raw-text-element", Converted, nil, "", n.Range, "<"+n.Name+">")
		case strings.Contains(n.Contents, "```"):
			// A raw-text literal cannot contain three backticks.
			w.add("RawElement", "raw-text-element", ReviewRequired, []string{"raw-text-fence"}, "", n.Range, "<"+n.Name+">")
		default:
			w.add("RawElement", "raw-text-element", Converted, []string{"raw-text-literal"}, "", n.Range, "<"+n.Name+">")
		}
	case *parser.StringExpression:
		status, reasons := w.value(n.Expression, contextText)
		w.addExpr("StringExpression", "text-interpolation", status, reasons, n.Range, n.Expression)
	case *parser.GoCode:
		w.goCode(n)
	case *parser.IfExpression:
		w.ifExpression(n, element)
	case *parser.SwitchExpression:
		w.switchExpression(n, element)
	case *parser.ForExpression:
		w.forExpression(n, element)
	case *parser.CallTemplateExpression:
		w.call("CallTemplateExpression", n.Expression, n.Range, false)
	case *parser.TemplElementExpression:
		w.call("TemplElementExpression", n.Expression, n.Range, len(n.Children) > 0)
		w.nodesOf(n.Children, element)
	default:
		w.drop(fmt.Sprintf("%T", node), parser.Range{})
	}
}

var elementNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func (w *walker) element(n *parser.Element) {
	if elementNamePattern.MatchString(n.Name) {
		w.add("Element", "element", Converted, nil, "", n.Range, "<"+n.Name+">")
	} else {
		w.add("Element", "element", Blocked, []string{"element-name"}, "element-names", n.Range, "<"+n.Name+">")
	}
	w.attributes(n.Attributes, n.Name)
	w.nodesOf(n.Children, n.Name)
}

func (w *walker) scriptElement(n *parser.ScriptElement) {
	interpolated := false
	static := false
	fence := false
	for _, content := range n.Contents {
		if content.GoCode != nil {
			interpolated = true
		}
		if content.Value != nil && strings.TrimSpace(*content.Value) != "" {
			static = true
			fence = fence || strings.Contains(*content.Value, "```")
		}
	}
	switch {
	case interpolated:
		// templ JS-encodes Go values in a script; KTML takes presenter-built
		// JSONValue (or TrustedScript) paths in expression positions.
		w.add("ScriptElement", "inline-script", PresentationScaffolded, []string{"script-body-value", "security-review-marker"}, "", n.Range, "<script>")
	case fence:
		w.add("ScriptElement", "inline-script", ReviewRequired, []string{"raw-text-fence"}, "", n.Range, "<script>")
	case static:
		w.add("ScriptElement", "inline-script", Converted, []string{"raw-text-literal"}, "", n.Range, "<script>")
	default:
		w.add("ScriptElement", "script-element", Converted, nil, "", n.Range, "<script>")
	}
}

func (w *walker) goCode(n *parser.GoCode) {
	w.add("GoCode", "local-initialization", PresentationScaffolded, []string{"local-initialization"}, "", n.Range, n.Expression.Value)
	if w.need != nil {
		w.need.Derived++
	}
	file, err := goparser.ParseFile(token.NewFileSet(), "", "package p\nfunc _() {\n"+n.Expression.Value+"\n}", 0)
	if err != nil {
		return
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch s := node.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE {
				for _, lhs := range s.Lhs {
					if ident, ok := lhs.(*ast.Ident); ok {
						w.declare(ident.Name, "local")
					}
				}
			}
		case *ast.ValueSpec:
			for _, ident := range s.Names {
				w.declare(ident.Name, "local")
			}
		case *ast.FuncLit:
			return false
		}
		return true
	})
}

func (w *walker) ifExpression(n *parser.IfExpression, element string) {
	status, reasons := w.condition(n.Expression)
	w.addExpr("IfExpression", "condition", status, reasons, n.Range, n.Expression)
	w.push()
	w.nodesOf(n.Then, element)
	w.pop()
	for _, elseIf := range n.ElseIfs {
		status, reasons := w.condition(elseIf.Expression)
		w.addExpr("ElseIfExpression", "condition", status, reasons, elseIf.Range, elseIf.Expression)
		w.push()
		w.nodesOf(elseIf.Then, element)
		w.pop()
	}
	w.push()
	w.nodesOf(n.Else, element)
	w.pop()
}

func (w *walker) switchExpression(n *parser.SwitchExpression, element string) {
	w.addExpr("SwitchExpression", "switch", PresentationScaffolded, []string{"closed-presentation-state"}, n.Range, n.Expression)
	if w.need != nil {
		w.need.States++
	}
	for _, c := range n.Cases {
		construct := "switch-case"
		if strings.HasPrefix(strings.TrimSpace(c.Expression.Value), "default") {
			construct = "switch-default"
		}
		w.add("CaseExpression", construct, PresentationScaffolded, []string{"state-variant"}, "", c.Expression.Range, c.Expression.Value)
		w.push()
		w.nodesOf(c.Children, element)
		w.pop()
	}
}

func (w *walker) forExpression(n *parser.ForExpression, element string) {
	status, reasons, names := w.loop(n.Expression)
	w.addExpr("ForExpression", "loop", status, reasons, n.Range, n.Expression)
	w.push()
	for _, name := range names {
		w.declare(name, "loop")
	}
	w.nodesOf(n.Children, element)
	w.pop()
}

func (w *walker) attributes(attributes []parser.Attribute, element string) {
	for _, attribute := range attributes {
		w.attribute(attribute, element)
	}
}

var entityPattern = regexp.MustCompile(`&(#[0-9]+|#[xX][0-9a-fA-F]+|[A-Za-z][A-Za-z0-9]*);`)

func (w *walker) attribute(attribute parser.Attribute, element string) {
	switch a := attribute.(type) {
	case *parser.ConstantAttribute:
		name, ok := constantKey(a.Key)
		switch {
		case !ok:
			w.add("ConstantAttribute", "dynamic-attribute-name", Blocked, []string{"dynamic-attribute-name"}, "dynamic-attribute-names", a.Range, a.Key.String())
		case !validAttributeName(name):
			w.add("ConstantAttribute", "literal-attribute", Blocked, []string{"attribute-name"}, "attribute-names", a.Range, name)
		case entityPattern.MatchString(a.Value):
			w.add("ConstantAttribute", "literal-attribute", ReviewRequired, []string{"literal-entity-escaping"}, "", a.Range, name+"="+a.Value)
		default:
			w.add("ConstantAttribute", "literal-attribute", Converted, nil, "", a.Range, name)
		}
	case *parser.BoolConstantAttribute:
		name, ok := constantKey(a.Key)
		switch {
		case !ok:
			w.add("BoolConstantAttribute", "dynamic-attribute-name", Blocked, []string{"dynamic-attribute-name"}, "dynamic-attribute-names", a.Range, a.Key.String())
		case !validAttributeName(name):
			w.add("BoolConstantAttribute", "valueless-attribute", Blocked, []string{"attribute-name"}, "attribute-names", a.Range, name)
		default:
			w.add("BoolConstantAttribute", "valueless-attribute", Converted, nil, "", a.Range, name)
		}
	case *parser.BoolExpressionAttribute:
		if _, ok := constantKey(a.Key); !ok {
			w.add("BoolExpressionAttribute", "dynamic-attribute-name", Blocked, []string{"dynamic-attribute-name"}, "dynamic-attribute-names", a.Range, a.Key.String())
			return
		}
		status, reasons := w.booleanAttribute(a.Expression)
		w.addExpr("BoolExpressionAttribute", "dynamic-boolean-attribute", status, reasons, a.Range, a.Expression)
	case *parser.ExpressionAttribute:
		name, ok := constantKey(a.Key)
		if !ok {
			w.add("ExpressionAttribute", "dynamic-attribute-name", Blocked, []string{"dynamic-attribute-name"}, "dynamic-attribute-names", a.Range, a.Key.String())
			return
		}
		construct, status, reasons, gap := w.expressionAttribute(element, name, a.Expression)
		w.addGap("ExpressionAttribute", construct, status, reasons, gap, a.Range, name+"={ "+a.Expression.Value+" }")
	case *parser.SpreadAttributes:
		status, reasons := w.spread(a.Expression)
		w.addExpr("SpreadAttributes", "attribute-spread", status, reasons, a.Range, a.Expression)
	case *parser.ConditionalAttribute:
		w.conditionalAttribute(a, element)
	default:
		w.drop(fmt.Sprintf("%T", attribute), parser.Range{})
	}
}

func (w *walker) conditionalAttribute(a *parser.ConditionalAttribute, element string) {
	onlyBoolean := len(a.Else) == 0 && len(a.Then) > 0
	for _, attribute := range a.Then {
		if _, ok := attribute.(*parser.BoolConstantAttribute); !ok {
			onlyBoolean = false
		}
	}
	switch {
	case !onlyBoolean:
		// if c { name="v" } becomes a conditional attribute group in a
		// parenthesized attribute list (migration-language-v1).
		status, reasons := w.condition(a.Expression)
		w.addExpr("ConditionalAttribute", "conditional-attributes", status, reasons, a.Range, a.Expression)
	default:
		status, reasons := w.booleanAttribute(a.Expression)
		w.addExpr("ConditionalAttribute", "conditional-boolean-attribute", status, reasons, a.Range, a.Expression)
	}
	w.attributes(a.Then, element)
	w.attributes(a.Else, element)
}

func constantKey(key parser.AttributeKey) (string, bool) {
	constant, ok := key.(parser.ConstantAttributeKey)
	if ok {
		return constant.Name, true
	}
	if pointer, ok := key.(*parser.ConstantAttributeKey); ok {
		return pointer.Name, true
	}
	return "", false
}

// validAttributeName applies the KTML attribute-name rule
// (attribute-names-doctype-comments-v1).
func validAttributeName(name string) bool {
	return name != "" && scanner.AttributeNameEnd([]byte(name), 0) == len(name)
}

// htmlCommentProblem applies KTML's HTML comment text rules.
func htmlCommentProblem(text string) string {
	switch {
	case strings.HasPrefix(text, ">") || strings.HasPrefix(text, "->"):
		return "html-comment-text"
	case strings.Contains(text, "<!--") || strings.Contains(text, "--!>") || strings.Contains(text, "-->"):
		return "html-comment-text"
	case strings.HasSuffix(text, "<!-"):
		return "html-comment-text"
	}
	return ""
}

func (w *walker) add(kind, construct string, status Status, reasons []string, gap string, r parser.Range, code string) {
	w.addGap(kind, construct, status, reasons, gap, r, code)
}

func (w *walker) addGap(kind, construct string, status Status, reasons []string, gap string, r parser.Range, code string) {
	if status != Blocked {
		gap = ""
	}
	if len(code) > 160 {
		code = code[:157] + "..."
	}
	w.nodes = append(w.nodes, Node{
		File:      w.file.rel,
		Template:  w.template,
		Kind:      kind,
		Construct: construct,
		Status:    status,
		Reasons:   reasons,
		Gap:       gap,
		From:      Position{Line: int(r.From.Line) + 1, Col: int(r.From.Col) + 1},
		To:        Position{Line: int(r.To.Line) + 1, Col: int(r.To.Col) + 1},
		Code:      code,
		Typed:     w.typed != nil,
	})
	if status == PresentationScaffolded && w.need != nil && kind != "HTMLTemplate" && kind != "GoCode" && kind != "SwitchExpression" && kind != "CaseExpression" {
		w.need.Derived++
	}
}

// addExpr adds a node for a construct carrying one Go expression, taking the
// gap from a "gap:" reason when the construct is blocked.
func (w *walker) addExpr(kind, construct string, status Status, reasons []string, r parser.Range, e parser.Expression) {
	gap := ""
	var kept []string
	for _, reason := range reasons {
		if g, ok := strings.CutPrefix(reason, "gap:"); ok {
			gap = g
			continue
		}
		kept = append(kept, reason)
	}
	if r == (parser.Range{}) {
		r = e.Range
	}
	w.addGap(kind, construct, status, kept, gap, r, e.Value)
}

func (w *walker) drop(kind string, r parser.Range) {
	w.dropped++
	w.add(kind, "unknown", Blocked, []string{"unclassified-node"}, "unclassified", r, kind)
}

func appendUnique(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}

func exprString(expr ast.Expr) string {
	var b strings.Builder
	writeExpr(&b, expr)
	return b.String()
}

func writeExpr(b *strings.Builder, expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.Ident:
		b.WriteString(e.Name)
	case *ast.SelectorExpr:
		writeExpr(b, e.X)
		b.WriteString(".")
		b.WriteString(e.Sel.Name)
	case *ast.StarExpr:
		b.WriteString("*")
		writeExpr(b, e.X)
	case *ast.ArrayType:
		b.WriteString("[]")
		writeExpr(b, e.Elt)
	case *ast.Ellipsis:
		b.WriteString("...")
		writeExpr(b, e.Elt)
	case *ast.MapType:
		b.WriteString("map[")
		writeExpr(b, e.Key)
		b.WriteString("]")
		writeExpr(b, e.Value)
	case *ast.FuncType:
		b.WriteString("func(...)")
	case *ast.IndexExpr:
		writeExpr(b, e.X)
		b.WriteString("[")
		writeExpr(b, e.Index)
		b.WriteString("]")
	default:
		fmt.Fprintf(b, "%T", expr)
	}
}

// spread classifies { attrs... }: a path of a templ.Attributes input or
// field becomes ...path; anything Go computes becomes a presenter-built
// Attributes value.
func (w *walker) spread(e parser.Expression) (Status, []string) {
	expr, typed := w.goExpr(e)
	if expr == nil {
		return ReviewRequired, []string{"unparsed-go-expression"}
	}
	if w.localPath(expr, typed) {
		return Converted, []string{"attribute-spread"}
	}
	return PresentationScaffolded, []string{"derived-attributes"}
}
