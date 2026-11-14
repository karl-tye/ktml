package templmigrate

import (
	"fmt"
	"go/ast"
	"go/types"
	"regexp"
	"slices"
	"strings"

	parser "github.com/a-h/templ/parser/v2"
)

// callee resolves a call to the signature of a converted template.
func (c *conversion) callee(w *walker, call *ast.CallExpr) *tmplSig {
	_, obj := w.callee(call, true)
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil {
		return nil
	}
	key := fn.Pkg().Path() + "."
	if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
		recvType := recv.Type()
		if pointer, ok := recvType.(*types.Pointer); ok {
			recvType = pointer.Elem()
		}
		if named, ok := types.Unalias(recvType).(*types.Named); ok {
			key += named.Obj().Name() + "."
		}
	}
	if sig := c.sigFor[key+fn.Name()]; sig != nil {
		return sig
	}
	return c.foreignSig(fn)
}

// call converts @X(…) and @X(…) { children }.
func (te *tmplEmitter) call(e parser.Expression, children []parser.Node, body bool, src *Node, r parser.Range) []kNode {
	expr, typed := te.w.goExpr(e)
	if expr == nil || !typed {
		te.partial(src, r, "untyped-expression", "call not type-checked")
		return nil
	}
	if p, ok := te.paramPath(expr); ok && p.kind == "component" {
		if body {
			te.partial(src, r, "slot-children", "children passed to a content input")
		}
		return []kNode{&kOutlet{name: p.ktml, src: src}}
	}
	call, ok := unparen(expr).(*ast.CallExpr)
	if !ok {
		te.partial(src, r, "dynamic-component", "dynamic components have no KTML equivalent: "+e.Value)
		return []kNode{&kNote{text: "ktml-migrate: TODO dynamic component " + e.Value}}
	}
	nodes := te.callExpr(call, src, r)
	if body {
		for _, node := range nodes {
			if kc, ok := node.(*kCall); ok {
				kc.children = te.children(children, nil, stripEnds)
			}
		}
	}
	return nodes
}

// callExpr converts one call expression: a template call, or one of the
// templ runtime components the policy converts.
func (te *tmplEmitter) callExpr(call *ast.CallExpr, src *Node, r parser.Range) []kNode {
	callee := te.c.callee(te.w, call)
	if callee == nil {
		return te.runtimeCall(call, src, r)
	}
	te.fe.importComponent(callee)
	kc := &kCall{name: callee.component, src: src}
	var nodes []kNode
	if callee.foreign != nil {
		for _, extra := range callee.foreign.extra {
			te.partial(src, r, "bridge-field", fmt.Sprintf("the bridge %s also fills %s, which no templ parameter maps to; fill it in the KTML call by hand", callee.name+"Templ", extra))
			nodes = append(nodes, &kNote{text: "ktml-migrate: TODO fill " + extra + " (from the bridge " + callee.name + "Templ)"})
		}
	}
	args := call.Args
	if callee.view {
		if te.view() {
			var goArgs []string
			start, end := te.span(call)
			if callee.receiver != nil {
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					goArgs = append(goArgs, te.goText(selector.X, src, r))
				}
			}
			// The clock goes before a variadic presenter parameter.
			clockAt := -1
			for i, arg := range args {
				param := callee.params[min(i, len(callee.params)-1)]
				if param.kind != "component" && param.kind != "components" && param.kind != "attributes" {
					if callee.clock && callee.variadicPresenter() && i == len(callee.params)-1 {
						clockAt = len(goArgs)
					}
					goArgs = append(goArgs, te.goText(arg, src, r))
				}
			}
			// A spread fills the variadic parameter, which takes a
			// presenter argument only when it is not content.
			if call.Ellipsis.IsValid() && callee.variadicPresenter() && len(goArgs) > 0 {
				goArgs[len(goArgs)-1] += "..."
			}
			if callee.clock {
				if clockAt >= 0 {
					goArgs = slices.Insert(goArgs, clockAt, te.sig.clockName)
				} else {
					goArgs = append(goArgs, te.sig.clockName)
				}
			}
			if callee.context {
				goArgs = append([]string{"ctx"}, goArgs...)
			}
			te.fe.importRecord(callee, callee.viewType)
			goExpr := te.fe.presenterRef(callee) + "(" + strings.Join(goArgs, ", ") + ")"
			path := te.fieldValue(start, end, callee.viewType, goExpr, fieldWiring, []string{callee.name}, src)
			kc.attrs = append(kc.attrs, kAttr{kind: attrValue, name: callee.viewInput, value: `"{` + path + `}"`})
		} else {
			te.partial(src, r, "presenter-required", "a direct template calls "+callee.name+", which needs a presenter")
		}
	}
	for i, param := range callee.params {
		if i >= len(args) {
			break
		}
		switch param.kind {
		case "component":
			te.contentArgument(kc, param.ktml, []ast.Expr{args[i]}, src, r)
			if n := len(kc.slots); n > 0 && kc.slots[n-1].forwards != "" && !te.c.testsPresence(callee, param.name, map[*tmplSig]bool{}) {
				// The callee never tells absent content from empty
				// content, so the plain fill renders the same.
				kc.slots[n-1].forwards = ""
			}
		case "components":
			te.contentArgument(kc, param.ktml, args[i:], src, r)
		case "attributes":
			kc.attrs = append(kc.attrs, te.attributesArgument(param.name, args[i], src, r)...)
		default:
			if !callee.view {
				kc.attrs = append(kc.attrs, te.scalarArgument(param, args[i], src, r))
			}
		}
	}
	return append(nodes, kc)
}

// scalarArgument passes a direct template's scalar input.
func (te *tmplEmitter) scalarArgument(param sigParam, arg ast.Expr, src *Node, r parser.Range) kAttr {
	kind, _ := ktmlScalar(param.t)
	if tv, ok := te.constant(arg); ok {
		switch kind {
		case "integer", "boolean":
			return kAttr{kind: attrValue, name: param.name, value: tv.Value.ExactString()}
		default:
			text, _ := te.literalText(arg)
			return kAttr{kind: attrValue, name: param.name, value: ktmlLiteral(text)}
		}
	}
	if !te.view() {
		p, _ := te.paramPath(arg)
		return kAttr{kind: attrValue, name: param.name, value: `"{` + p.name + `}"`}
	}
	if kind == "string" {
		if parts, ok := te.templateParts(arg); ok {
			return kAttr{kind: attrValue, name: param.name, value: ktmlValue(te.partSegments(parts, src, r, []string{param.name}))}
		}
	}
	start, end := te.span(arg)
	goExpr := te.goText(arg, src, r)
	t := te.typeOf(arg)
	switch kind {
	case "integer":
		goExpr = int64Expr(goExpr, t)
	case "string":
		goExpr = te.fe.goStringExpr(goExpr, t)
	}
	path := te.fieldValue(start, end, kind, goExpr, te.kindOf(arg), append([]string{param.name}, hintOf(arg)...), src)
	return kAttr{kind: attrValue, name: param.name, value: `"{` + path + `}"`}
}

// contentArgument turns templ.Component arguments into a slot fill: a
// template call, the caller's own content input, or nothing for nil.
func (te *tmplEmitter) contentArgument(kc *kCall, name string, args []ast.Expr, src *Node, r parser.Range) {
	var body []kNode
	for _, arg := range args {
		arg = unparen(arg)
		if ident, ok := arg.(*ast.Ident); ok && ident.Name == "nil" {
			continue
		}
		if p, ok := te.paramPath(arg); ok && p.kind == "component" {
			body = append(body, &kOutlet{name: p.ktml})
			continue
		}
		if call, ok := arg.(*ast.CallExpr); ok {
			body = append(body, te.callExpr(call, nil, r)...)
			continue
		}
		te.partial(src, r, "dynamic-component", "a component-valued argument that is not a template call: "+types.ExprString(arg))
	}
	if len(body) > 0 {
		slot := kSlot{name: name, body: body}
		if len(args) == 1 {
			if p, ok := te.paramPath(unparen(args[0])); ok && p.kind == "component" {
				slot.forwards = p.ktml
			}
		}
		kc.slots = append(kc.slots, slot)
	}
}

// attributesArgument passes a templ.Attributes argument: a bag literal for
// a static map, the caller's own Attributes input, an empty bag for nil, or
// a presenter-built bag.
func (te *tmplEmitter) attributesArgument(name string, arg ast.Expr, src *Node, r parser.Range) []kAttr {
	arg = unparen(arg)
	if ident, ok := arg.(*ast.Ident); ok && ident.Name == "nil" {
		return []kAttr{{kind: attrBag, name: name}}
	}
	if p, ok := te.paramPath(arg); ok && p.kind == "attributes" {
		return []kAttr{{kind: attrValue, name: name, value: `"{` + p.name + `}"`}}
	}
	if entries, ok := staticAttributes(arg); ok {
		bag := kAttr{kind: attrBag, name: name}
		for _, entry := range entries {
			bag.bag = append(bag.bag, staticKAttr(entry)...)
		}
		return []kAttr{bag}
	}
	if !te.view() {
		te.partial(src, r, "derived-attributes", "derived attributes in a direct template")
		return nil
	}
	start, end := te.span(arg)
	te.fe.helpers["attributes"] = true
	path := te.fieldValue(start, end, "Attributes", "migrateAttributes("+te.goText(arg, src, r)+")", fieldDerived, []string{name}, src)
	te.fe.security(src, r, "attributes", path)
	te.review(src, r, "derived-attributes", "Go-built attributes become a presenter-built Attributes bag; build it with the ktml.*Attr constructors")
	return []kAttr{{kind: attrValue, name: name, value: `"{` + path + `}"`, note: "ktml-migrate: security-review(attributes): " + path + " is built in Go"}}
}

// runtimeCall converts calls of templ runtime components.
func (te *tmplEmitter) runtimeCall(call *ast.CallExpr, src *Node, r parser.Range) []kNode {
	name, _ := te.w.callee(call, true)
	switch name {
	case templPath + ".JSFuncCall":
		return te.jsFuncCall(call, src, r)
	case templPath + ".JSONScript":
		return te.jsonScript(call, src, r)
	case ".Once":
		te.partial(src, r, "once-handle", "templ.NewOnceHandle is not a language feature: move the guarded script into the static bundle")
		return []kNode{&kNote{text: "ktml-migrate: TODO move the script guarded by " + types.ExprString(call) + " into the static bundle"}}
	}
	if fn, ok := te.typeOf(call.Fun).(*types.Signature); ok && fn.Results().Len() == 1 && isComponent(fn.Results().At(0).Type()) {
		if _, obj := te.w.callee(call, true); obj != nil && te.c.templateFile(obj) {
			te.partial(src, r, "unconverted-dependency", fmt.Sprintf("%s is a templ template outside this batch: KTML markup calls only KTML components, so convert it first (Phase 7 dependency order) or in the same batch", types.ExprString(call.Fun)))
			return []kNode{&kNote{text: "ktml-migrate: TODO " + types.ExprString(call) + " is an unconverted templ template"}}
		}
	}
	te.partial(src, r, "function-returning-component", "the call does not name a converted template: "+types.ExprString(call))
	return []kNode{&kNote{text: "ktml-migrate: TODO " + types.ExprString(call) + " is not a converted template"}}
}

// jsFuncCall converts templ.JSFuncCall(name, args…) to the script templ
// renders: <script>name(args)</script>, with literal arguments in a static
// body and Go values as presenter-built JSON.
func (te *tmplEmitter) jsFuncCall(call *ast.CallExpr, src *Node, r parser.Range) []kNode {
	if len(call.Args) == 0 {
		te.partial(src, r, "script-call", "templ.JSFuncCall without a function name")
		return nil
	}
	fn, ok := te.literalText(call.Args[0])
	if !ok {
		te.partial(src, r, "script-call", "templ.JSFuncCall with a computed function name")
		return nil
	}
	segments := []segment{{literal: fn + "("}}
	for i, arg := range call.Args[1:] {
		if i > 0 {
			segments = append(segments, segment{literal: ", "})
		}
		if tv, ok := te.constant(arg); ok {
			segments = append(segments, segment{literal: tv.Value.ExactString()})
			continue
		}
		if !te.view() {
			te.partial(src, r, "script-call", "script data in a direct template")
			return nil
		}
		start, end := te.span(arg)
		path := te.fieldValue(start, end, "JSONValue", te.fe.imports.add("github.com/karl-tye/ktml", "ktml")+".MustJSON("+te.goText(arg, src, r)+")", fieldDerived, append([]string{"arg"}, hintOf(arg)...), src)
		te.fe.security(src, r, "json", path)
		segments = append(segments, segment{path: path})
	}
	segments = append(segments, segment{literal: ")"})
	static := true
	for _, s := range segments {
		if s.path != "" {
			static = false
		}
	}
	el := &kElement{name: "script", src: src}
	if static {
		var b strings.Builder
		for _, s := range segments {
			b.WriteString(s.literal)
		}
		el.children = []kNode{&kRaw{text: b.String()}}
	} else {
		el.children = []kNode{&kText{segments: segments}}
	}
	return []kNode{el}
}

// jsonScript converts templ.JSONScript(id, value) to the JSON data element.
func (te *tmplEmitter) jsonScript(call *ast.CallExpr, src *Node, r parser.Range) []kNode {
	if len(call.Args) != 2 || !te.view() {
		te.partial(src, r, "json-script", "templ.JSONScript needs a presenter")
		return nil
	}
	id, ok := te.literalText(call.Args[0])
	attrs := []kAttr{{kind: attrValue, name: "id", value: ktmlLiteral(id)}, {kind: attrValue, name: "type", value: `"application/json"`}}
	if !ok {
		start, end := te.span(call.Args[0])
		path := te.fieldValue(start, end, "string", te.goText(call.Args[0], src, r), te.kindOf(call.Args[0]), []string{"scriptId"}, src)
		attrs[0].value = `"{` + path + `}"`
	}
	te.fe.helpers["jsonscript"] = true
	start, end := te.span(call.Args[1])
	path := te.fieldValue(start, end, "JSONValue", "migrateJSONScript("+te.goText(call.Args[1], src, r)+")", fieldDerived, append([]string{"data"}, hintOf(call.Args[1])...), src)
	te.fe.security(src, r, "json", path)
	return []kNode{&kElement{name: "script", attrs: attrs, children: []kNode{&kText{segments: []segment{{path: path}}}}, src: src}}
}

// --- loops ---

// forExpr converts a loop into Keyed Repetition over a presenter-built
// collection of item records. The key is the item's identity field when
// the classifier found one, and otherwise a presenter-chosen key with a
// review marker.
func (te *tmplEmitter) forExpr(n *parser.ForExpression, next parser.Node) []kNode {
	src := te.c.node(te.fe.file, "ForExpression", n.Range)
	if p, ok := contentLoop(te.w, te.paramPath, n); ok {
		// Every component of a variadic content input: its outlet.
		for _, child := range trimWhitespace(n.Children) {
			switch call := child.(type) {
			case *parser.TemplElementExpression:
				te.c.node(te.fe.file, "TemplElementExpression", call.Range)
			}
		}
		return []kNode{&kOutlet{name: p.ktml, src: src}}
	}
	if !te.view() {
		te.partial(src, n.Range, "loop", "loop in a direct template")
		return nil
	}
	start, end, ok := te.fe.rw.span(n.Expression)
	if !ok {
		te.partial(src, n.Range, "loop", "loop not found in the generated Go")
		return nil
	}
	var valueName, keyName string
	var rangeX ast.Expr
	var element types.Type
	counted := ""
	if stmt, ok := stmtContaining[*ast.RangeStmt](te.fe.t, n.Expression); ok {
		if ident, ok := stmt.Value.(*ast.Ident); ok && ident.Name != "_" {
			valueName = ident.Name
		}
		if ident, ok := stmt.Key.(*ast.Ident); ok && ident.Name != "_" {
			keyName = ident.Name
		}
		rangeX = stmt.X
		switch u := te.typeOf(stmt.X).Underlying().(type) {
		case *types.Slice:
			element = u.Elem()
		case *types.Array:
			element = u.Elem()
		case *types.Map:
			element = u.Elem()
		}
	} else if stmt, ok := stmtContaining[*ast.ForStmt](te.fe.t, n.Expression); ok {
		if assign, ok := stmt.Init.(*ast.AssignStmt); ok && len(assign.Lhs) == 1 {
			if ident, ok := assign.Lhs[0].(*ast.Ident); ok {
				counted = ident.Name
			}
		}
	} else {
		te.partial(src, n.Range, "loop", "loop statement not found in the generated Go")
		return nil
	}
	parent := te.scope
	base := strings.TrimSuffix(te.sig.viewType, "View")
	itemName := valueName
	if itemName == "" {
		itemName = "item"
	}
	rec := newRecord(te.fe.recordName(base+upperFirst(itemName)), fmt.Sprintf("An item of %s's loop at %s:%d.", te.sig.name, te.fe.file.rel, int(n.Range.From.Line)+1))
	te.fe.records = append(te.fe.records, rec)
	ktmlItem := itemName
	if ktmlKeywords[ktmlItem] || te.ktml[ktmlItem] || !validKTMLName(ktmlItem) {
		ktmlItem = uniqueName(te.ktml, "item")
	}
	te.ktml[ktmlItem] = true
	item := &recScope{rec: rec, goVar: te.goVar(itemName + "Item"), root: ktmlItem, parent: parent, depth: parent.depth + 1}

	collHint := []string{"items"}
	if rangeX != nil {
		collHint = hintOf(rangeX)
		collHint = collHint[len(collHint)-1:]
	}
	collField := &field{name: uniqueName(parent.rec.used, lowerCamel(collHint)), ktmlType: "[" + rec.name + "]", kind: fieldWiring, src: src0(src)}
	parent.rec.fields = append(parent.rec.fields, collField)
	collPath := parent.root + "." + collField.name

	header, problems := te.fe.rw.rewrite(start, end)
	for _, problem := range problems {
		te.partial(src, n.Range, "presenter-reference", problem)
	}
	te.declare(start, end, item)
	headerAt := te.body.Len()
	if len(problems) > 0 {
		// The loop cannot run in the presenter package: it becomes an empty
		// loop, and whatever uses its variables is unresolved.
		te.open("for range 0 { // TODO(ktml-migrate): for " + header)
		for _, ident := range te.fe.rw.idents {
			offset := te.fe.t.tok.Offset(ident.Pos())
			if offset >= start && offset < end {
				if obj := te.fe.t.info.Defs[ident]; obj != nil {
					te.c.poisoned[obj] = "its loop cannot run in the presenter package"
				}
			}
		}
	} else {
		te.open("for " + header + " {")
	}
	te.line("var " + item.goVar + " " + rec.name)

	// The key.
	keyType, keyExpr, nilKey := "", "", ""
	reasons := []string(nil)
	if src != nil {
		reasons = src.Reasons
	}
	// An ID-like field is an identity the converter cannot prove: the same
	// ID can repeat, and an empty one repeats as soon as two items lack it,
	// which is a render error (ErrDuplicateKey). Each such key is flagged.
	identity := ""
	for _, reason := range reasons {
		if fieldName, ok := strings.CutPrefix(reason, "key:"); ok && valueName != "" {
			obj, _, _ := types.LookupFieldOrMethod(element, true, nil, fieldName)
			if v, ok := obj.(*types.Var); ok && isInteger(v.Type()) {
				keyType, keyExpr = "integer", int64Expr(valueName+"."+fieldName, v.Type())
				identity = valueName + "." + fieldName + " is unique and set"
			} else {
				keyType, keyExpr = "string", te.fe.goStringExpr(valueName+"."+fieldName, obj.Type())
				identity = valueName + "." + fieldName + " is unique and non-empty"
			}
		}
		if fieldName, ok := strings.CutPrefix(reason, "key-needs-presenter-string:"); ok && valueName != "" {
			obj, _, _ := types.LookupFieldOrMethod(element, true, nil, fieldName)
			if v, ok := obj.(*types.Var); ok && !nillable(v.Type()) {
				keyType, keyExpr = "string", te.fe.imports.add("fmt", "fmt")+".Sprint("+valueName+"."+fieldName+")"
				identity = valueName + "." + fieldName + " is unique and non-empty"
			} else {
				// fmt.Sprint of a pointer prints <nil> for every nil ID (or an
				// address): never a key.
				nilKey = valueName + "." + fieldName
			}
		}
	}
	switch {
	case len(problems) > 0:
		keyType, keyExpr = "integer", "int64(len("+parent.goVar+"."+collField.goName()+"))"
	case keyExpr != "":
		if identity != "" {
			te.review(src, n.Range, "identity-key", "the scaffold keys items by "+strings.SplitN(identity, " ", 2)[0]+", which the converter cannot prove unique: confirm "+identity+" in every collection, or key by position")
			te.line("// TODO(ktml-migrate): confirm " + identity + "; a repeated key is a render error.")
		}
	case nilKey != "":
		keyType, keyExpr = "integer", "int64(len("+parent.goVar+"."+collField.goName()+"))"
		te.review(src, n.Range, "position-key", "the identity "+nilKey+" can be nil, so the scaffold keys items by position. Choose an identity key that is always set")
		te.line("// TODO(ktml-migrate): key by a stable identity, not by position (" + nilKey + " can be nil).")
	case counted != "":
		keyType, keyExpr = "integer", "int64("+counted+")"
	case element != nil && isScalar(element) && valueName != "":
		if isInteger(element) {
			keyType, keyExpr = "integer", int64Expr(valueName, element)
		} else {
			keyType, keyExpr = "string", te.fe.goStringExpr(valueName, element)
		}
		te.review(src, n.Range, "scalar-item-key", "the item itself is the key; confirm the collection has no duplicates")
	case rangeX != nil && keyName != "" && isMapType(te.typeOf(rangeX)):
		keyType, keyExpr = "string", te.fe.imports.add("fmt", "fmt")+".Sprint("+keyName+")"
	default:
		keyType, keyExpr = "integer", "int64(len("+parent.goVar+"."+collField.goName()+"))"
		te.review(src, n.Range, "position-key", "no stable identity found: the scaffold keys items by position. Choose an identity key")
		te.line("// TODO(ktml-migrate): key by a stable identity, not by position.")
	}
	keyField := &field{name: "key", ktmlType: keyType, kind: fieldWiring, src: src0(src)}
	rec.used["key"] = true
	rec.fields = append(rec.fields, keyField)
	te.line(item.goVar + ".Key = " + keyExpr)

	te.scope = item
	body := te.children(n.Children, next, stripEnds)
	te.scope = parent
	te.line(parent.goVar + "." + collField.goName() + " = append(" + parent.goVar + "." + collField.goName() + ", " + item.goVar + ")")
	te.close("}")
	if len(problems) == 0 {
		te.blankUnused(headerAt, start, end)
	}
	if src != nil {
		te.fe.srcmap.Fields = append(te.fe.srcmap.Fields, FieldMapping{Template: te.sig.name, From: src.From, To: src.To, Record: parent.rec.name, Field: collField.name, Kind: collField.kind.String()})
	}
	return []kNode{&kFor{item: ktmlItem, coll: collPath, key: ktmlItem + ".key", body: body, src: src}}
}

// blankUnused replaces the loop variables a presenter loop never reads with
// _ (Go rejects unused variables), editing the header written at headerAt.
func (te *tmplEmitter) blankUnused(headerAt, start, end int) {
	text := te.body.String()
	headerEnd := headerAt + strings.Index(text[headerAt:], "\n")
	header, loop := text[headerAt:headerEnd], text[headerEnd:]
	assign := strings.Index(header, ":=")
	if assign < 0 {
		return
	}
	names := strings.Split(strings.TrimPrefix(strings.TrimSpace(header[:assign]), "for "), ",")
	var code []string
	for _, line := range strings.Split(loop, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			code = append(code, line)
		}
	}
	loopCode := strings.Join(code, "\n")
	changed := false
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "_" || regexp.MustCompile(`\b`+regexp.QuoteMeta(name)+`\b`).MatchString(loopCode) {
			continue
		}
		names[i] = "_"
		changed = true
	}
	if !changed {
		return
	}
	lhs := strings.Join(trimAll(names), ", ")
	rest := strings.TrimSpace(header[assign+2:])
	indent := header[:len(header)-len(strings.TrimLeft(header, "\t"))]
	if lhs == "_" || lhs == "_, _" {
		header = indent + "for " + rest
	} else {
		header = indent + "for " + lhs + " := " + rest
	}
	te.body.Reset()
	te.body.WriteString(text[:headerAt] + header + loop)
}

func trimAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strings.TrimSpace(v)
	}
	return out
}

func src0(n *Node) Position {
	if n == nil {
		return Position{}
	}
	return n.From
}

func isMapType(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Map)
	return ok
}

func validKTMLName(name string) bool {
	if name == "" || !(name[0] >= 'a' && name[0] <= 'z') {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

var nilComparison = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*[!=]=\s*nil\b`)

// testsPresence reports whether a template tells absent content from
// present content for one of its parameters: it compares it with nil, or
// passes it to a template that does. A component converted by an earlier
// batch is assumed to.
func (c *conversion) testsPresence(sig *tmplSig, param string, seen map[*tmplSig]bool) bool {
	if sig.foreign != nil || sig.decl == nil {
		return true
	}
	if seen[sig] {
		return false
	}
	seen[sig] = true
	for _, e := range templExpressions(sig.tmpl.Children) {
		for _, match := range nilComparison.FindAllStringSubmatch(e.Value, -1) {
			if match[1] == param {
				return true
			}
		}
	}
	found := false
	w := c.run.walkers[sig.file]
	ast.Inspect(sig.decl, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		callee := c.callee(w, call)
		if callee == nil {
			return true
		}
		for i, arg := range call.Args {
			if ident, ok := unparen(arg).(*ast.Ident); ok && ident.Name == param && i < len(callee.params) {
				if c.testsPresence(callee, callee.params[i].name, seen) {
					found = true
				}
			}
		}
		return true
	})
	return found
}

// nillable reports whether a value of the type can be nil, so that
// fmt.Sprint prints it as <nil> (or as an address).
func nillable(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Interface, *types.Map, *types.Slice, *types.Signature, *types.Chan:
		return true
	}
	return false
}
