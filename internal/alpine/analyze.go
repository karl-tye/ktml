// Package alpine analyzes the Alpine.js in KTML documents: it parses
// directive values as JavaScript, models the scopes x-data and x-for
// introduce, resolves names to their bindings, and reports what is
// statically clear. A component's outer Alpine scope is never known: a
// caller, slot content, or an HTMX swap can place it inside any x-data, so
// a name that reaches the component root unresolved is not reported.
package alpine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

// Severity is the LSP DiagnosticSeverity.
type Severity int

const (
	Error       Severity = 1
	Warning     Severity = 2
	Information Severity = 3
)

// Diagnostic is one finding at a half-open source range.
type Diagnostic struct {
	Start, End int
	Severity   Severity
	Code       string
	Message    string
}

// BindingKind classifies an Alpine name.
type BindingKind int

const (
	DataProperty BindingKind = iota
	DataMethod
	DataGetter
	ForItem
	ForIndex
	Ref
	Local
)

func (k BindingKind) String() string {
	return [...]string{"property", "method", "getter", "x-for item", "x-for index", "x-ref", "local"}[k]
}

// Binding is a declared Alpine name.
type Binding struct {
	Name       string
	Kind       BindingKind
	Start, End int // the declaring name in source
	Type       *Type
	Scope      *Scope
	// Component is the KTML component whose body declares it.
	Component string
}

// ScopeKind classifies a Scope.
type ScopeKind int

const (
	DataScope ScopeKind = iota
	ForScope
	// OpaqueScope stands for scopes that static analysis cannot see:
	// a non-literal x-data, or the inside of a component call.
	OpaqueScope
)

// Scope is a region where Alpine names are visible.
type Scope struct {
	Kind     ScopeKind
	Parent   *Scope
	Element  *ast.ElementNode
	Bindings []*Binding
	// Refs are the x-ref names of a data scope's subtree.
	Refs map[string]*Binding
	// DynamicRefs reports refs static analysis cannot name.
	DynamicRefs bool
	// Unknown reports component calls, outlets, or slot content in the
	// subtree, which may use or add names.
	Unknown bool
	// Start and End are the source range of the scope's element.
	Start, End int
	Component  string
}

func (s *Scope) lookup(name string) *Binding {
	for _, binding := range s.Bindings {
		if binding.Name == name {
			return binding
		}
	}
	return nil
}

// dataRoot returns the closest data scope at or above s, stopping at
// opaque scopes, or nil.
func (s *Scope) dataRoot() *Scope {
	for scope := s; scope != nil; scope = scope.Parent {
		switch scope.Kind {
		case DataScope:
			return scope
		case OpaqueScope:
			return nil
		}
	}
	return nil
}

// ReferenceKind classifies a Reference.
type ReferenceKind int

const (
	IdentifierReference ReferenceKind = iota
	ThisReference
	RefReference
)

// Reference is one use of an Alpine name.
type Reference struct {
	Name       string
	Kind       ReferenceKind
	Start, End int
	// Binding is the resolved declaration, or nil.
	Binding *Binding
	// Open reports a name that reached an unknown scope unresolved.
	Open bool
	// Global reports an Alpine magic name.
	Global bool
	// Builtin reports a JavaScript or browser global name.
	Builtin bool
	// Write reports an assignment or update target.
	Write bool
	// diagnosed reports a reference that already has a finding; model
	// reports an x-model binding, which writes.
	diagnosed, model bool
}

// Expression is one Alpine attribute value.
type Expression struct {
	Element   *ast.ElementNode
	Attribute *ast.AttributeNode
	Directive Directive
	Text      *Text
	Program   *Node
	// Scope is the scope the value is evaluated in.
	Scope *Scope
	// Handler reports that $event is in scope.
	Handler bool
	// Component is the KTML component whose body holds the attribute.
	Component string
	// Method is the data scope whose methods' this the cursor sees, set for
	// x-data values.
	Method *Scope
}

// Document is the Alpine model of one KTML document.
type Document struct {
	Scopes      []*Scope
	Bindings    []*Binding
	References  []*Reference
	Expressions []*Expression
	Diagnostics []Diagnostic
}

// Env tells the analyzer about KTML names.
type Env struct {
	// Component reports whether an element name is a component call.
	Component func(name string) bool
	// Outlet reports whether name is a content outlet of component.
	Outlet func(component, name string) bool
	// PathType describes the type of an interpolated path.
	PathType func(path *ast.PathExpression) string
}

// SnapshotEnv derives an Env from a compiler snapshot.
func SnapshotEnv(snapshot *compiler.Snapshot, file string) Env {
	types := make(map[[2]int]compiler.Type)
	for _, resolved := range snapshot.Paths(file) {
		types[[2]int{resolved.Range.Start, resolved.Range.End}] = resolved.Type
	}
	return Env{
		Component: func(name string) bool {
			_, ok := snapshot.Resolve(file, name)
			return ok
		},
		Outlet: func(component, name string) bool {
			if name == compiler.ChildrenInput {
				return true
			}
			resolved, ok := snapshot.Resolve(file, component)
			if !ok {
				return false
			}
			for _, input := range resolved.Inputs {
				if input.Name == name && input.IsContent() {
					return true
				}
			}
			return false
		},
		PathType: func(path *ast.PathExpression) string {
			if t, ok := types[[2]int{path.P, path.E}]; ok {
				return t.String()
			}
			return ""
		},
	}
}

// Analyze builds the Alpine model of a document.
func Analyze(source []byte, document *ast.Document, env Env) *Document {
	a := &analyzer{source: source, env: env, doc: &Document{}}
	if document == nil {
		return a.doc
	}
	for _, node := range document.Nodes {
		switch declaration := node.(type) {
		case *ast.ElementDecl:
			a.component = declaration.Name
			a.walk(declaration.Body, nil)
		case *ast.RenderClause:
			a.component = declaration.Name
			a.walk(declaration.Body, nil)
		}
	}
	a.resolveAll()
	sort.SliceStable(a.doc.Diagnostics, func(i, j int) bool { return a.doc.Diagnostics[i].Start < a.doc.Diagnostics[j].Start })
	return a.doc
}

type analyzer struct {
	source      []byte
	env         Env
	doc         *Document
	component   string
	pendingRefs []pendingRef
	swaps       []swapCandidate
}

func (a *analyzer) report(start, end int, severity Severity, code, format string, args ...any) {
	if end <= start {
		end = start + 1
	}
	a.doc.Diagnostics = append(a.doc.Diagnostics, Diagnostic{Start: start, End: end, Severity: severity, Code: code, Message: fmt.Sprintf(format, args...)})
}

func (a *analyzer) newScope(kind ScopeKind, parent *Scope, element *ast.ElementNode) *Scope {
	scope := &Scope{Kind: kind, Parent: parent, Element: element, Component: a.component}
	if element != nil {
		scope.Start, scope.End = element.P, element.E
	}
	a.doc.Scopes = append(a.doc.Scopes, scope)
	return scope
}

// markUnknown records that content static analysis cannot see renders in
// scope and every scope above it in this component.
func markUnknown(scope *Scope) {
	for ; scope != nil && scope.Kind != OpaqueScope; scope = scope.Parent {
		scope.Unknown = true
	}
}

func (a *analyzer) walk(nodes []ast.Node, scope *Scope) {
	for _, node := range nodes {
		switch value := node.(type) {
		case *ast.ConditionNode:
			a.walk(value.Then, scope)
			a.walk(value.Else, scope)
		case *ast.RepetitionNode:
			a.walk(value.Body, scope)
		case *ast.SlotNode:
			markUnknown(scope)
			a.walk(value.Body, a.newScope(OpaqueScope, scope, nil))
		case *ast.ElementNode:
			a.element(value, scope)
		}
	}
}

// alpineAttribute is a named attribute with its parsed directive.
type alpineAttribute struct {
	entry     *ast.AttributeNode
	directive Directive
}

func (a *analyzer) element(element *ast.ElementNode, scope *Scope) {
	if a.env.Component != nil && a.env.Component(element.Name) {
		// The call's content renders inside the component, in scopes the
		// caller cannot see.
		markUnknown(scope)
		inner := a.newScope(OpaqueScope, scope, element)
		a.walk(element.Children, inner)
		return
	}
	if a.env.Outlet != nil && len(element.Attributes) == 0 && len(element.Children) == 0 && a.env.Outlet(a.component, element.Name) {
		markUnknown(scope)
		return
	}
	var attributes []alpineAttribute
	spread := false
	for _, entry := range ast.NamedAttributes(element.Attributes) {
		if directive, ok := ParseDirective(entry.Name); ok {
			attributes = append(attributes, alpineAttribute{entry, directive})
		}
	}
	for _, entry := range element.Attributes {
		if entry.Spread != nil {
			spread = true
		}
	}
	if spread {
		markUnknown(scope)
	}
	elementScope := scope
	for _, attribute := range attributes {
		if attribute.directive.Name == "data" {
			elementScope = a.data(element, attribute, scope)
		}
	}
	a.checkDirectives(element, attributes, elementScope)
	childScope := elementScope
	for _, attribute := range attributes {
		switch attribute.directive.Name {
		case "for":
			childScope = a.forScope(element, attribute, elementScope)
		case "ref":
			a.ref(attribute, elementScope)
		}
	}
	for _, attribute := range attributes {
		name := attribute.directive.Name
		if name == "data" || name == "for" {
			continue
		}
		info, known := directives[name]
		if !known || info.value == plainValue || info.value == noValue {
			if name == "mask" && attribute.directive.Argument == "dynamic" {
				a.expression(element, attribute, elementScope, false)
			}
			continue
		}
		evaluation := elementScope
		if name == "bind" && attribute.directive.Argument == "key" || name == "if" && childScope != elementScope {
			// :key is evaluated per x-for item.
			evaluation = childScope
		}
		a.expression(element, attribute, evaluation, info.value == handlerValue)
	}
	a.walk(element.Children, childScope)
}

// expression parses one directive value and records it.
func (a *analyzer) expression(element *ast.ElementNode, attribute alpineAttribute, scope *Scope, handler bool) *Expression {
	text, ok := AttributeText(a.source, attribute.entry)
	if !ok {
		return nil
	}
	expression := &Expression{Element: element, Attribute: attribute.entry, Directive: attribute.directive, Text: text, Scope: scope, Handler: handler, Component: a.component}
	program, err := ParseProgram(text.Value)
	if err != nil {
		start, end := text.SourceRange(err.Start, min(err.End, len(text.Value)))
		a.report(start, end, Error, "alpine-syntax", "%s: %s", attribute.entry.Name, err.Message)
	}
	expression.Program = program
	a.doc.Expressions = append(a.doc.Expressions, expression)
	return expression
}

// data creates the scope of an x-data element.
func (a *analyzer) data(element *ast.ElementNode, attribute alpineAttribute, parent *Scope) *Scope {
	expression := a.expression(element, attribute, parent, false)
	object := dataObject(expression)
	if object == nil {
		return a.newScope(OpaqueScope, parent, element)
	}
	scope := a.newScope(DataScope, parent, element)
	scope.Refs = make(map[string]*Binding)
	if expression == nil {
		// A valueless x-data declares an empty component.
		return scope
	}
	expression.Method = scope
	for _, property := range object.Children {
		if property.Kind == Spread || property.Computed {
			// Properties static analysis cannot name.
			scope.Kind = OpaqueScope
			continue
		}
		kind := DataProperty
		switch {
		case property.Method:
			kind = DataMethod
		case property.Getter || property.Setter:
			kind = DataGetter
		}
		if scope.lookup(property.Name) != nil {
			continue
		}
		start, end := expression.Text.SourceRange(property.NameStart, property.NameEnd)
		binding := &Binding{Name: property.Name, Kind: kind, Start: start, End: end, Scope: scope, Component: a.component}
		if len(property.Children) > 0 {
			binding.Type = a.typeOf(property.Children[0], expression.Text)
			if property.Getter {
				binding.Type = &Type{Name: "unknown"}
			}
		}
		scope.Bindings = append(scope.Bindings, binding)
		a.doc.Bindings = append(a.doc.Bindings, binding)
	}
	return scope
}

// dataObject returns the object literal of an x-data value, or nil when
// the value is not one.
func dataObject(expression *Expression) *Node {
	if expression == nil {
		return &Node{Kind: ObjectLit}
	}
	if strings.TrimSpace(expression.Text.Value) == "" {
		return &Node{Kind: ObjectLit}
	}
	program := expression.Program
	if program == nil || len(program.Children) != 1 || program.Children[0].Kind != ExprStmt {
		return nil
	}
	value := program.Children[0].Children[0]
	for value.Kind == Paren {
		value = value.Children[0]
	}
	if value.Kind != ObjectLit {
		return nil
	}
	return value
}

func (a *analyzer) forScope(element *ast.ElementNode, attribute alpineAttribute, parent *Scope) *Scope {
	scope := a.newScope(ForScope, parent, element)
	text, ok := AttributeText(a.source, attribute.entry)
	if !ok {
		a.report(attribute.entry.P, attribute.entry.E, Error, "alpine-syntax", "x-for needs a value: item in items")
		return scope
	}
	value := text.Value
	split := forSplit(value)
	if split < 0 && len(text.Holes) > 0 {
		// The whole x-for is built in Go: its names are unknown.
		scope.Kind = OpaqueScope
		return scope
	}
	if split < 0 {
		start, end := text.SourceRange(0, len(value))
		a.report(start, end, Error, "alpine-syntax", "x-for must be item in items or (item, index) in items")
		return scope
	}
	keyword := 2
	head := strings.TrimSpace(value[:split])
	headStart := strings.Index(value, head)
	if strings.HasPrefix(head, "(") && strings.HasSuffix(head, ")") {
		head, headStart = head[1:len(head)-1], headStart+1
	}
	// The collection is evaluated in the template's scope.
	collection := &Expression{Element: element, Attribute: attribute.entry, Directive: attribute.directive, Text: text, Scope: parent, Component: a.component}
	tailStart := split + keyword
	padded := strings.Repeat(" ", tailStart) + value[tailStart:]
	program, err := ParseProgram(padded)
	if err != nil {
		start, end := text.SourceRange(err.Start, min(err.End, len(value)))
		a.report(start, end, Error, "alpine-syntax", "%s: %s", attribute.entry.Name, err.Message)
	}
	collection.Program = program
	a.doc.Expressions = append(a.doc.Expressions, collection)
	collectionType := &Type{Name: "unknown"}
	if program != nil && len(program.Children) == 1 && program.Children[0].Kind == ExprStmt {
		collectionType = a.typeIn(program.Children[0].Children[0], text, parent)
	}
	parts := splitTopLevel(head)
	offset := headStart
	for index, part := range parts {
		trimmed := strings.TrimSpace(part)
		partStart := offset + strings.Index(part, trimmed)
		offset += len(part) + 1
		if trimmed == "" {
			continue
		}
		pattern, err := ParsePattern(strings.Repeat(" ", partStart) + trimmed)
		if err != nil {
			start, end := text.SourceRange(err.Start, min(err.End, len(value)))
			a.report(start, end, Error, "alpine-syntax", "x-for: %s", err.Message)
			continue
		}
		kind := ForItem
		if index > 0 {
			kind = ForIndex
		}
		for _, name := range patternNames(pattern) {
			start, end := text.SourceRange(name.NameStart, name.NameEnd)
			binding := &Binding{Name: name.Name, Kind: kind, Start: start, End: end, Scope: scope, Component: a.component}
			binding.Type = &Type{Name: "number"}
			if kind == ForItem {
				binding.Type = collectionType.elementType()
				if pattern.Kind != Ident {
					binding.Type = &Type{Name: "unknown"}
				}
			}
			if index == 2 {
				binding.Type = &Type{Name: "unknown"}
			}
			scope.Bindings = append(scope.Bindings, binding)
			a.doc.Bindings = append(a.doc.Bindings, binding)
		}
	}
	return scope
}

// forSplit returns the offset of the in or of keyword of an x-for value.
func forSplit(value string) int {
	depth := 0
	for index := 0; index+2 <= len(value); index++ {
		switch value[index] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		}
		if depth != 0 || index == 0 {
			continue
		}
		word := value[index : index+2]
		if (word == "in" || word == "of") && isSpace(value[index-1]) && index+2 < len(value) && isSpace(value[index+2]) {
			return index
		}
	}
	return -1
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// splitTopLevel splits on commas outside brackets.
func splitTopLevel(value string) []string {
	var parts []string
	depth, start := 0, 0
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, value[start:index])
				start = index + 1
			}
		}
	}
	return append(parts, value[start:])
}

// patternNames returns the identifiers a binding pattern declares.
func patternNames(pattern *Node) []*Node {
	if pattern == nil {
		return nil
	}
	switch pattern.Kind {
	case Ident:
		return []*Node{pattern}
	case DefaultPattern, RestPattern:
		return patternNames(pattern.Children[0])
	case ArrayPattern:
		var names []*Node
		for _, element := range pattern.Children {
			names = append(names, patternNames(element)...)
		}
		return names
	case ObjectPattern:
		var names []*Node
		for _, property := range pattern.Children {
			if property.Kind == RestPattern {
				names = append(names, patternNames(property)...)
			} else if len(property.Children) > 0 {
				names = append(names, patternNames(property.Children[len(property.Children)-1])...)
			}
		}
		return names
	}
	return nil
}

func (a *analyzer) ref(attribute alpineAttribute, scope *Scope) {
	root := scope.dataRoot()
	if root == nil {
		return
	}
	text, ok := AttributeText(a.source, attribute.entry)
	if !ok || len(text.Holes) > 0 {
		root.DynamicRefs = true
		return
	}
	name := strings.TrimSpace(text.Value)
	if name == "" || root.Refs[name] != nil {
		return
	}
	offset := strings.Index(text.Value, name)
	start, end := text.SourceRange(offset, offset+len(name))
	binding := &Binding{Name: name, Kind: Ref, Start: start, End: end, Scope: root, Component: a.component, Type: &Type{Name: "HTMLElement"}}
	root.Refs[name] = binding
	a.doc.Bindings = append(a.doc.Bindings, binding)
}
