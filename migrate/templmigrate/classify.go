package templmigrate

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	parser "github.com/a-h/templ/parser/v2"
	"github.com/karl-tye/ktml/internal/compiler"
)

type valueContext int

const (
	contextText valueContext = iota
	contextAttribute
	contextArgument
)

// goExpr returns the Go AST for a templ expression: the typed generated
// node when types are loaded, otherwise a syntactic parse. typed reports
// which. A nil expression did not parse.
func (w *walker) goExpr(e parser.Expression) (ast.Expr, bool) {
	if w.typed != nil {
		if expr := w.typed.expr(e); expr != nil {
			return expr, true
		}
	}
	expr, err := goparser.ParseExpr(e.Value)
	if err != nil {
		return nil, false
	}
	return expr, false
}

func (w *walker) typeOf(expr ast.Expr, typed bool) types.Type {
	if !typed {
		return nil
	}
	return w.typed.info.TypeOf(expr)
}

func (w *walker) object(ident *ast.Ident, typed bool) types.Object {
	if !typed {
		return nil
	}
	if obj := w.typed.info.Uses[ident]; obj != nil {
		return obj
	}
	return w.typed.info.Defs[ident]
}

// localPath reports whether expr is a field path rooted in a template input,
// loop binding, or local: the only value shape KTML templates accept.
func (w *walker) localPath(expr ast.Expr, typed bool) bool {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return w.localPath(e.X, typed)
	case *ast.Ident:
		if typed {
			v, ok := w.object(e, typed).(*types.Var)
			return ok && !v.IsField() && v.Pkg() != nil && v.Parent() != v.Pkg().Scope()
		}
		_, ok := w.lookup(e.Name)
		return ok
	case *ast.SelectorExpr:
		if !w.localPath(e.X, typed) {
			return false
		}
		if typed {
			selection := w.typed.info.Selections[e]
			return selection != nil && selection.Kind() == types.FieldVal
		}
		return true
	}
	return false
}

// packageConstant reports whether expr names a Go constant, which a
// converter can inline as a KTML literal.
func (w *walker) packageConstant(expr ast.Expr, typed bool) bool {
	if !typed {
		return false
	}
	var ident *ast.Ident
	switch e := expr.(type) {
	case *ast.Ident:
		ident = e
	case *ast.SelectorExpr:
		ident = e.Sel
	default:
		return false
	}
	_, ok := w.object(ident, typed).(*types.Const)
	return ok
}

// callee names the function a call invokes as "path.Name" for package
// functions, ".Name" for methods, and "" for anything else.
func (w *walker) callee(call *ast.CallExpr, typed bool) (string, types.Object) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if obj := w.object(fun, typed); obj != nil {
			if fn, ok := obj.(*types.Func); ok && fn.Pkg() != nil {
				return fn.Pkg().Path() + "." + fn.Name(), fn
			}
			if name, ok := obj.(*types.TypeName); ok && name.Pkg() != nil {
				return name.Pkg().Path() + "." + name.Name(), name
			}
			return "", obj
		}
		return "." + fun.Name, nil
	case *ast.SelectorExpr:
		if obj := w.object(fun.Sel, typed); obj != nil {
			if fn, ok := obj.(*types.Func); ok && fn.Pkg() != nil {
				if fn.Type().(*types.Signature).Recv() != nil {
					return "." + fn.Name(), fn
				}
				return fn.Pkg().Path() + "." + fn.Name(), fn
			}
			if name, ok := obj.(*types.TypeName); ok && name.Pkg() != nil {
				return name.Pkg().Path() + "." + name.Name(), name
			}
			return "", obj
		}
		if x, ok := fun.X.(*ast.Ident); ok {
			if path, ok := w.imports[x.Name]; ok {
				if _, local := w.lookup(x.Name); !local {
					return path + "." + fun.Sel.Name, nil
				}
			}
		}
		return "." + fun.Sel.Name, nil
	}
	return "", nil
}

func (w *walker) isConversion(call *ast.CallExpr, typed bool) bool {
	if !typed {
		return false
	}
	tv, ok := w.typed.info.Types[call.Fun]
	return ok && tv.IsType()
}

// value classifies an output value: text, an ordinary attribute value, or a
// component argument.
func (w *walker) value(e parser.Expression, context valueContext) (Status, []string) {
	expr, typed := w.goExpr(e)
	if expr == nil {
		return ReviewRequired, []string{"unparsed-go-expression"}
	}
	return w.valueExpr(expr, typed, context)
}

func (w *walker) valueExpr(expr ast.Expr, typed bool, context valueContext) (Status, []string) {
	if paren, ok := expr.(*ast.ParenExpr); ok {
		return w.valueExpr(paren.X, typed, context)
	}
	if _, ok := expr.(*ast.BasicLit); ok {
		return Converted, []string{"literal"}
	}
	t := w.typeOf(expr, typed)
	if context == contextArgument {
		switch {
		case isComponent(t):
			return ReviewRequired, []string{"component-valued-argument"}
		case t != nil && isSignature(t):
			return ReviewRequired, []string{"function-valued-argument"}
		}
	}
	if w.localPath(expr, typed) {
		if !typed {
			return Converted, []string{"field-path", "types-unavailable"}
		}
		if context == contextAttribute {
			switch {
			case isBool(t):
				return PresentationScaffolded, []string{"boolean-attribute-value"}
			case isSafeURL(t):
				return ReviewRequired, []string{"trusted-url-value"}
			case !isScalar(t):
				return PresentationScaffolded, []string{"stringified-attribute-value"}
			}
		}
		if context == contextArgument && !isScalar(t) && !isOptionalScalar(t) {
			return Converted, []string{"record-argument"}
		}
		return Converted, []string{"field-path"}
	}
	if w.packageConstant(expr, typed) {
		return Converted, []string{"inlined-go-constant"}
	}
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Name == "true" || e.Name == "false" {
			return Converted, []string{"literal"}
		}
		if e.Name == "nil" && context == contextArgument {
			return Converted, []string{"absent-optional-argument"}
		}
		return PresentationScaffolded, []string{"go-package-value"}
	case *ast.SelectorExpr:
		return PresentationScaffolded, []string{"go-package-value"}
	case *ast.CallExpr:
		return w.callValue(e, typed)
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return PresentationScaffolded, []string{"concatenation"}
		}
		return PresentationScaffolded, []string{"derived-value"}
	case *ast.CompositeLit:
		return PresentationScaffolded, []string{"composite-literal"}
	case *ast.FuncLit:
		return ReviewRequired, []string{"function-literal"}
	case *ast.IndexExpr, *ast.IndexListExpr, *ast.SliceExpr:
		return PresentationScaffolded, []string{"indexing"}
	case *ast.StarExpr:
		return PresentationScaffolded, []string{"pointer-dereference"}
	}
	return PresentationScaffolded, []string{"derived-value"}
}

func (w *walker) callValue(call *ast.CallExpr, typed bool) (Status, []string) {
	if _, ok := call.Fun.(*ast.FuncLit); ok {
		return ReviewRequired, []string{"immediately-invoked-function"}
	}
	name, _ := w.callee(call, typed)
	switch name {
	case templPath + ".SafeURL":
		return ReviewRequired, []string{"trusted-url"}
	case templPath + ".URL":
		return PresentationScaffolded, []string{"url-construction"}
	case templPath + ".JSONString":
		return PresentationScaffolded, []string{"json-value"}
	case templPath + ".KV", templPath + ".Classes", templPath + ".Class", templPath + ".SafeClass":
		return PresentationScaffolded, []string{"computed-classes"}
	case templPath + ".SafeCSS", templPath + ".SanitizeCSS", templPath + ".SafeCSSProperty":
		return ReviewRequired, []string{"trusted-css"}
	case templPath + ".Raw":
		return Blocked, []string{"gap:trusted-html", "trusted-html"}
	case "fmt.Sprintf", "fmt.Sprint", "fmt.Sprintln":
		return PresentationScaffolded, []string{"formatting"}
	}
	if w.isConversion(call, typed) {
		return PresentationScaffolded, []string{"type-conversion"}
	}
	switch {
	case strings.HasPrefix(name, "strconv."):
		return PresentationScaffolded, []string{"formatting"}
	case strings.HasPrefix(name, "."):
		return PresentationScaffolded, []string{"method-call"}
	}
	return PresentationScaffolded, []string{"function-call"}
}

// condition classifies an if or else-if condition.
func (w *walker) condition(e parser.Expression) (Status, []string) {
	expr, typed := w.goExpr(e)
	if expr == nil {
		if hasInitializer(e.Value) {
			return PresentationScaffolded, []string{"if-initializer"}
		}
		return ReviewRequired, []string{"unparsed-go-expression"}
	}
	return w.conditionExpr(expr, typed)
}

func (w *walker) conditionExpr(expr ast.Expr, typed bool) (Status, []string) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return w.conditionExpr(e.X, typed)
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			return w.conditionExpr(e.X, typed)
		}
	case *ast.BinaryExpr:
		switch e.Op {
		case token.LAND, token.LOR:
			left, leftReasons := w.conditionExpr(e.X, typed)
			right, rightReasons := w.conditionExpr(e.Y, typed)
			return maxStatus(left, right), mergeReasons(leftReasons, rightReasons)
		case token.EQL, token.NEQ:
			operand, other := e.X, e.Y
			if isNil(operand) {
				operand, other = other, operand
			}
			if isNil(other) && w.localPath(operand, typed) {
				t := w.typeOf(operand, typed)
				if t == nil {
					return Converted, []string{"presence", "types-unavailable"}
				}
				if _, ok := t.Underlying().(*types.Pointer); ok {
					return Converted, []string{"presence"}
				}
				return PresentationScaffolded, []string{"nil-check"}
			}
			return PresentationScaffolded, []string{"comparison"}
		default:
			return PresentationScaffolded, []string{"comparison"}
		}
	}
	if w.localPath(expr, typed) {
		return Converted, []string{"boolean-path"}
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "len" {
			return PresentationScaffolded, []string{"collection-length"}
		}
		if _, ok := call.Fun.(*ast.FuncLit); ok {
			return ReviewRequired, []string{"immediately-invoked-function"}
		}
		return PresentationScaffolded, []string{"derived-condition"}
	}
	if w.packageConstant(expr, typed) {
		return PresentationScaffolded, []string{"go-package-value"}
	}
	return PresentationScaffolded, []string{"derived-condition"}
}

// booleanAttribute classifies a templ boolean attribute condition. KTML
// accepts only an exact boolean path there.
func (w *walker) booleanAttribute(e parser.Expression) (Status, []string) {
	expr, typed := w.goExpr(e)
	if expr == nil {
		return ReviewRequired, []string{"unparsed-go-expression"}
	}
	if paren, ok := expr.(*ast.ParenExpr); ok {
		expr = paren.X
	}
	if w.localPath(expr, typed) {
		return Converted, []string{"boolean-path"}
	}
	status, reasons := w.conditionExpr(expr, typed)
	kept := []string{"derived-boolean"}
	for _, reason := range reasons {
		if reason != "boolean-path" && reason != "presence" {
			kept = appendUnique(kept, reason)
		}
	}
	return maxStatus(status, PresentationScaffolded), kept
}

// templURLAttribute reports the attributes templ sanitizes as URLs.
func templURLAttribute(element, name string) bool {
	switch element {
	case "a", "link":
		return name == "href"
	case "form":
		return name == "action"
	case "object":
		return name == "data"
	}
	return false
}

func (w *walker) expressionAttribute(element, name string, e parser.Expression) (construct string, status Status, reasons []string, gap string) {
	lower := strings.ToLower(name)
	expr, typed := w.goExpr(e)
	if expr == nil {
		elements, typed := w.listElements(e)
		switch {
		case elements == nil:
			return "attribute-interpolation", ReviewRequired, []string{"unparsed-go-expression"}, ""
		case lower == "style":
			return "style-attribute", PresentationScaffolded, []string{"css-value"}, ""
		case lower == "class":
			for _, element := range elements {
				if _, ok := element.(*ast.BasicLit); ok {
					continue
				}
				if w.packageConstant(element, typed) || w.localPath(element, typed) && (!typed || isString(w.typeOf(element, typed))) {
					continue
				}
				return "class-list", PresentationScaffolded, []string{"computed-classes"}, ""
			}
			return "class-list", Converted, []string{"class-list-interpolation"}, ""
		}
		return "attribute-list", PresentationScaffolded, []string{"derived-value"}, ""
	}
	if paren, ok := expr.(*ast.ParenExpr); ok {
		expr = paren.X
	}
	switch {
	case lower == "style":
		if _, ok := expr.(*ast.BasicLit); ok {
			return "style-attribute", Converted, []string{"literal"}, ""
		}
		if call, ok := expr.(*ast.CallExpr); ok {
			if name, _ := w.callee(call, typed); name == templPath+".SafeCSS" {
				return "style-attribute", ReviewRequired, []string{"trusted-css"}, ""
			}
		}
		// The presenter builds the declarations with ktml.CSS.
		return "style-attribute", PresentationScaffolded, []string{"css-value"}, ""
	case compiler.ScriptAttribute(name):
		if _, ok := expr.(*ast.BasicLit); ok {
			return "script-attribute", Converted, []string{"literal"}, ""
		}
		t := w.typeOf(expr, typed)
		if w.localPath(expr, typed) && isBool(t) {
			return "script-attribute", Converted, []string{"script-boolean"}, ""
		}
		if call, ok := expr.(*ast.CallExpr); ok {
			if name, _ := w.callee(call, typed); name == templPath+".JSONString" {
				return "script-attribute", PresentationScaffolded, []string{"json-value", "security-review-marker"}, ""
			}
		}
		if t != nil && isNamed(t, templPath, "ComponentScript") {
			return "script-attribute", Blocked, []string{"script-template"}, "script-templates"
		}
		return "script-attribute", PresentationScaffolded, []string{"script-attribute-value", "security-review-marker"}, ""
	case templURLAttribute(element, lower):
		if call, ok := expr.(*ast.CallExpr); ok {
			name, _ := w.callee(call, typed)
			switch name {
			case templPath + ".SafeURL":
				return "url-attribute", ReviewRequired, []string{"trusted-url"}, ""
			case templPath + ".URL":
				return "url-attribute", PresentationScaffolded, []string{"url-construction"}, ""
			}
			return "url-attribute", PresentationScaffolded, []string{"url-construction"}, ""
		}
		if _, ok := expr.(*ast.BasicLit); ok {
			return "url-attribute", PresentationScaffolded, []string{"url-literal-policy"}, ""
		}
		if isSafeURL(w.typeOf(expr, typed)) {
			return "url-attribute", ReviewRequired, []string{"trusted-url-value"}, ""
		}
		return "url-attribute", PresentationScaffolded, []string{"url-policy"}, ""
	case compiler.URLAttribute(name) && !isBasicLit(expr) && !compiler.SafeURLText(goLeadingLiteral(expr)):
		// templ writes this URL unsanitized; the presenter builds a
		// ktml.URL, which the URL policy checks.
		return "url-attribute", PresentationScaffolded, []string{"url-policy"}, ""
	case lower == "class":
		if _, ok := expr.(*ast.BasicLit); ok {
			return "class-attribute", Converted, []string{"literal"}, ""
		}
		if w.localPath(expr, typed) && (!typed || isString(w.typeOf(expr, typed))) {
			return "class-attribute", Converted, []string{"field-path"}, ""
		}
		if w.packageConstant(expr, typed) {
			return "class-attribute", Converted, []string{"inlined-go-constant"}, ""
		}
		return "class-attribute", PresentationScaffolded, []string{"computed-classes"}, ""
	}
	status, reasons = w.valueExpr(expr, typed, contextAttribute)
	if status == Blocked {
		for _, reason := range reasons {
			if g, ok := strings.CutPrefix(reason, "gap:"); ok {
				gap = g
			}
		}
		reasons = withoutGap(reasons)
	}
	return "attribute-interpolation", status, reasons, gap
}

// loop classifies a templ for statement and returns the names it binds.
func (w *walker) loop(e parser.Expression) (Status, []string, []string) {
	var rangeStmt *ast.RangeStmt
	typed := false
	if w.typed != nil {
		if stmt, ok := stmtContaining[*ast.RangeStmt](w.typed, e); ok {
			rangeStmt, typed = stmt, true
		} else if _, ok := stmtContaining[*ast.ForStmt](w.typed, e); ok {
			return PresentationScaffolded, []string{"counted-loop"}, forNames(e.Value)
		}
	}
	if rangeStmt == nil {
		file, err := goparser.ParseFile(token.NewFileSet(), "", "package p\nfunc _() {\nfor "+e.Value+" {}\n}", 0)
		if err != nil {
			return ReviewRequired, []string{"unparsed-go-expression"}, nil
		}
		body := file.Decls[0].(*ast.FuncDecl).Body.List
		stmt, ok := body[0].(*ast.RangeStmt)
		if !ok {
			return PresentationScaffolded, []string{"counted-loop"}, forNames(e.Value)
		}
		rangeStmt = stmt
	}
	var names []string
	keyName, valueName := "", ""
	if ident, ok := rangeStmt.Key.(*ast.Ident); ok {
		keyName = ident.Name
		names = append(names, ident.Name)
	}
	if ident, ok := rangeStmt.Value.(*ast.Ident); ok {
		valueName = ident.Name
		names = append(names, ident.Name)
	}
	if !w.localPath(rangeStmt.X, typed) {
		if w.packageConstant(rangeStmt.X, typed) {
			return PresentationScaffolded, []string{"range-over-integer"}, names
		}
		return PresentationScaffolded, []string{"derived-collection"}, names
	}
	if !typed {
		if keyName != "" && keyName != "_" {
			return PresentationScaffolded, []string{"loop-index"}, names
		}
		return ReviewRequired, []string{"unkeyed-loop", "types-unavailable"}, names
	}
	collection := w.typed.info.TypeOf(rangeStmt.X)
	var element types.Type
	switch u := collection.Underlying().(type) {
	case *types.Slice:
		element = u.Elem()
	case *types.Array:
		element = u.Elem()
	case *types.Pointer:
		if array, ok := u.Elem().Underlying().(*types.Array); ok {
			element = array.Elem()
		}
	case *types.Map:
		return PresentationScaffolded, []string{"map-iteration"}, names
	case *types.Basic:
		return PresentationScaffolded, []string{"range-over-integer"}, names
	case *types.Signature:
		return PresentationScaffolded, []string{"iterator"}, names
	}
	if element == nil {
		return PresentationScaffolded, []string{"derived-collection"}, names
	}
	if keyName != "" && keyName != "_" && w.used(rangeStmt.Key.(*ast.Ident)) {
		return PresentationScaffolded, []string{"loop-index"}, names
	}
	if valueName == "" {
		return PresentationScaffolded, []string{"loop-index"}, names
	}
	if isScalar(element) {
		return ReviewRequired, []string{"scalar-item-key-uniqueness"}, names
	}
	if isComponent(element) {
		return ReviewRequired, []string{"component-collection"}, names
	}
	field, kind := keyField(element)
	switch kind {
	case "scalar":
		return Converted, []string{"key:" + field}, names
	case "needs-string":
		return PresentationScaffolded, []string{"key-needs-presenter-string:" + field}, names
	}
	return ReviewRequired, []string{"unkeyed-loop"}, names
}

// keyFields are the field names accepted as a stable item identity, in
// preference order.
var keyFields = []string{"ID", "Id", "Key", "UUID", "Uuid", "Slug", "Code", "Reference", "Ref"}

func keyField(element types.Type) (string, string) {
	candidates := keyFields
	if named, ok := types.Unalias(derefType(element)).(*types.Named); ok {
		candidates = append(candidates[:len(candidates):len(candidates)], named.Obj().Name()+"ID", named.Obj().Name()+"Id")
	}
	for _, name := range candidates {
		obj, _, _ := types.LookupFieldOrMethod(element, true, nil, name)
		v, ok := obj.(*types.Var)
		if !ok || !v.IsField() {
			continue
		}
		if isScalar(v.Type()) {
			if basic, ok := v.Type().Underlying().(*types.Basic); ok && basic.Info()&(types.IsString|types.IsInteger) != 0 {
				return name, "scalar"
			}
		}
		return name, "needs-string"
	}
	return "", ""
}

func (w *walker) used(ident *ast.Ident) bool {
	obj := w.typed.info.Defs[ident]
	if obj == nil {
		return false
	}
	for _, use := range w.typed.info.Uses {
		if use == obj {
			return true
		}
	}
	return false
}

func forNames(clause string) []string {
	return strings.FieldsFunc(strings.SplitN(clause, ":=", 2)[0], func(r rune) bool { return r == ',' || r == ' ' })
}

// call classifies a templ component call: @X(...) with or without children.
func (w *walker) call(kind string, e parser.Expression, r parser.Range, children bool) {
	construct, status, reasons, gap, callee := w.callClassification(e)
	if children {
		construct += "-with-children"
	}
	w.addGap(kind, construct, status, reasons, gap, r, e.Value)
	w.nodes[len(w.nodes)-1].Callee = callee
	w.nodes[len(w.nodes)-1].References = w.argumentTemplates(e, callee)
}

// argumentTemplates lists the files of templates a call passes in its
// arguments (@Frame(widgets.Badge("x"))): the call needs them converted
// first, as it needs its callee.
func (w *walker) argumentTemplates(e parser.Expression, callee string) []string {
	expr, typed := w.goExpr(e)
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil
	}
	files := map[string]bool{}
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			inner, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, obj := w.callee(inner, typed)
			if file, ok := w.symbols.template(w, name, obj, typed); ok && file != "" && file != callee {
				files[file] = true
			}
			return true
		})
	}
	return sortedKeys(files)
}

func (w *walker) callClassification(e parser.Expression) (construct string, status Status, reasons []string, gap, callee string) {
	expr, typed := w.goExpr(e)
	if expr == nil {
		return "component-call", ReviewRequired, []string{"unparsed-go-expression"}, "", ""
	}
	switch x := expr.(type) {
	case *ast.CallExpr:
		if _, ok := x.Fun.(*ast.FuncLit); ok {
			return "component-call", ReviewRequired, []string{"immediately-invoked-function"}, "", ""
		}
		name, obj := w.callee(x, typed)
		switch name {
		case templPath + ".Raw":
			return "raw-html", Blocked, []string{"trusted-html"}, "trusted-html", ""
		case templPath + ".JSONScript":
			// script type="application/json" id="…" "{data}" with a
			// presenter-built JSONValue.
			return "json-script", PresentationScaffolded, []string{"json-value"}, "", ""
		case templPath + ".JSFuncCall":
			return w.scriptCall(x, typed)
		case templPath + ".JSUnsafeFuncCall":
			return "script-call", ReviewRequired, []string{"unsafe-script-call"}, "", ""

		case templPath + ".Join":
			return "component-join", ReviewRequired, []string{"dynamic-component"}, "", ""
		case ".Once":
			if fn, ok := obj.(*types.Func); !typed || ok && fn.Pkg() != nil && fn.Pkg().Path() == templPath {
				// Not a language feature: the guarded script moves into the
				// static bundle.
				return "once-handle", ReviewRequired, []string{"once-handle-static-bundle"}, "", ""
			}
		}
		if strings.HasPrefix(name, templPath+".") {
			return "templ-runtime-component", ReviewRequired, []string{"templ-runtime-component"}, "", ""
		}
		if file, ok := w.symbols.template(w, name, obj, typed); ok {
			status, reasons := Converted, []string(nil)
			construct := "component-call"
			if strings.HasPrefix(name, ".") {
				construct = "method-template-call"
				status = PresentationScaffolded
				reasons = append(reasons, "method-template")
			}
			for _, arg := range x.Args {
				argStatus, argReasons := w.valueExpr(arg, typed, contextArgument)
				status = maxStatus(status, argStatus)
				if argStatus != Converted {
					reasons = mergeReasons(reasons, withoutGap(argReasons))
				}
			}
			return construct, status, reasons, "", file
		}
		return "component-call", ReviewRequired, []string{"function-returning-component"}, "", ""
	case *ast.Ident:
		if kind, ok := w.lookup(x.Name); ok && kind == "component" {
			return "slot-outlet", Converted, []string{"component-input-as-slot"}, "", ""
		}
		if typed {
			if v, ok := w.object(x, typed).(*types.Var); ok && isComponent(v.Type()) && w.isParam(v) {
				return "slot-outlet", Converted, []string{"component-input-as-slot"}, "", ""
			}
		}
		return "dynamic-component", ReviewRequired, []string{"dynamic-component"}, "", ""
	}
	return "dynamic-component", ReviewRequired, []string{"dynamic-component"}, "", ""
}

func (w *walker) isParam(v *types.Var) bool {
	kind, ok := w.lookup(v.Name())
	return ok && kind == "component"
}

func withoutGap(reasons []string) []string {
	var kept []string
	for _, reason := range reasons {
		if !strings.HasPrefix(reason, "gap:") {
			kept = append(kept, reason)
		}
	}
	return kept
}

func mergeReasons(a, b []string) []string {
	for _, reason := range b {
		a = appendUnique(a, reason)
	}
	return a
}

func isNil(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}

func isBool(t types.Type) bool {
	if t == nil {
		return false
	}
	basic, ok := t.Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsBoolean != 0
}

func isString(t types.Type) bool {
	if t == nil {
		return false
	}
	basic, ok := t.Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsString != 0
}

func isScalar(t types.Type) bool {
	if t == nil {
		return false
	}
	basic, ok := t.Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsConstType != 0
}

func isOptionalScalar(t types.Type) bool {
	if t == nil {
		return false
	}
	pointer, ok := t.Underlying().(*types.Pointer)
	return ok && isScalar(pointer.Elem())
}

func isSignature(t types.Type) bool {
	_, ok := t.Underlying().(*types.Signature)
	return ok
}

// hasInitializer reports whether an if clause carries an init statement
// (if x := f(); x != nil).
func hasInitializer(clause string) bool {
	file, err := goparser.ParseFile(token.NewFileSet(), "", "package p\nfunc _() {\nif "+clause+" {}\n}", 0)
	if err != nil {
		return false
	}
	stmt, ok := file.Decls[0].(*ast.FuncDecl).Body.List[0].(*ast.IfStmt)
	return ok && stmt.Init != nil
}

// listElements parses a templ attribute value list ({ "a", b, templ.KV(...) })
// and returns its elements, typed when the generated Go is available.
func (w *walker) listElements(e parser.Expression) ([]ast.Expr, bool) {
	const prefix = "[]any{"
	fset := token.NewFileSet()
	parsed, err := goparser.ParseExprFrom(fset, "", prefix+e.Value+"}", 0)
	if err != nil {
		return nil, false
	}
	literal, ok := parsed.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	start, typedOK := 0, false
	if w.typed != nil {
		start, typedOK = w.typed.target(e)
	}
	elements := make([]ast.Expr, 0, len(literal.Elts))
	allTyped := typedOK
	for _, element := range literal.Elts {
		var typedElement ast.Expr
		if typedOK {
			from := start + fset.Position(element.Pos()).Offset - len(prefix)
			to := start + fset.Position(element.End()).Offset - len(prefix)
			for _, n := range w.typed.nodes[from] {
				if expr, ok := n.(ast.Expr); ok && w.typed.tok.Offset(expr.End()) == to {
					typedElement = expr
					break
				}
			}
		}
		if typedElement == nil {
			allTyped = false
			typedElement = element
		}
		elements = append(elements, typedElement)
	}
	if !allTyped {
		// Mixed typed and syntactic nodes cannot share one types.Info.
		return literal.Elts, false
	}
	return elements, true
}

func derefType(t types.Type) types.Type {
	if pointer, ok := t.(*types.Pointer); ok {
		return pointer.Elem()
	}
	return t
}

// scriptCall classifies templ.JSFuncCall(name, args...): with literal
// arguments it is a static script, script "name(args)"; otherwise the
// presenter builds each argument as a JSONValue for a mixed script body
// (migration-language-v1).
func (w *walker) scriptCall(call *ast.CallExpr, typed bool) (construct string, status Status, reasons []string, gap, callee string) {
	for _, arg := range call.Args {
		if _, ok := arg.(*ast.BasicLit); !ok {
			return "script-call", PresentationScaffolded, []string{"json-value", "security-review-marker"}, "", ""
		}
	}
	return "script-call", Converted, []string{"static-script-call"}, "", ""
}

func isBasicLit(expr ast.Expr) bool {
	_, ok := expr.(*ast.BasicLit)
	return ok
}

// goLeadingLiteral returns the string literal text that starts a Go string
// expression: the literal operands of a + chain before its first value, or
// a fmt.Sprintf format before its first verb.
func goLeadingLiteral(expr ast.Expr) string {
	var text strings.Builder
	var walk func(ast.Expr) bool
	walk = func(expr ast.Expr) bool {
		switch e := unparen(expr).(type) {
		case *ast.BasicLit:
			value, err := strconv.Unquote(e.Value)
			if e.Kind != token.STRING || err != nil {
				return false
			}
			text.WriteString(value)
			return true
		case *ast.BinaryExpr:
			return e.Op == token.ADD && walk(e.X) && walk(e.Y)
		case *ast.CallExpr:
			if selector, ok := e.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Sprintf" && len(e.Args) > 0 {
				if lit, ok := e.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					format, err := strconv.Unquote(lit.Value)
					if err != nil {
						return false
					}
					for i := 0; i < len(format); i++ {
						if format[i] != '%' {
							text.WriteByte(format[i])
							continue
						}
						if i+1 < len(format) && format[i+1] == '%' {
							text.WriteByte('%')
							i++
							continue
						}
						return false
					}
				}
			}
		}
		return false
	}
	walk(expr)
	return text.String()
}
