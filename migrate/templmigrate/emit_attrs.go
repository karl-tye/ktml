package templmigrate

import (
	"go/ast"
	"html"
	"regexp"
	"strings"

	parser "github.com/a-h/templ/parser/v2"
	"github.com/karl-tye/ktml/internal/compiler"
)

func (te *tmplEmitter) attributes(element string, attrs []parser.Attribute) []kAttr {
	var out []kAttr
	for _, attr := range attrs {
		converted := te.attribute(element, attr)
		if src := te.attributeNode(attr); src != nil {
			if len(converted) == 0 {
				// An attribute that renders nothing (false, or a partial
				// one) still maps to its element.
				converted = append(converted, kAttr{kind: attrNone, src: src})
			} else {
				converted[0].src = src
			}
		}
		out = append(out, converted...)
	}
	return out
}

// attributeNode returns the classified node of a templ attribute.
func (te *tmplEmitter) attributeNode(attr parser.Attribute) *Node {
	file := te.fe.file
	switch a := attr.(type) {
	case *parser.ConstantAttribute:
		return te.c.node(file, "ConstantAttribute", a.Range)
	case *parser.BoolConstantAttribute:
		return te.c.node(file, "BoolConstantAttribute", a.Range)
	case *parser.BoolExpressionAttribute:
		return te.c.node(file, "BoolExpressionAttribute", a.Range)
	case *parser.ExpressionAttribute:
		return te.c.node(file, "ExpressionAttribute", a.Range)
	case *parser.SpreadAttributes:
		return te.c.node(file, "SpreadAttributes", a.Range)
	case *parser.ConditionalAttribute:
		return te.c.node(file, "ConditionalAttribute", a.Range)
	}
	return nil
}

func (te *tmplEmitter) attribute(element string, attribute parser.Attribute) []kAttr {
	file := te.fe.file
	switch a := attribute.(type) {
	case *parser.ConstantAttribute:
		src := te.c.node(file, "ConstantAttribute", a.Range)
		name, ok := constantKey(a.Key)
		if !ok {
			te.partial(src, a.Range, "dynamic-attribute-name", "dynamic attribute names have no KTML equivalent")
			return nil
		}
		value := a.Value
		if strings.EqualFold(name, "class") {
			// templ collapses whitespace in literal classes.
			value = strings.Join(strings.Fields(value), " ")
		}
		return []kAttr{{kind: attrValue, name: name, value: ktmlLiteral(html.UnescapeString(value))}}
	case *parser.BoolConstantAttribute:
		src := te.c.node(file, "BoolConstantAttribute", a.Range)
		name, ok := constantKey(a.Key)
		if !ok {
			te.partial(src, a.Range, "dynamic-attribute-name", "dynamic attribute names have no KTML equivalent")
			return nil
		}
		return []kAttr{{kind: attrBool, name: name}}
	case *parser.BoolExpressionAttribute:
		src := te.c.node(file, "BoolExpressionAttribute", a.Range)
		name, _ := constantKey(a.Key)
		expr, typed := te.w.goExpr(a.Expression)
		if expr == nil || !typed {
			te.partial(src, a.Range, "untyped-expression", "expression not type-checked")
			return nil
		}
		if text, ok := te.literalText(expr); ok {
			if text == "true" {
				return []kAttr{{kind: attrBool, name: name}}
			}
			return nil
		}
		path, _ := te.condition(expr, src, a.Range, name)
		// A direct template's condition can be an expression (!enabled),
		// which no interpolation holds; so can a presence written as one.
		// templ writes presence; a boolean path in an aria-* attribute
		// renders true or false (ADR-0001).
		if compiler.ARIAAttribute(name) || !conditionPath.MatchString(path) {
			return []kAttr{{kind: attrGroup, cond: path, then: []kAttr{{kind: attrBool, name: name}}}}
		}
		return []kAttr{{kind: attrValue, name: name, value: `"{` + path + `}"`}}
	case *parser.ExpressionAttribute:
		src := te.c.node(file, "ExpressionAttribute", a.Range)
		name, ok := constantKey(a.Key)
		if !ok {
			te.partial(src, a.Range, "dynamic-attribute-name", "dynamic attribute names have no KTML equivalent")
			return nil
		}
		return te.expressionAttribute(element, name, a, src)
	case *parser.SpreadAttributes:
		src := te.c.node(file, "SpreadAttributes", a.Range)
		expr, typed := te.w.goExpr(a.Expression)
		if expr == nil || !typed {
			te.partial(src, a.Range, "untyped-expression", "expression not type-checked")
			return nil
		}
		if p, ok := te.paramPath(expr); ok && p.kind == "attributes" {
			return []kAttr{{kind: attrSpread, path: p.name}}
		}
		if entries, ok := staticAttributes(expr); ok {
			var out []kAttr
			for _, entry := range entries {
				out = append(out, staticKAttr(entry)...)
			}
			return out
		}
		if !te.view() {
			te.partial(src, a.Range, "derived-attributes", "derived attributes in a direct template")
			return nil
		}
		start, end := te.span(expr)
		goExpr := "migrateAttributes(" + te.goText(expr, src, a.Range) + ")"
		te.fe.helpers["attributes"] = true
		path := te.fieldValue(start, end, "Attributes", goExpr, fieldDerived, append([]string{"attrs"}, hintOf(expr)...), src)
		te.fe.security(src, a.Range, "attributes", path)
		te.review(src, a.Range, "derived-attributes", "Go-built attributes become a presenter-built Attributes bag; build it with the ktml.*Attr constructors")
		return []kAttr{{kind: attrSpread, path: path, note: "ktml-migrate: security-review(attributes): " + path + " is built in Go"}}
	case *parser.ConditionalAttribute:
		src := te.c.node(file, "ConditionalAttribute", a.Range)
		expr, typed := te.w.goExpr(a.Expression)
		if expr == nil || !typed {
			te.partial(src, a.Range, "untyped-expression", "expression not type-checked")
			return nil
		}
		cond, goRef := te.condition(expr, src, a.Range, "")
		group := kAttr{kind: attrGroup, cond: cond}
		guarded := te.view() && goRef != ""
		if guarded {
			te.open("if " + goRef + " {")
		}
		group.then = te.attributes(element, a.Then)
		if len(a.Else) > 0 {
			if guarded {
				te.close("} else {")
				te.indent++
				te.blocks = append(te.blocks, map[string]bool{})
			}
			group.els = te.attributes(element, a.Else)
		}
		if guarded {
			te.close("}")
		}
		return []kAttr{group}
	}
	te.partial(nil, parser.Range{}, "unknown-attribute", "unhandled templ attribute")
	return nil
}

// staticKAttr converts one entry of a static templ.Attributes literal.
func staticKAttr(entry staticAttribute) []kAttr {
	if entry.boolean {
		if entry.present {
			return []kAttr{{kind: attrBool, name: entry.name}}
		}
		return nil
	}
	return []kAttr{{kind: attrValue, name: entry.name, value: ktmlLiteral(entry.value)}}
}

func (te *tmplEmitter) expressionAttribute(element, name string, a *parser.ExpressionAttribute, src *Node) []kAttr {
	lower := strings.ToLower(name)
	if lower == "class" {
		if elements, typed := te.w.listElements(a.Expression); elements != nil {
			if !typed {
				te.partial(src, a.Range, "untyped-expression", "class list not type-checked")
				return nil
			}
			return te.classList(elements, src, a.Range)
		}
	}
	expr, typed := te.w.goExpr(a.Expression)
	if expr == nil || !typed {
		te.partial(src, a.Range, "untyped-expression", "expression not type-checked")
		return nil
	}
	if text, ok := te.literalText(expr); ok {
		if lower == "class" {
			text = strings.Join(strings.Fields(text), " ")
		}
		return []kAttr{{kind: attrValue, name: name, value: ktmlLiteral(text)}}
	}
	if !te.view() {
		p, _ := te.paramPath(expr)
		return []kAttr{{kind: attrValue, name: name, value: `"{` + p.name + `}"`}}
	}
	start, end := te.span(expr)
	t := te.typeOf(expr)
	hint := append([]string{name}, hintOf(expr)...)
	if compiler.URLAttribute(name) && !compiler.ScriptAttribute(name) && !templURLAttribute(element, lower) {
		// templ writes this URL unsanitized; KTML takes a URL, or literal
		// text that fixes the scheme and host before scalar paths.
		if parts, ok := te.templateParts(expr); !ok || !compiler.SafeURLText(leadingLiteral(parts)) {
			return te.urlAttribute(name, expr, start, end, src, a.Range)
		}
	}
	switch {
	case lower == "class":
		return te.classList([]ast.Expr{expr}, src, a.Range)
	case lower == "style":
		if attrs, ok := te.styleKV(name, expr, src, a.Range); ok {
			return attrs
		}
		te.fe.helpers["style"] = true
		goExpr := "migrateStyle(" + te.fe.goStringExpr(te.goText(expr, src, a.Range), t) + ")"
		path := te.fieldValue(start, end, "CSSValue", goExpr, fieldDerived, hint, src)
		te.fe.security(src, a.Range, "css", path)
		te.review(src, a.Range, "css-value", "templ sanitized this style at render; the scaffold trusts it. Build it with ktml.CSS")
		return []kAttr{{kind: attrValue, name: name, value: `"{` + path + `}"`, note: "ktml-migrate: security-review(css): " + path + " is trusted CSS (migrateStyle); prefer ktml.CSS"}}
	case templURLAttribute(element, lower):
		return te.urlAttribute(name, expr, start, end, src, a.Range)
	case compiler.ScriptAttribute(name):
		if isBool(t) {
			path := te.fieldValue(start, end, "boolean", te.goText(expr, src, a.Range), te.kindOf(expr), hint, src)
			return []kAttr{{kind: attrValue, name: name, value: `"{` + path + `}"`}}
		}
		if call, ok := unparen(expr).(*ast.CallExpr); ok {
			if callee, _ := te.w.callee(call, true); callee == templPath+".JSONString" && len(call.Args) == 1 {
				goExpr := te.fe.imports.add("github.com/karl-tye/ktml", "ktml") + ".MustJSON(" + te.goText(call.Args[0], src, a.Range) + ")"
				path := te.fieldValue(start, end, "JSONValue", goExpr, fieldDerived, hint, src)
				te.fe.security(src, a.Range, "json", path)
				return []kAttr{{kind: attrValue, name: name, value: `"{` + path + `}"`, note: "ktml-migrate: security-review(json): " + path + " is JSON built in Go"}}
			}
		}
		if t != nil && isNamed(t, templPath, "ComponentScript") {
			te.partial(src, a.Range, "script-template", "templ script templates have no KTML equivalent")
			return nil
		}
		goExpr := te.fe.imports.add("github.com/karl-tye/ktml", "ktml") + ".TrustScript(" + te.fe.goStringExpr(te.goText(expr, src, a.Range), t) + ")"
		path := te.fieldValue(start, end, "TrustedScript", goExpr, fieldDerived, hint, src)
		te.fe.security(src, a.Range, "trusted-script", path)
		return []kAttr{{kind: attrValue, name: name, value: `"{` + path + `}"`, note: "ktml-migrate: security-review(script): " + path + " is JavaScript built in Go"}}
	}
	if parts, ok := te.templateParts(expr); ok {
		return []kAttr{{kind: attrValue, name: name, value: ktmlValue(te.partSegments(parts, src, a.Range, []string{name}))}}
	}
	if isInteger(t) {
		path := te.fieldValue(start, end, "integer", int64Expr(te.goText(expr, src, a.Range), t), te.kindOf(expr), hint, src)
		return []kAttr{{kind: attrValue, name: name, value: `"{` + path + `}"`}}
	}
	// templ renders other values with fmt.Sprint, booleans as true/false.
	path := te.fieldValue(start, end, "string", te.fe.goStringExpr(te.goText(expr, src, a.Range), t), te.kindOf(expr), hint, src)
	return []kAttr{{kind: attrValue, name: name, value: `"{` + path + `}"`}}
}

// urlAttribute converts an attribute templ sanitizes as a URL.
func (te *tmplEmitter) urlAttribute(name string, expr ast.Expr, start, end int, src *Node, r parser.Range) []kAttr {
	ktml := te.fe.imports.add("github.com/karl-tye/ktml", "ktml")
	hint := append([]string{name}, hintOf(expr)...)
	inner := expr
	trusted := isSafeURL(te.typeOf(expr))
	if call, ok := unparen(expr).(*ast.CallExpr); ok && len(call.Args) == 1 {
		switch callee, _ := te.w.callee(call, true); callee {
		case templPath + ".URL":
			inner, trusted = call.Args[0], false
		case templPath + ".SafeURL":
			inner, trusted = call.Args[0], true
		}
	}
	text := te.fe.goStringExpr(te.goText(inner, src, r), te.typeOf(inner))
	if trusted {
		path := te.fieldValue(start, end, "TrustedURL", ktml+".TrustURL("+text+")", fieldDerived, hint, src)
		te.fe.security(src, r, "trusted-url", path)
		te.review(src, r, "trusted-url", "templ.SafeURL bypassed URL sanitizing; the scaffold uses ktml.TrustURL")
		return []kAttr{{kind: attrValue, name: name, value: `"{` + path + `}"`, note: "ktml-migrate: security-review(url): " + path + " is a TrustedURL"}}
	}
	path := te.fieldValue(start, end, "URL", ktml+".NewURL("+text+")", fieldDerived, hint, src)
	return []kAttr{{kind: attrValue, name: name, value: `"{` + path + `}"`}}
}

// classList converts a templ class list. Literals, constants, and string
// values join with spaces as templ joins them. One templ.KV becomes a
// conditional attribute group, which keeps the classes in KTML; more than
// one becomes a presenter-built class string.
func (te *tmplEmitter) classList(elements []ast.Expr, src *Node, r parser.Range) []kAttr {
	var segments []segment
	var kvs []*ast.CallExpr
	kvAt := -1
	appendText := func(s segment) {
		if len(segments) > 0 {
			segments = append(segments, segment{literal: " "})
		}
		segments = append(segments, s)
	}
	dynamic := false
	for _, element := range elements {
		if text, ok := te.literalText(element); ok {
			appendText(segment{literal: text})
			continue
		}
		if call, ok := unparen(element).(*ast.CallExpr); ok {
			if callee, _ := te.w.callee(call, true); callee == templPath+".KV" && len(call.Args) == 2 {
				if _, ok := te.literalText(call.Args[0]); ok {
					kvs = append(kvs, call)
					kvAt = len(segments)
					continue
				}
			}
		}
		t := te.typeOf(element)
		if !isString(t) {
			dynamic = true
			continue
		}
		if !te.view() {
			p, _ := te.paramPath(element)
			appendText(segment{path: p.name})
			continue
		}
		start, end := te.span(element)
		appendText(segment{path: te.fieldValue(start, end, "string", te.fe.goStringExpr(te.goText(element, src, r), t), te.kindOf(element), append([]string{"class"}, hintOf(element)...), src)})
	}
	if !dynamic && len(kvs) == 0 {
		return []kAttr{{kind: attrValue, name: "class", value: ktmlValue(segments)}}
	}
	if !dynamic && len(kvs) == 1 && kvAt == len(segments) {
		kvClass, _ := te.literalText(kvs[0].Args[0])
		cond, goRef := te.condition(kvs[0].Args[1], src, r, "")
		_ = goRef
		with := append(append([]segment{}, segments...), segment{})
		with = with[:len(segments)]
		if len(segments) > 0 {
			with = append(with, segment{literal: " "})
		}
		with = append(with, segment{literal: kvClass})
		without := segments
		if len(without) == 0 {
			// templ writes class="" when no class is on.
			without = []segment{{literal: ""}}
		}
		return []kAttr{{kind: attrGroup, cond: cond,
			then: []kAttr{{kind: attrValue, name: "class", value: ktmlValue(with)}},
			els:  []kAttr{{kind: attrValue, name: "class", value: ktmlValue(without)}}}}
	}
	if !te.view() {
		te.partial(src, r, "computed-classes", "computed classes in a direct template")
		return nil
	}
	// The presenter builds the class string with templ's semantics.
	te.fe.helpers["classes"] = true
	var parts []string
	start, end := -1, -1
	for _, element := range elements {
		s, e := te.span(element)
		if start < 0 || s < start {
			start = s
		}
		if e > end {
			end = e
		}
		if call, ok := unparen(element).(*ast.CallExpr); ok {
			if callee, _ := te.w.callee(call, true); callee == templPath+".KV" && len(call.Args) == 2 {
				parts = append(parts, "migrateKV("+te.goText(call.Args[0], src, r)+", "+te.goText(call.Args[1], src, r)+")")
				continue
			}
		}
		parts = append(parts, te.goText(element, src, r))
	}
	path := te.fieldValue(start, end, "string", "migrateClasses("+strings.Join(parts, ", ")+")", fieldDerived, []string{"class"}, src)
	return []kAttr{{kind: attrValue, name: "class", value: `"{` + path + `}"`}}
}

// script converts a <script> element. A static body becomes a raw-text
// literal; Go values become presenter-built JSONValue paths in a script
// body (migration-language-v1 §5).
func (te *tmplEmitter) script(n *parser.ScriptElement) kNode {
	src := te.c.node(te.fe.file, "ScriptElement", n.Range)
	attrs := te.attributes("script", n.Attributes)
	el := &kElement{name: "script", attrs: attrs, src: src}
	static := true
	var text strings.Builder
	for _, content := range n.Contents {
		if content.GoCode != nil {
			static = false
		}
		if content.Value != nil {
			text.WriteString(*content.Value)
		}
	}
	if static {
		switch {
		case strings.TrimSpace(text.String()) == "":
			if text.Len() > 0 {
				el.children = []kNode{&kRaw{text: text.String()}}
			}
		case strings.Contains(text.String(), "```"):
			te.partial(src, n.Range, "raw-text-fence", "the script contains ``` and cannot be a raw-text literal; move it to a static file")
		default:
			el.children = []kNode{&kRaw{text: text.String()}}
		}
		return el
	}
	if !te.view() {
		te.partial(src, n.Range, "script-body-value", "script with Go values in a direct template")
		return el
	}
	var segments []segment
	for _, content := range n.Contents {
		if content.Value != nil {
			segments = append(segments, segment{literal: *content.Value})
		}
		if content.GoCode == nil {
			continue
		}
		expr, typed := te.w.goExpr(content.GoCode.Expression)
		if expr == nil || !typed {
			te.partial(src, n.Range, "untyped-expression", "script value not type-checked")
			continue
		}
		start, end := te.span(expr)
		path := te.fieldValue(start, end, "JSONValue", te.fe.imports.add("github.com/karl-tye/ktml", "ktml")+".MustJSON("+te.goText(expr, src, n.Range)+")", fieldDerived, append([]string{"script"}, hintOf(expr)...), src)
		te.fe.security(src, n.Range, "json", path)
		segments = append(segments, segment{path: path})
	}
	te.review(src, n.Range, "script-body-value", "Go values in a script are JSON built by the presenter; check each path sits in an expression position")
	el.children = []kNode{&kText{segments: segments}}
	return el
}

// leadingLiteral returns the literal text of template parts before the
// first Go value.
func leadingLiteral(parts []templatePart) string {
	var text strings.Builder
	for _, part := range parts {
		if part.expr != nil {
			break
		}
		text.WriteString(part.literal)
	}
	return text.String()
}

// styleKV converts style={ templ.KV("prop: value", on) } with a literal
// style: templ writes the style, trimmed and ending with a semicolon, when
// on, and style="" otherwise. It becomes a conditional literal style, so no
// Go-built CSS is involved.
func (te *tmplEmitter) styleKV(name string, expr ast.Expr, src *Node, r parser.Range) ([]kAttr, bool) {
	call, ok := unparen(expr).(*ast.CallExpr)
	if !ok || len(call.Args) != 2 {
		return nil, false
	}
	if callee, _ := te.w.callee(call, true); callee != templPath+".KV" {
		return nil, false
	}
	style, ok := te.literalText(call.Args[0])
	if !ok || !isBool(te.typeOf(call.Args[1])) {
		return nil, false
	}
	style = strings.TrimSpace(style)
	if style != "" && !strings.HasSuffix(style, ";") {
		style += ";"
	}
	if on, ok := te.literalText(call.Args[1]); ok {
		if on == "true" {
			return []kAttr{{kind: attrValue, name: name, value: ktmlLiteral(style)}}, true
		}
		return []kAttr{{kind: attrValue, name: name, value: ktmlLiteral("")}}, true
	}
	cond, _ := te.condition(call.Args[1], src, r, name)
	return []kAttr{{kind: attrGroup, cond: cond,
		then: []kAttr{{kind: attrValue, name: name, value: ktmlLiteral(style)}},
		els:  []kAttr{{kind: attrValue, name: name, value: ktmlLiteral("")}}}}, true
}

// conditionPath matches a condition that is one boolean path.
var conditionPath = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
