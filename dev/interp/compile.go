package interp

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/rt"
)

// component is one compiled Component Signature. Its first len(inputs) frame
// slots hold the inputs in signature order; clause bindings and repetition
// items take the slots after them.
type component struct {
	id       compiler.SymbolID
	name     string
	goID     string
	file     string
	inputs   []input
	slots    int
	body     []op
	dispatch *dispatchOp
}

// input is one component input as its frame slot sees it.
type input struct {
	name     string
	goName   string
	typ      compiler.Type
	mode     ast.InputMode
	fallback reflect.Value
}

func (info input) isContent() bool { return info.typ.Kind == compiler.ContentTypeKind }

// local is a path root visible where an operation is compiled: an input, a
// clause binding, or a repetition item.
type local struct {
	slot int
	typ  compiler.Type
	mode ast.InputMode
}

type scope map[string]local

func (s scope) with(name string, value local) scope {
	extended := make(scope, len(s)+1)
	for key, existing := range s {
		extended[key] = existing
	}
	extended[name] = value
	return extended
}

// unit compiles the components of one Document.
type unit struct {
	snapshot    *compiler.Snapshot
	options     Options
	file        string
	importPaths map[string]string
	visible     map[string]compiler.Component
	model       *component
	repetitions int
	// instanceRoot is the root element of the single-root body being
	// compiled, which carries the instance attribute in development.
	instanceRoot *ast.ElementNode
}

func compileDocument(snapshot *compiler.Snapshot, file string, options Options, importPaths map[string]string) ([]*component, error) {
	document := snapshot.Document(file)
	if document == nil {
		return nil, fmt.Errorf("interp: %s is not a loaded component document", file)
	}
	u := &unit{snapshot: snapshot, options: options, file: file, importPaths: importPaths, visible: make(map[string]compiler.Component)}
	for _, visible := range snapshot.VisibleComponents(file) {
		u.visible[visible.Name] = visible
	}
	var compiled []*component
	for _, node := range document.Nodes {
		declaration, ok := node.(*ast.ElementDecl)
		if !ok {
			continue
		}
		model, err := u.component(declaration)
		if err != nil {
			return nil, fmt.Errorf("interp: component %s: %w", declaration.Name, err)
		}
		compiled = append(compiled, model)
	}
	return compiled, nil
}

// goID is the canonical "<import path>.<Name>" component ID that generated
// code uses in render errors.
func (u *unit) goID(file, name string) (string, error) {
	dir := filepath.Dir(file)
	importPath, ok := u.importPaths[dir]
	if !ok {
		var err error
		importPath, err = u.options.Packages(dir)
		if err != nil {
			return "", fmt.Errorf("resolve Go package for %s: %w", dir, err)
		}
		u.importPaths[dir] = importPath
	}
	return importPath + "." + compiler.GeneratedGoField(name), nil
}

func (u *unit) component(declaration *ast.ElementDecl) (*component, error) {
	for _, attribute := range declaration.Attributes {
		if attribute.Kind == ast.OverrideDeclaration {
			return nil, fmt.Errorf("declaration overrides are not supported")
		}
	}
	semantic, ok := u.visible[declaration.Name]
	if !ok {
		return nil, fmt.Errorf("not in the workspace snapshot")
	}
	goID, err := u.goID(semantic.File, semantic.Name)
	if err != nil {
		return nil, err
	}
	model := &component{id: semantic.ID, name: semantic.Name, goID: goID, file: semantic.File}
	u.model, u.repetitions = model, 0
	roots := make(scope, len(semantic.Inputs))
	for _, declared := range semantic.Inputs {
		info, err := semanticInput(declared)
		if err != nil {
			return nil, err
		}
		mode := info.mode
		if mode == ast.DefaultedInput {
			// The frame holds the resolved value of a defaulted input.
			mode = ast.RequiredInput
		}
		roots[info.name] = local{slot: len(model.inputs), typ: info.typ, mode: mode}
		model.inputs = append(model.inputs, info)
	}
	model.slots = len(model.inputs)
	if declaration.IsSignature() {
		group, ok := u.snapshot.ClauseGroup(semantic.ID)
		if !ok {
			return nil, fmt.Errorf("signature has no validated render clauses")
		}
		model.dispatch, err = u.dispatch(&group, roots)
		return model, err
	}
	model.body, err = u.componentBody(declaration.Body, roots)
	return model, err
}

// componentBody compiles one default or clause body. In development the
// instance is marked exactly as the generated ktmldev variant marks it.
func (u *unit) componentBody(nodes []ast.Node, roots scope) ([]op, error) {
	if !u.options.Development {
		return u.body(nodes, roots)
	}
	markup := func(element *ast.ElementNode) bool {
		if info, outlet := roots[element.Name]; outlet && info.typ.Kind == compiler.ContentTypeKind {
			return false
		}
		_, isComponent := u.visible[element.Name]
		return !isComponent
	}
	switch u.snapshot.InstanceShape(u.file, nodes, markup) {
	case compiler.DocumentInstance:
		return u.body(nodes, roots)
	case compiler.SingleRootInstance:
		u.instanceRoot = nodes[0].(*ast.ElementNode)
		ops, err := u.body(nodes, roots)
		u.instanceRoot = nil
		return ops, err
	}
	var b builder
	b.add(instanceOp{kind: beginInstance})
	for _, node := range nodes {
		if err := u.node(&b, node, roots); err != nil {
			return nil, err
		}
	}
	b.add(instanceOp{kind: endInstance})
	return b.done(), nil
}

func semanticInput(declared compiler.Input) (input, error) {
	info := input{name: declared.Name, goName: compiler.GeneratedGoField(declared.Name), typ: declared.Type, mode: ast.InputMode(declared.Mode)}
	if info.mode == ast.DefaultedInput && !info.isContent() {
		if declared.Default == nil {
			return input{}, fmt.Errorf("input %s: missing scalar default", declared.Name)
		}
		value, err := scalarValue(declared.Default, declared.Type)
		if err != nil {
			return input{}, fmt.Errorf("input %s: %w", declared.Name, err)
		}
		info.fallback = value
	}
	return info, nil
}

func scalarValue(literal *compiler.ScalarValue, target compiler.Type) (reflect.Value, error) {
	switch {
	case target.IsScalar(compiler.StringType) && literal.Kind == compiler.StringLiteral:
		return reflect.ValueOf(literal.String), nil
	case target.IsScalar(compiler.BooleanType) && literal.Kind == compiler.BooleanLiteral:
		return reflect.ValueOf(literal.Boolean), nil
	case target.IsScalar(compiler.NumberType) && literal.Kind == compiler.NumberLiteral:
		return reflect.ValueOf(literal.Number), nil
	case target.IsScalar(compiler.IntegerType) && literal.Kind == compiler.IntegerLiteral:
		return reflect.ValueOf(literal.Integer), nil
	default:
		return reflect.Value{}, fmt.Errorf("default requires %s", target)
	}
}

// slot allocates a frame slot for a binding or repetition item.
func (u *unit) slot() int {
	u.model.slots++
	return u.model.slots - 1
}

// builder collects the operations of one body. Adjacent static markup is
// coalesced into one write exactly where the generator coalesces it: any
// other operation ends the run.
type builder struct {
	ops     []op
	pending strings.Builder
}

func (b *builder) static(markup string) { b.pending.WriteString(markup) }

func (b *builder) add(operation op) {
	b.flush()
	b.ops = append(b.ops, operation)
}

func (b *builder) flush() {
	if b.pending.Len() == 0 {
		return
	}
	b.ops = append(b.ops, staticOp(b.pending.String()))
	b.pending.Reset()
}

func (b *builder) done() []op {
	b.flush()
	return b.ops
}

func (u *unit) body(nodes []ast.Node, roots scope) ([]op, error) {
	var b builder
	for _, node := range nodes {
		if err := u.node(&b, node, roots); err != nil {
			return nil, err
		}
	}
	return b.done(), nil
}

func (u *unit) node(b *builder, node ast.Node, roots scope) error {
	switch value := node.(type) {
	case *ast.TextNode:
		return u.interpolated(b, value.Quoted, value.Value, roots, false)
	case *ast.RepetitionNode:
		return u.repetition(b, value, roots)
	case *ast.ConditionNode:
		return u.condition(b, value, roots)
	case *ast.SlotNode:
		return fmt.Errorf("slot %s must be a direct body node of a component call", value.Name)
	case *ast.DoctypeNode:
		if value.Name != "html" {
			return fmt.Errorf("doctype %s is not supported", value.Name)
		}
		b.static("<!doctype html>")
		return nil
	case *ast.HTMLCommentNode:
		b.static("<!--" + value.Text + "-->")
		return nil
	case *ast.RawTextNode:
		return fmt.Errorf("a raw-text literal is only the content of a script or style element")
	case *ast.ElementNode:
		if info, outlet := roots[value.Name]; outlet && info.typ.Kind == compiler.ContentTypeKind {
			if len(value.Attributes) > 0 || len(value.Children) > 0 {
				return fmt.Errorf("content outlet %s cannot have attributes or a body", value.Name)
			}
			site, _ := u.snapshot.OutletSource(value)
			b.add(outletOp{slot: info.slot, site: site})
			return nil
		}
		if _, isComponent := u.visible[value.Name]; isComponent {
			return u.call(b, value, roots)
		}
		return u.element(b, value, roots)
	default:
		return fmt.Errorf("unsupported node %T", node)
	}
}

// path compiles a typed dotted path. With presence set the last part is not
// read, so a presence test sees its pointer or content value.
func (u *unit) path(expression *ast.PathExpression, roots scope) (pathRef, compiler.Type, error) {
	root := expression.Parts[0].Name
	info, ok := roots[root]
	if !ok {
		return pathRef{}, compiler.Type{}, fmt.Errorf("references unknown input %s", root)
	}
	ref := pathRef{slot: info.slot}
	current := info.typ
	for _, part := range expression.Parts[1:] {
		if current.Kind != compiler.RecordTypeKind {
			return pathRef{}, compiler.Type{}, fmt.Errorf("path %s: %s has no field %s", expression.String(), current, part.Name)
		}
		record, _ := u.snapshot.Record(current.Record)
		field, ok := record.Field(part.Name)
		if !ok {
			return pathRef{}, compiler.Type{}, fmt.Errorf("path %s: record %s has no field %s", expression.String(), record.Name, part.Name)
		}
		ref.fields = append(ref.fields, field.GoName)
		current = field.Type
	}
	return ref, current, nil
}

func (u *unit) element(b *builder, element *ast.ElementNode, roots scope) error {
	b.static("<" + element.Name)
	if element == u.instanceRoot {
		u.instanceRoot = nil
		b.add(instanceOp{kind: instanceAttr})
	}
	if err := u.attributes(b, element.Attributes, ownedNames(element.Attributes), roots); err != nil {
		return err
	}
	b.static(">")
	if compiler.VoidElement(element.Name) {
		if len(element.Children) > 0 {
			return fmt.Errorf("void element %s cannot have children", element.Name)
		}
		return nil
	}
	if compiler.RawTextElement(element.Name) {
		if err := u.rawTextBody(b, element, roots); err != nil {
			return err
		}
	} else {
		for _, child := range element.Children {
			if err := u.node(b, child, roots); err != nil {
				return err
			}
		}
	}
	b.static("</" + element.Name + ">")
	return nil
}

// scriptBody compiles the quoted text of a script element: static
// JavaScript byte for byte, and JSONValue, boolean, and TrustedScript paths
// through their script-body writers.
func (u *unit) scriptBody(b *builder, element string, text *ast.TextNode, roots scope) error {
	if text.Quoted == nil {
		return fmt.Errorf("%s content has no quoted value", element)
	}
	for _, segment := range text.Quoted.Segments {
		if segment.Kind == ast.LiteralSegment {
			if end := compiler.RawTextEndTag(element, segment.Literal); end != "" {
				return fmt.Errorf("%s content cannot contain %s", element, end)
			}
			b.static(segment.Literal)
			continue
		}
		ref, pathType, err := u.path(segment.Path, roots)
		if err != nil {
			return fmt.Errorf("%s content: %w", element, err)
		}
		var write writeFunc
		switch {
		case pathType.IsScalar(compiler.BooleanType):
			write = writeScriptBool
		case isContextual(pathType, compiler.JSONValueType):
			write = func(w *rt.Writer, v reflect.Value) error { return rt.JSONScript(w, v.Interface().(ktml.JSONValue)) }
		case isContextual(pathType, compiler.TrustedScriptType):
			write = func(w *rt.Writer, v reflect.Value) error { return rt.Script(w, v.Interface().(ktml.TrustedScript)) }
		default:
			return fmt.Errorf("%s content cannot render %s of type %s; build it in Go as ktml.JSONValue or ktml.TrustedScript", element, segment.Path.String(), pathType)
		}
		b.add(valueOp{path: ref, write: write})
	}
	return nil
}

// rawTextBody compiles the content of a raw-text element: raw-text literals
// of script and style, written byte for byte.
func (u *unit) rawTextBody(b *builder, element *ast.ElementNode, roots scope) error {
	for _, child := range element.Children {
		if text, ok := child.(*ast.TextNode); ok && strings.EqualFold(element.Name, "script") {
			if err := u.scriptBody(b, element.Name, text, roots); err != nil {
				return err
			}
			continue
		}
		raw, ok := child.(*ast.RawTextNode)
		if !ok || !compiler.RawTextBodyElement(element.Name) {
			return fmt.Errorf("%s content is raw text, which the interpreter cannot emit from %T", element.Name, child)
		}
		if end := compiler.RawTextEndTag(element.Name, raw.Text); end != "" {
			return fmt.Errorf("%s content cannot contain %s", element.Name, end)
		}
		b.static(raw.Text)
	}
	return nil
}

// attributes compiles an element's attribute entries in source order. A
// conditional group compiles to an ifOp whose branches see the paths their
// condition narrows.
func (u *unit) attributes(b *builder, entries []*ast.AttributeNode, owned []string, roots scope) error {
	for _, attribute := range entries {
		if attribute.Condition != nil {
			if err := u.attributeCondition(b, attribute.Condition, owned, roots); err != nil {
				return err
			}
			continue
		}
		if attribute.Spread != nil {
			if err := u.spread(b, attribute.Spread, owned, roots); err != nil {
				return err
			}
			continue
		}
		name, value := attribute.Name, quotedLiteral(attribute.Quoted, attribute.Value)
		if hasPath(attribute.Quoted) && compiler.ScriptAttribute(name) {
			if err := u.scriptAttribute(b, name, attribute.Quoted, roots); err != nil {
				return err
			}
			continue
		}
		if hasPath(attribute.Quoted) && compiler.StyleAttribute(name) {
			if err := u.styleAttribute(b, name, attribute.Quoted, roots); err != nil {
				return err
			}
			continue
		}
		if hasPath(attribute.Quoted) && compiler.URLAttribute(name) {
			if err := u.urlAttribute(b, name, attribute.Quoted, roots); err != nil {
				return err
			}
			continue
		}
		if path, ok := exactPath(attribute.Quoted); ok {
			ref, pathType, err := u.path(path, roots)
			if err != nil {
				return fmt.Errorf("attribute %s: %w", name, err)
			}
			if pathType.IsScalar(compiler.BooleanType) {
				var then, otherwise builder
				if compiler.ARIAAttribute(name) {
					then.static(" " + name + "=\"true\"")
					otherwise.static(" " + name + "=\"false\"")
				} else {
					then.static(" " + name)
				}
				b.add(ifOp{test: ref.boolean, then: then.done(), otherwise: otherwise.done()})
				continue
			}
			write, ok := attributeWriter(pathType)
			if !ok {
				return fmt.Errorf("attribute %s cannot render %s of type %s", name, path.String(), pathType)
			}
			b.static(" " + name + "=\"")
			b.add(valueOp{path: ref, write: write})
			b.static("\"")
			continue
		}
		if hasPath(attribute.Quoted) {
			b.static(" " + name + "=\"")
			if err := u.interpolated(b, attribute.Quoted, attribute.Value, roots, true); err != nil {
				return fmt.Errorf("attribute %s: %w", name, err)
			}
			b.static("\"")
			continue
		}
		// Only a valueless attribute is a static boolean attribute.
		if attribute.Value == "" {
			b.static(" " + name)
			continue
		}
		b.static(" " + name + "=\"" + rt.EscapeString(value) + "\"")
	}
	return nil
}

func (u *unit) attributeCondition(b *builder, group *ast.AttributeCondition, owned []string, roots scope) error {
	test, err := u.test(group.Condition, roots)
	if err != nil {
		return err
	}
	var then, otherwise builder
	if err := u.attributes(&then, group.Then, owned, roots); err != nil {
		return err
	}
	if err := u.attributes(&otherwise, group.Else, owned, roots); err != nil {
		return err
	}
	b.add(ifOp{test: test, then: then.done(), otherwise: otherwise.done()})
	return nil
}

// scriptAttribute compiles a script attribute with interpolated values: paths
// must be TrustedScript, JSONValue, or boolean (the JavaScript literal).
func (u *unit) scriptAttribute(b *builder, name string, quoted *ast.QuotedValue, roots scope) error {
	b.static(" " + name + "=\"")
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.LiteralSegment {
			b.static(rt.EscapeString(segment.Literal))
			continue
		}
		ref, pathType, err := u.path(segment.Path, roots)
		if err != nil {
			return fmt.Errorf("attribute %s: %w", name, err)
		}
		var write writeFunc
		switch {
		case pathType.IsScalar(compiler.BooleanType):
			write = writeScriptBool
		case isContextual(pathType, compiler.TrustedScriptType):
			write = writeScriptAttr
		case isContextual(pathType, compiler.JSONValueType):
			write = writeJSONAttr
		default:
			return fmt.Errorf("script attribute %s cannot render %s of type %s; build it in Go as ktml.JSONValue or ktml.TrustedScript", name, segment.Path.String(), pathType)
		}
		b.add(valueOp{path: ref, write: write})
	}
	b.static("\"")
	return nil
}

// urlAttribute compiles a URL attribute with interpolated values: one whole
// URL or TrustedURL, or literal text that fixes the scheme and host
// (compiler.SafeURLPrefix) followed by string, integer, and number paths.
func (u *unit) urlAttribute(b *builder, name string, quoted *ast.QuotedValue, roots scope) error {
	if path, ok := exactPath(quoted); ok {
		ref, pathType, err := u.path(path, roots)
		if err != nil {
			return fmt.Errorf("attribute %s: %w", name, err)
		}
		if !isContextual(pathType, compiler.URLType) && !isContextual(pathType, compiler.TrustedURLType) {
			return fmt.Errorf("URL attribute %s cannot render %s of type %s; build it in Go as ktml.URL or ktml.TrustedURL", name, path.String(), pathType)
		}
		write, _ := attributeWriter(pathType)
		b.static(" " + name + "=\"")
		b.add(valueOp{path: ref, write: write})
		b.static("\"")
		return nil
	}
	if !compiler.SafeURLPrefix(quoted) {
		return fmt.Errorf("URL attribute %s: literal text before the first path must fix the scheme and host", name)
	}
	for _, segment := range quoted.Segments {
		if segment.Kind != ast.PathSegment {
			continue
		}
		_, pathType, err := u.path(segment.Path, roots)
		if err != nil {
			return fmt.Errorf("attribute %s: %w", name, err)
		}
		if pathType.Kind != compiler.ScalarTypeKind || pathType.IsScalar(compiler.BooleanType) {
			return fmt.Errorf("URL attribute %s cannot render %s of type %s after its literal text", name, segment.Path.String(), pathType)
		}
	}
	b.static(" " + name + "=\"")
	if err := u.interpolated(b, quoted, "", roots, true); err != nil {
		return fmt.Errorf("attribute %s: %w", name, err)
	}
	b.static("\"")
	return nil
}

// styleAttribute compiles the style attribute with interpolated values:
// paths must be CSSValue.
func (u *unit) styleAttribute(b *builder, name string, quoted *ast.QuotedValue, roots scope) error {
	b.static(" " + name + "=\"")
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.LiteralSegment {
			b.static(rt.EscapeString(segment.Literal))
			continue
		}
		ref, pathType, err := u.path(segment.Path, roots)
		if err != nil {
			return fmt.Errorf("attribute %s: %w", name, err)
		}
		if !isContextual(pathType, compiler.CSSValueType) {
			return fmt.Errorf("style attribute %s cannot render %s of type %s; build it in Go as ktml.CSSValue", name, segment.Path.String(), pathType)
		}
		b.add(valueOp{path: ref, write: writeCSS})
	}
	b.static("\"")
	return nil
}

func writeCSS(w *rt.Writer, v reflect.Value) error {
	return rt.CSS(w, v.Interface().(ktml.CSSValue))
}

// interpolated compiles a quoted value as text, or as the content of a
// double-quoted attribute value when attribute is set.
func (u *unit) interpolated(b *builder, quoted *ast.QuotedValue, raw string, roots scope, attribute bool) error {
	if quoted != nil {
		for _, segment := range quoted.Segments {
			switch segment.Kind {
			case ast.LiteralSegment:
				if segment.Literal != "" {
					b.static(rt.EscapeString(segment.Literal))
				}
			case ast.PathSegment:
				if segment.Path == nil || len(segment.Path.Parts) == 0 {
					return fmt.Errorf("empty interpolation path")
				}
				ref, pathType, err := u.path(segment.Path, roots)
				if err != nil {
					return fmt.Errorf("text %w", err)
				}
				write, ok := scalarWriter(pathType, attribute)
				if !ok {
					return fmt.Errorf("text input %s has unsupported type %s", segment.Path.String(), pathType)
				}
				b.add(valueOp{path: ref, write: write})
			}
		}
		return nil
	}
	value := unquote(raw)
	for {
		start, end, name, ok := nextReference(value, roots)
		if !ok {
			if value != "" {
				b.static(rt.EscapeString(value))
			}
			return nil
		}
		if start > 0 {
			b.static(rt.EscapeString(value[:start]))
		}
		info := roots[name]
		if info.mode == ast.OptionalInput {
			return fmt.Errorf("optional input %s must be narrowed before value access", name)
		}
		write, ok := scalarWriter(info.typ, attribute)
		if !ok {
			return fmt.Errorf("text input %s has unsupported type %s", name, info.typ)
		}
		b.add(valueOp{path: pathRef{slot: info.slot}, write: write})
		value = value[end+1:]
	}
}

func (u *unit) condition(b *builder, node *ast.ConditionNode, roots scope) error {
	test, err := u.test(node.Condition, roots)
	if err != nil {
		return err
	}
	then, err := u.body(node.Then, roots)
	if err != nil {
		return err
	}
	otherwise, err := u.body(node.Else, roots)
	if err != nil {
		return err
	}
	b.add(ifOp{test: test, then: then, otherwise: otherwise})
	return nil
}

// test compiles a condition. And and or short-circuit as in Go.
func (u *unit) test(condition *ast.ConditionExpr, roots scope) (func(*state) bool, error) {
	switch condition.Kind {
	case ast.PathCondition:
		ref, typ, err := u.path(condition.Path, roots)
		if err != nil {
			return nil, fmt.Errorf("condition %s: %w", condition.Path.String(), err)
		}
		if !typ.IsScalar(compiler.BooleanType) {
			return nil, fmt.Errorf("condition %s is %s, not boolean", condition.Path.String(), typ)
		}
		return ref.boolean, nil
	case ast.PresenceCondition:
		ref, _, err := u.path(condition.Path, roots)
		if err != nil {
			return nil, fmt.Errorf("condition %s?: %w", condition.Path.String(), err)
		}
		return ref.present, nil
	case ast.NotCondition:
		operand, err := u.test(condition.Operands[0], roots)
		if err != nil {
			return nil, err
		}
		return func(s *state) bool { return !operand(s) }, nil
	case ast.GroupCondition:
		return u.test(condition.Operands[0], roots)
	case ast.AndCondition, ast.OrCondition:
		operands := make([]func(*state) bool, len(condition.Operands))
		for index, operand := range condition.Operands {
			var err error
			if operands[index], err = u.test(operand, roots); err != nil {
				return nil, err
			}
		}
		if condition.Kind == ast.AndCondition {
			return func(s *state) bool {
				for _, operand := range operands {
					if !operand(s) {
						return false
					}
				}
				return true
			}, nil
		}
		return func(s *state) bool {
			for _, operand := range operands {
				if operand(s) {
					return true
				}
			}
			return false
		}, nil
	}
	return nil, fmt.Errorf("unsupported condition %q", condition.Kind)
}

func (u *unit) repetition(b *builder, node *ast.RepetitionNode, roots scope) error {
	spelling := node.Collection.String()
	repetition, ok := u.snapshot.RepetitionFor(node)
	if !ok || repetition.KeyType == "" || repetition.Collection.Type.Element == nil {
		return fmt.Errorf("keyed repetition over %s has no validated collection and key", spelling)
	}
	collection, _, err := u.path(node.Collection, roots)
	if err != nil {
		return fmt.Errorf("keyed repetition collection: %w", err)
	}
	u.repetitions++
	item := u.slot()
	locals := roots.with(node.Item, local{slot: item, typ: *repetition.Collection.Type.Element, mode: ast.RequiredInput})
	key, keyType, err := u.path(node.Key, locals)
	if err != nil {
		return fmt.Errorf("keyed repetition key: %w", err)
	}
	integer := keyType.IsScalar(compiler.IntegerType)
	if !integer && !keyType.IsScalar(compiler.StringType) {
		return fmt.Errorf("keyed repetition key %s has unsupported type %s", node.Key.String(), keyType)
	}
	body, err := u.body(node.Body, locals)
	if err != nil {
		return err
	}
	b.add(&repetitionOp{collection: collection, item: item, key: key, integer: integer, source: spelling, identity: repetition.Source, body: body})
	return nil
}

func (u *unit) call(b *builder, call *ast.ElementNode, roots scope) error {
	target := u.visible[call.Name]
	targetInputs := make(map[string]compiler.Input, len(target.Inputs))
	for _, declared := range target.Inputs {
		targetInputs[declared.Name] = declared
	}
	site, _ := u.snapshot.CallSource(call)
	operation := &callOp{target: target.ID, name: call.Name, site: site}
	supplied := make(map[string]bool, len(call.Attributes))
	for _, attribute := range call.Attributes {
		if !attribute.IsNamed() {
			return fmt.Errorf("component %s inputs cannot be conditional or spread", call.Name)
		}
		declared, ok := targetInputs[attribute.Name]
		if !ok {
			return fmt.Errorf("component %s has no input %s", call.Name, attribute.Name)
		}
		if declared.IsContent() {
			return fmt.Errorf("component %s content input %s cannot be supplied as an attribute", call.Name, attribute.Name)
		}
		if supplied[attribute.Name] {
			return fmt.Errorf("component %s input %s is supplied more than once", call.Name, attribute.Name)
		}
		supplied[attribute.Name] = true
		value, err := u.argument(call.Name, attribute, declared, roots)
		if err != nil {
			return err
		}
		operation.arguments = append(operation.arguments, value)
	}
	if err := u.callContent(operation, call, target, roots); err != nil {
		return err
	}
	for _, declared := range target.Inputs {
		if !supplied[declared.Name] && declared.Mode == compiler.RequiredInput && !declared.IsContent() {
			return fmt.Errorf("component %s requires input %s", call.Name, declared.Name)
		}
	}
	b.add(operation)
	return nil
}

func (u *unit) argument(callee string, attribute *ast.AttributeNode, declared compiler.Input, roots scope) (argument, error) {
	if attribute.BagRange != nil {
		return u.bagArgument(callee, attribute, declared, roots)
	}
	value := argument{name: attribute.Name}
	if path, ok := exactPath(attribute.Quoted); ok {
		ref, pathType, err := u.path(path, roots)
		if err != nil {
			return argument{}, fmt.Errorf("component %s input %s: %w", callee, attribute.Name, err)
		}
		if !pathType.Equal(declared.Type) {
			return argument{}, fmt.Errorf("component %s input %s requires %s, got %s", callee, attribute.Name, declared.Type, pathType)
		}
		value.path = &ref
		return value, nil
	}
	if declared.Type.IsScalar(compiler.StringType) && hasPath(attribute.Quoted) {
		for _, segment := range attribute.Quoted.Segments {
			switch segment.Kind {
			case ast.LiteralSegment:
				if segment.Literal != "" {
					value.concat = append(value.concat, part{literal: segment.Literal})
				}
			case ast.PathSegment:
				ref, pathType, err := u.path(segment.Path, roots)
				if err != nil {
					return argument{}, fmt.Errorf("component %s input %s: %w", callee, attribute.Name, err)
				}
				// Integers and numbers are formatted as text writes them.
				format := func(v reflect.Value) string { return v.String() }
				switch {
				case pathType.IsScalar(compiler.StringType):
				case pathType.IsScalar(compiler.IntegerType):
					format = func(v reflect.Value) string { return rt.FormatInt(v.Int()) }
				case pathType.IsScalar(compiler.NumberType):
					format = func(v reflect.Value) string { return rt.FormatNumber(v.Float()) }
				default:
					return argument{}, fmt.Errorf("component %s input %s: path %s has unsupported type %s in a string value", callee, attribute.Name, segment.Path.String(), pathType)
				}
				value.concat = append(value.concat, part{path: &ref, format: format})
			}
		}
		value.isConcat = true
		return value, nil
	}
	if declared.Type.Kind != compiler.ScalarTypeKind {
		return argument{}, fmt.Errorf("component %s input %s requires %s", callee, attribute.Name, declared.Type)
	}
	literal := quotedLiteral(attribute.Quoted, attribute.Value)
	switch declared.Type.Scalar {
	case compiler.StringType:
		value.literal = reflect.ValueOf(literal)
	case compiler.BooleanType:
		if literal != "true" && literal != "false" {
			return argument{}, fmt.Errorf("component %s input %s requires a boolean", callee, attribute.Name)
		}
		value.literal = reflect.ValueOf(literal == "true")
	case compiler.NumberType:
		number, err := strconv.ParseFloat(literal, 64)
		if err != nil {
			return argument{}, fmt.Errorf("component %s input %s requires a number", callee, attribute.Name)
		}
		value.literal = reflect.ValueOf(number)
	case compiler.IntegerType:
		integer, err := strconv.ParseInt(literal, 10, 64)
		if err != nil {
			return argument{}, fmt.Errorf("component %s input %s requires an integer", callee, attribute.Name)
		}
		value.literal = reflect.ValueOf(integer)
	}
	return value, nil
}

// callContent compiles a call's unlabelled children and slot fills, each into
// one fill closure created when the call runs.
func (u *unit) callContent(operation *callOp, call *ast.ElementNode, target compiler.Component, roots scope) error {
	var children []ast.Node
	fills := make(map[string]*ast.SlotNode)
	declaredContent := make(map[string]compiler.Input)
	for _, declared := range target.Inputs {
		if declared.IsContent() {
			declaredContent[declared.Name] = declared
		}
	}
	for _, child := range call.Children {
		slot, fill := child.(*ast.SlotNode)
		if !fill {
			children = append(children, child)
			continue
		}
		if _, ok := declaredContent[slot.Name]; !ok || slot.Name == compiler.ChildrenInput {
			return fmt.Errorf("component %s has no slot %s", call.Name, slot.Name)
		}
		if _, duplicate := fills[slot.Name]; duplicate {
			return fmt.Errorf("component %s slot %s is filled more than once", call.Name, slot.Name)
		}
		fills[slot.Name] = slot
	}
	for _, declared := range target.Inputs {
		if !declared.IsContent() {
			continue
		}
		var body []ast.Node
		present := false
		if declared.Name == compiler.ChildrenInput {
			body, present = children, len(children) > 0
		} else if slot, ok := fills[declared.Name]; ok {
			body, present = slot.Body, true
		}
		if !present {
			if declared.Mode == compiler.RequiredInput {
				return fmt.Errorf("component %s requires content %s", call.Name, declared.Name)
			}
			continue
		}
		ops, err := u.body(body, roots)
		if err != nil {
			return err
		}
		operation.contents = append(operation.contents, content{name: declared.Name, body: ops})
	}
	if len(children) > 0 {
		if declared, ok := declaredContent[compiler.ChildrenInput]; !ok || !declared.IsContent() {
			return fmt.Errorf("component %s does not render children", call.Name)
		}
	}
	return nil
}

func (u *unit) dispatch(group *compiler.ClauseGroup, roots scope) (*dispatchOp, error) {
	selector, ok := roots[group.Selector.Name]
	if !ok {
		return nil, fmt.Errorf("selector %s is not an input", group.Selector.Name)
	}
	operation := &dispatchOp{kind: group.Kind, selector: selector.slot, input: group.Selector.Name, cases: make(map[string]clauseCase), residual: make(map[string]bool)}
	for _, name := range group.Residual {
		operation.residual[name] = true
	}
	switch group.Kind {
	case compiler.StateSelector:
		if _, ok := u.snapshot.State(group.Selector.Type.State); !ok {
			return nil, fmt.Errorf("unknown state %s", group.Selector.Type.Name)
		}
	case compiler.BooleanSelector, compiler.OptionalSelector:
	default:
		return nil, fmt.Errorf("unsupported selector kind %q", group.Kind)
	}
	for _, clause := range group.Clauses {
		locals := roots
		var bindings []binding
		for _, declared := range clause.Bindings {
			if group.Kind == compiler.BooleanSelector {
				break
			}
			mode := ast.RequiredInput
			if declared.Optional {
				mode = ast.OptionalInput
			}
			slot := u.slot()
			locals = locals.with(declared.Name, local{slot: slot, typ: declared.Type, mode: mode})
			bindings = append(bindings, binding{slot: slot, field: compiler.GoFieldName(declared.Field)})
		}
		body, err := u.componentBody(clause.Body, locals)
		if err != nil {
			return nil, err
		}
		operation.cases[clause.Case] = clauseCase{bindings: bindings, body: body}
	}
	needsDefault := len(group.Residual) > 0 || group.Kind != compiler.StateSelector
	if needsDefault {
		var err error
		if operation.fallback, err = u.componentBody(group.DefaultBody, roots); err != nil {
			return nil, err
		}
	}
	return operation, nil
}

func exactPath(quoted *ast.QuotedValue) (*ast.PathExpression, bool) {
	if quoted == nil || len(quoted.Segments) != 1 {
		return nil, false
	}
	path := quoted.Segments[0].Path
	if quoted.Segments[0].Kind != ast.PathSegment || path == nil || len(path.Parts) == 0 {
		return nil, false
	}
	return path, true
}

func hasPath(quoted *ast.QuotedValue) bool {
	if quoted == nil {
		return false
	}
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.PathSegment {
			return true
		}
	}
	return false
}

func quotedLiteral(quoted *ast.QuotedValue, raw string) string {
	if quoted == nil {
		return unquote(raw)
	}
	var value strings.Builder
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.PathSegment {
			return ""
		}
		value.WriteString(segment.Literal)
	}
	return value.String()
}

func unquote(value string) string {
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	return value
}

func nextReference(value string, roots scope) (start, end int, name string, ok bool) {
	start = len(value)
	for root := range roots {
		pattern := "{" + root + "}"
		if index := strings.Index(value, pattern); index >= 0 && index < start {
			start = index
			end = index + len(pattern) - 1
			name = root
			ok = true
		}
	}
	return start, end, name, ok
}

func isContextual(typ compiler.Type, name string) bool {
	return typ.Kind == compiler.ContextualTypeKind && typ.Name == name
}
