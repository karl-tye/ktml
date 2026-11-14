package alpine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
)

// Type is the literal type of an Alpine value.
type Type struct {
	// Name is boolean, number, string, null, array, object, function,
	// HTMLElement, a KTML type, or unknown.
	Name    string
	Fields  []*Field
	Element *Type
	Params  []string
}

// Field is a property of an object type.
type Field struct {
	Name string
	Type *Type
}

func (t *Type) String() string {
	if t == nil {
		return "unknown"
	}
	switch t.Name {
	case "array":
		if t.Element != nil && t.Element.Name != "unknown" {
			return t.Element.String() + "[]"
		}
		return "array"
	case "object":
		names := make([]string, 0, len(t.Fields))
		for _, field := range t.Fields {
			names = append(names, field.Name+": "+field.Type.String())
		}
		if len(names) > 4 {
			names = append(names[:4], "…")
		}
		return "{ " + strings.Join(names, ", ") + " }"
	case "function":
		return "(" + strings.Join(t.Params, ", ") + ") => …"
	}
	return t.Name
}

func (t *Type) elementType() *Type {
	if t != nil && t.Name == "array" && t.Element != nil {
		return t.Element
	}
	return &Type{Name: "unknown"}
}

// Field returns a field of an object type.
func (t *Type) Field(name string) *Field {
	if t == nil {
		return nil
	}
	for _, field := range t.Fields {
		if field.Name == name {
			return field
		}
	}
	return nil
}

func (a *analyzer) typeOf(node *Node, text *Text) *Type {
	if node == nil {
		return &Type{Name: "unknown"}
	}
	switch node.Kind {
	case Literal:
		return &Type{Name: [...]string{"number", "string", "boolean", "null", "undefined"}[node.Literal]}
	case TemplateLit:
		return &Type{Name: "string"}
	case RegexLit:
		return &Type{Name: "RegExp"}
	case ArrayLit:
		t := &Type{Name: "array"}
		for _, element := range node.Children {
			if element != nil && element.Kind != Spread {
				t.Element = a.typeOf(element, text)
				break
			}
		}
		return t
	case ObjectLit:
		t := &Type{Name: "object"}
		for _, property := range node.Children {
			if property.Kind == Property && !property.Computed && len(property.Children) > 0 {
				t.Fields = append(t.Fields, &Field{Name: property.Name, Type: a.typeOf(property.Children[0], text)})
			}
		}
		return t
	case Arrow, Function:
		t := &Type{Name: "function"}
		for _, param := range node.Params {
			for _, name := range patternNames(param) {
				t.Params = append(t.Params, name.Name)
			}
		}
		return t
	case Paren:
		return a.typeOf(node.Children[0], text)
	case Unary:
		if node.Name == "!" {
			return &Type{Name: "boolean"}
		}
		if node.Name == "-" || node.Name == "+" {
			return &Type{Name: "number"}
		}
	case Binary:
		switch node.Name {
		case "==", "!=", "===", "!==", "<", ">", "<=", ">=", "instanceof", "in":
			return &Type{Name: "boolean"}
		}
	case Hole:
		if path, ok := text.Holes[node.Start]; ok && a.env.PathType != nil {
			switch kind := a.env.PathType(path); kind {
			case "boolean":
				return &Type{Name: "boolean"}
			case "":
			default:
				return &Type{Name: kind + " (from Go)"}
			}
		}
	}
	return &Type{Name: "unknown"}
}

// typeIn types an expression, looking names up in scope.
func (a *analyzer) typeIn(node *Node, text *Text, scope *Scope) *Type {
	switch node.Kind {
	case Ident:
		for ; scope != nil && scope.Kind != OpaqueScope; scope = scope.Parent {
			if binding := scope.lookup(node.Name); binding != nil && binding.Type != nil {
				return binding.Type
			}
		}
		return &Type{Name: "unknown"}
	case Member:
		if field := a.typeIn(node.Children[0], text, scope).Field(node.Name); field != nil {
			return field.Type
		}
		return &Type{Name: "unknown"}
	}
	return a.typeOf(node, text)
}

// env is the lexical environment of JavaScript being resolved.
type env struct {
	expression *Expression
	scope      *Scope
	locals     []map[string]*Binding
	// this is the data scope whose properties this.name reaches.
	this *Scope
	// method is the data scope whose method body is being resolved: its
	// properties are not in scope bare.
	method *Scope
	// methodNode is the function of that method, whose this is the data.
	methodNode *Node
}

func (e *env) push() { e.locals = append(e.locals, make(map[string]*Binding)) }
func (e *env) pop()  { e.locals = e.locals[:len(e.locals)-1] }

func (a *analyzer) resolveAll() {
	for _, expression := range a.doc.Expressions {
		if expression.Program == nil {
			continue
		}
		e := &env{expression: expression, scope: expression.Scope}
		e.push()
		if expression.Directive.Name == "data" && expression.Method != nil {
			a.resolveData(expression, e)
			continue
		}
		e.this = expression.Scope.dataRoot()
		a.resolveNode(expression.Program, e)
	}
	a.reportSiblingScopes()
	for _, swap := range a.swaps {
		a.checkSwap(swap.element, swap.has, swap.scope)
	}
}

// resolveData resolves an x-data object: property values in the parent
// scope, and method bodies with this bound to the new data.
func (a *analyzer) resolveData(expression *Expression, e *env) {
	object := dataObject(expression)
	for _, property := range object.Children {
		if property.Kind != Property || len(property.Children) == 0 {
			a.resolveNode(property, e)
			continue
		}
		value := property.Children[len(property.Children)-1]
		if property.Method || property.Getter || property.Setter || value.Kind == Function {
			e.this, e.method, e.methodNode = expression.Method, expression.Method, value
			a.resolveNode(value, e)
			e.this, e.method, e.methodNode = nil, nil, nil
			continue
		}
		a.resolveNode(value, e)
	}
}

func (a *analyzer) span(e *env, start, end int) (int, int) {
	return e.expression.Text.SourceRange(start, end)
}

func (a *analyzer) declare(e *env, pattern *Node) {
	for _, name := range patternNames(pattern) {
		start, end := a.span(e, name.NameStart, name.NameEnd)
		binding := &Binding{Name: name.Name, Kind: Local, Start: start, End: end, Component: a.component, Type: &Type{Name: "unknown"}}
		e.locals[len(e.locals)-1][name.Name] = binding
		a.doc.Bindings = append(a.doc.Bindings, binding)
	}
}

// resolvePattern resolves the default values and computed keys of a
// pattern, which are expressions.
func (a *analyzer) resolvePattern(pattern *Node, e *env) {
	if pattern == nil {
		return
	}
	switch pattern.Kind {
	case DefaultPattern:
		a.resolvePattern(pattern.Children[0], e)
		a.resolveNode(pattern.Children[1], e)
	case RestPattern:
		a.resolvePattern(pattern.Children[0], e)
	case ArrayPattern, ObjectPattern:
		for _, child := range pattern.Children {
			a.resolvePattern(child, e)
		}
	case Property:
		if pattern.Computed && len(pattern.Children) > 1 {
			a.resolveNode(pattern.Children[0], e)
		}
		if len(pattern.Children) > 0 {
			a.resolvePattern(pattern.Children[len(pattern.Children)-1], e)
		}
	}
}

func (a *analyzer) resolveNode(node *Node, e *env) {
	if node == nil {
		return
	}
	switch node.Kind {
	case Ident:
		a.reference(node, e)
	case Member:
		object := node.Children[0]
		switch {
		case object.Kind == This && e.this != nil:
			a.thisReference(node, e)
		case object.Kind == Ident && object.Name == "$refs":
			a.reference(object, e)
			a.refReference(node, e)
		case object.Kind == Member && object.Name == "$refs" && object.Children[0].Kind == This:
			a.refReference(node, e)
		default:
			a.resolveNode(object, e)
		}
	case Property:
		if node.Computed {
			a.resolveNode(node.Children[0], e)
		}
		if len(node.Children) > 0 {
			a.resolveNode(node.Children[len(node.Children)-1], e)
		}
	case Arrow, Function:
		savedThis := e.this
		if node.Kind == Function && node != e.methodNode {
			// A nested function has its own this.
			e.this = nil
		}
		e.push()
		for _, param := range node.Params {
			a.declare(e, param)
			a.resolvePattern(param, e)
		}
		for _, child := range node.Children {
			a.resolveNode(child, e)
		}
		e.pop()
		e.this = savedThis
	case Block:
		e.push()
		for _, param := range node.Params {
			a.declare(e, param)
		}
		for _, child := range node.Children {
			a.resolveNode(child, e)
		}
		e.pop()
	case VarDecl:
		for _, declarator := range node.Children {
			if len(declarator.Children) > 1 {
				a.resolveNode(declarator.Children[1], e)
			}
			a.resolvePattern(declarator.Children[0], e)
			a.declare(e, declarator.Children[0])
		}
	case Assign:
		target := node.Children[0]
		if target.Kind == ObjectPattern || target.Kind == ArrayPattern {
			for _, name := range patternNames(target) {
				a.reference(name, e)
				a.markWrite()
			}
		} else {
			a.resolveNode(target, e)
			a.markWrite()
		}
		a.resolveNode(node.Children[1], e)
	case Update:
		a.resolveNode(node.Children[0], e)
		a.markWrite()
	case Loop:
		e.push()
		for _, child := range node.Children {
			a.resolveNode(child, e)
		}
		e.pop()
	default:
		for _, child := range node.Children {
			a.resolveNode(child, e)
		}
	}
}

func (a *analyzer) reference(node *Node, e *env) {
	start, end := a.span(e, node.NameStart, node.NameEnd)
	reference := &Reference{Name: node.Name, Start: start, End: end, model: e.expression.Directive.Name == "model"}
	a.doc.References = append(a.doc.References, reference)
	for index := len(e.locals) - 1; index >= 0; index-- {
		if binding := e.locals[index][node.Name]; binding != nil {
			reference.Binding = binding
			return
		}
	}
	if strings.HasPrefix(node.Name, "$") {
		reference.Global = true
		if node.Name == "$event" && !e.expression.Handler {
			a.report(start, end, Warning, "alpine-scope-reference", "$event exists only in x-on handlers, such as @click; %s has no event", e.expression.Attribute.Name)
		}
		return
	}
	for scope := e.scope; scope != nil; scope = scope.Parent {
		if scope.Kind == OpaqueScope {
			reference.Open = true
			break
		}
		if binding := scope.lookup(node.Name); binding != nil {
			reference.Binding = binding
			return
		}
	}
	if e.method != nil && !reference.Open {
		if binding := e.method.lookup(node.Name); binding != nil {
			reference.Open, reference.diagnosed = true, true
			a.report(start, end, Warning, "alpine-missing-this", "%s is a property of this component, but a method sees its properties only through this; write this.%s", node.Name, node.Name)
			return
		}
	}
	// A browser global such as open or name still resolves when a sibling
	// scope declares the name, so reportSiblingScopes checks Builtin too.
	reference.Builtin = globals[node.Name]
	reference.Open = true
}

func (a *analyzer) thisReference(node *Node, e *env) {
	start, end := a.span(e, node.NameStart, node.NameEnd)
	reference := &Reference{Name: node.Name, Kind: ThisReference, Start: start, End: end, model: e.expression.Directive.Name == "model"}
	a.doc.References = append(a.doc.References, reference)
	if strings.HasPrefix(node.Name, "$") {
		reference.Global = true
		return
	}
	// this is the merged data of every enclosing component.
	for scope := e.this; scope != nil; scope = scope.Parent {
		if scope.Kind == OpaqueScope {
			reference.Open = true
			return
		}
		if binding := scope.lookup(node.Name); binding != nil && scope.Kind == DataScope {
			reference.Binding = binding
			return
		}
	}
	reference.Open = true
}

func (a *analyzer) refReference(node *Node, e *env) {
	start, end := a.span(e, node.NameStart, node.NameEnd)
	reference := &Reference{Name: node.Name, Kind: RefReference, Start: start, End: end}
	a.doc.References = append(a.doc.References, reference)
	root := e.scope.dataRoot()
	if e.method != nil {
		root = e.method
	}
	if root == nil {
		reference.Open = true
		return
	}
	a.pendingRefs = append(a.pendingRefs, pendingRef{reference, root, e.expression})
}

type pendingRef struct {
	reference  *Reference
	root       *Scope
	expression *Expression
}

// reportSiblingScopes reports names declared only by scopes that do not
// enclose the use, and $refs names that no x-ref declares.
func (a *analyzer) reportSiblingScopes() {
	for _, pending := range a.pendingRefs {
		reference, root := pending.reference, pending.root
		if binding := root.Refs[reference.Name]; binding != nil {
			reference.Binding = binding
			continue
		}
		reference.Open = true
		if root.DynamicRefs || root.Unknown {
			continue
		}
		a.report(reference.Start, reference.End, Warning, "alpine-unknown-ref", "no element in this x-data has x-ref=\"%s\", so $refs.%s is undefined", reference.Name, reference.Name)
	}
	declared := make(map[string][]*Binding)
	for _, binding := range a.doc.Bindings {
		if binding.Kind == DataProperty || binding.Kind == DataMethod || binding.Kind == DataGetter || binding.Kind == ForItem || binding.Kind == ForIndex {
			declared[binding.Component+"\x00"+binding.Name] = append(declared[binding.Component+"\x00"+binding.Name], binding)
		}
	}
	for _, reference := range a.doc.References {
		if reference.Binding != nil || reference.Global || reference.diagnosed || reference.Kind != IdentifierReference || strings.HasPrefix(reference.Name, "$") {
			continue
		}
		expression := a.expressionAt(reference.Start)
		if expression == nil || a.inMethodOrOpaque(expression) {
			continue
		}
		bindings := declared[expression.Component+"\x00"+reference.Name]
		if len(bindings) == 0 {
			continue
		}
		binding := bindings[0]
		a.report(reference.Start, reference.End, Warning, "alpine-out-of-scope", "%s is declared by the %s at line %d, which does not enclose this element, so it is not in scope here", reference.Name, declarer(binding), a.line(binding.Start))
	}
}

func declarer(binding *Binding) string {
	if binding.Kind == ForItem || binding.Kind == ForIndex {
		return "x-for"
	}
	return "x-data"
}

// inMethodOrOpaque reports whether a reference's expression is resolved
// through an unknown scope before the component root, where an
// out-of-scope finding cannot be sure.
func (a *analyzer) inMethodOrOpaque(expression *Expression) bool {
	for scope := expression.Scope; scope != nil; scope = scope.Parent {
		if scope.Kind == OpaqueScope {
			return true
		}
	}
	return false
}

func (a *analyzer) expressionAt(offset int) *Expression {
	for _, expression := range a.doc.Expressions {
		if offset >= expression.Attribute.P && offset <= expression.Attribute.E {
			return expression
		}
	}
	return nil
}

func (a *analyzer) line(offset int) int {
	return strings.Count(string(a.source[:min(offset, len(a.source))]), "\n") + 1
}

// checkDirectives reports misplaced directives and invalid combinations.
func (a *analyzer) checkDirectives(element *ast.ElementNode, attributes []alpineAttribute, scope *Scope) {
	template := strings.EqualFold(element.Name, "template")
	has := make(map[string]alpineAttribute)
	for _, attribute := range attributes {
		directive, entry := attribute.directive, attribute.entry
		has[directive.Name] = attribute
		info, known := directives[directive.Name]
		nameEnd := entry.P + len(entry.Name)
		if !known {
			if suggestion := closestName(directive.Name, Directives()); suggestion != "" {
				a.report(entry.P, nameEnd, Warning, "alpine-unknown-directive", "x-%s is not an Alpine directive; did you mean x-%s?", directive.Name, suggestion)
			}
			continue
		}
		if info.template && !template {
			a.report(entry.P, nameEnd, Error, "alpine-directive-placement", "x-%s works only on a template element; wrap the element in template %s", directive.Name, entry.Name+`="…"`)
		}
		if directive.Name == "on" && directive.Argument == "" {
			a.report(entry.P, nameEnd, Error, "alpine-directive-placement", "%s names no event; write @click or x-on:click", entry.Name)
		}
		if directive.Name == "bind" && directive.Argument == "" && !directive.Shorthand && entry.Quoted == nil && entry.Value == "" {
			a.report(entry.P, nameEnd, Error, "alpine-directive-placement", "x-bind needs an attribute name or an object value")
		}
		a.checkModifiers(attribute, nameEnd)
	}
	if forAttribute, ok := has["for"]; ok && template {
		if ifAttribute, both := has["if"]; both {
			a.report(ifAttribute.entry.P, ifAttribute.entry.P+len(ifAttribute.entry.Name), Error, "alpine-directive-combination", "x-if and x-for cannot share a template; nest a template x-if inside the x-for template")
		}
		_ = forAttribute
	}
	for _, name := range []string{"for", "if"} {
		if attribute, ok := has[name]; ok && template {
			if count, known := rootElements(element.Children, a.env, a.component); known && count != 1 {
				a.report(element.P, element.P+len(element.Name), Error, "alpine-template-root", "a template with x-%s must contain exactly one root element, not %d", name, count)
			}
			_ = attribute
		}
	}
	for _, attribute := range attributes {
		if attribute.directive.Name == "bind" && attribute.directive.Argument == "key" {
			if _, ok := has["for"]; !ok || !template {
				a.report(attribute.entry.P, attribute.entry.P+len(attribute.entry.Name), Warning, "alpine-directive-combination", ":key has an effect only on a template with x-for")
			}
		}
	}
	a.swaps = append(a.swaps, swapCandidate{element, has, scope})
}

type swapCandidate struct {
	element *ast.ElementNode
	has     map[string]alpineAttribute
	scope   *Scope
}

// markWrite marks the reference just recorded as an assignment target.
func (a *analyzer) markWrite() {
	if count := len(a.doc.References); count > 0 {
		a.doc.References[count-1].Write = true
	}
}

// rootElements counts the root nodes of a template's content, or returns
// false when a component, outlet, condition, or repetition makes the
// count unknown.
func rootElements(children []ast.Node, env Env, component string) (int, bool) {
	count := 0
	for _, child := range children {
		switch value := child.(type) {
		case *ast.ElementNode:
			if env.Component != nil && env.Component(value.Name) || env.Outlet != nil && env.Outlet(component, value.Name) && len(value.Attributes) == 0 && len(value.Children) == 0 {
				return 0, false
			}
			count++
		case *ast.TextNode:
			if value.Quoted != nil {
				for _, segment := range value.Quoted.Segments {
					if segment.Kind == ast.PathSegment || strings.TrimSpace(segment.Literal) != "" {
						count++
						break
					}
				}
			}
		case *ast.HTMLCommentNode:
		default:
			return 0, false
		}
	}
	return count, true
}

func (a *analyzer) checkModifiers(attribute alpineAttribute, nameEnd int) {
	directive := attribute.directive
	if len(directive.Modifiers) == 0 {
		return
	}
	if directive.Name == "on" && keyEvents[directive.Argument] {
		return
	}
	if directive.Name == "transition" || directive.Name == "mask" || directive.Name == "sort" || directive.Name == "intersect" || directive.Name == "anchor" {
		// These modifiers take open-ended values.
		return
	}
	allowed := Modifiers(directive)
	if allowed == nil {
		return
	}
	for index, modifier := range directive.Modifiers {
		if modifier == "" || containsString(allowed, modifier) || modifierValue(modifier) && index > 0 {
			continue
		}
		message := fmt.Sprintf("%s is not a modifier of x-%s", modifier, directive.Name)
		if suggestion := closestName(modifier, allowed); suggestion != "" {
			message += "; did you mean ." + suggestion + "?"
		}
		a.report(attribute.entry.P, nameEnd, Warning, "alpine-unknown-modifier", "%s", message)
		return
	}
}

// modifierValue reports a modifier that is the value of the one before
// it, such as .500ms after .debounce.
func modifierValue(modifier string) bool {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(modifier, "ms"), "s")
	if trimmed == "" {
		return false
	}
	for _, c := range trimmed {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func containsString(list []string, value string) bool {
	for _, candidate := range list {
		if candidate == value {
			return true
		}
	}
	return false
}

// checkSwap reports an x-data element that its own HTMX request replaces:
// the swap discards the element, so Alpine initializes the new one with
// fresh data and the state resets.
func (a *analyzer) checkSwap(element *ast.ElementNode, has map[string]alpineAttribute, scope *Scope) {
	if _, data := has["data"]; !data || scope == nil || scope.Kind != DataScope || len(scope.Bindings) == 0 {
		return
	}
	values := make(map[string]string)
	request := false
	for _, entry := range ast.NamedAttributes(element.Attributes) {
		name := strings.TrimPrefix(strings.ToLower(entry.Name), "data-")
		if text, ok := AttributeText(a.source, entry); ok {
			values[name] = strings.TrimSpace(text.Value)
		} else {
			values[name] = ""
		}
		switch name {
		case "hx-get", "hx-post", "hx-put", "hx-patch", "hx-delete":
			request = true
		}
	}
	if !request {
		return
	}
	target, targeted := values["hx-target"]
	swap := strings.Fields(values["hx-swap"])
	if targeted && target != "this" || len(swap) == 0 || !strings.EqualFold(swap[0], "outerHTML") {
		return
	}
	if strings.Contains(values["hx-ext"], "morph") || values["hx-preserve"] != "" {
		return
	}
	// Only state that changes is lost: a property something in the scope
	// writes, by assignment or x-model.
	written := make(map[string]bool)
	for _, reference := range a.doc.References {
		if reference.Binding != nil && reference.Binding.Scope == scope && reference.Binding.Kind == DataProperty && (reference.Write || reference.model) {
			written[reference.Name] = true
		}
	}
	names := make([]string, 0, len(written))
	for name := range written {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return
	}
	attribute := has["data"].entry
	a.report(attribute.P, attribute.P+len(attribute.Name), Warning, "alpine-swap-resets-state", "hx-swap=\"outerHTML\" replaces this x-data element on every response, so Alpine initializes the new element and %s reset; keep the state on an element outside the swap, or swap with the alpine-morph extension", strings.Join(names, ", "))
}

// closestName returns the candidate within edit distance two of name.
func closestName(name string, candidates []string) string {
	best, bestDistance, ties := "", 3, 0
	for _, candidate := range candidates {
		distance := editDistance(name, candidate)
		switch {
		case distance < bestDistance:
			best, bestDistance, ties = candidate, distance, 0
		case distance == bestDistance:
			ties++
		}
	}
	if ties > 0 || bestDistance == 0 || len(name) < 3 {
		return ""
	}
	return best
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}
