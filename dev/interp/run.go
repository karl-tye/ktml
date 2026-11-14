package interp

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"unsafe"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/dev"
	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/rt"
)

// state is one running component body or fill closure. model is the component
// whose source the body belongs to, which names it in render errors; identity
// is the innermost repetition item's Render Identity in that source, or "";
// instance is the Render Identity of the component instance in development.
type state struct {
	program  *Program
	ctx      context.Context
	w        *rt.Writer
	model    *component
	values   []reflect.Value
	identity string
	instance string
}

// op is one compiled render operation.
type op interface {
	run(s *state) error
}

func runOps(ops []op, s *state) error {
	for _, operation := range ops {
		if err := operation.run(s); err != nil {
			return err
		}
	}
	return nil
}

// render runs a component with its input values, as the generated internal
// renderer does: cancellation, then required content, then defaults, then the
// body or Render Clause dispatch.
func (p *Program) render(ctx context.Context, w *rt.Writer, model *component, values []reflect.Value) error {
	if err := ctx.Err(); err != nil {
		return rt.Canceled(model.goID, "", err)
	}
	for index, info := range model.inputs {
		if info.isContent() && info.mode == ast.RequiredInput && !present(values[index]) {
			return rt.MissingContent(model.goID, info.name)
		}
	}
	for index, info := range model.inputs {
		if info.mode != ast.DefaultedInput || info.isContent() {
			continue
		}
		if value := deref(values[index]); value.IsValid() {
			values[index] = value
		} else {
			values[index] = info.fallback
		}
	}
	s := &state{program: p, ctx: ctx, w: w, model: model, values: values}
	if p.options.Development {
		s.instance = dev.Scope(ctx)
	}
	if model.dispatch != nil {
		return model.dispatch.run(s)
	}
	return runOps(model.body, s)
}

// context is the context passed to the component call or content outlet whose
// c<n> or o<n> source is site. In development it carries the Render Identity
// of the instance rendered there: the innermost repetition item, or the scope
// of the context, extended by the site's frame.
func (s *state) context(site string) context.Context {
	if !s.program.options.Development {
		return s.ctx
	}
	scope := s.identity
	if scope == "" {
		scope = dev.Scope(s.ctx)
	}
	return dev.WithScope(s.ctx, dev.CallFrame(scope, s.model.goID, site))
}

type instanceKind int

const (
	instanceAttr instanceKind = iota
	beginInstance
	endInstance
)

// instanceOp writes a component instance marker of the development variant.
type instanceOp struct {
	kind instanceKind
}

func (operation instanceOp) run(s *state) error {
	var err error
	switch operation.kind {
	case instanceAttr:
		err = dev.InstanceAttr(s.w, s.instance)
	case beginInstance:
		err = dev.BeginInstance(s.w, s.instance)
	case endInstance:
		err = dev.EndInstance(s.w, s.instance)
	}
	if err != nil {
		return rt.WriteError(s.model.goID, err)
	}
	return nil
}

// deref follows pointers; a nil pointer is the zero Value.
func deref(value reflect.Value) reflect.Value {
	for value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}

// present reports whether an optional value, content value, or state is
// present.
func present(value reflect.Value) bool {
	if !value.IsValid() {
		return false
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func:
		return !value.IsNil()
	}
	return true
}

// pathRef is a compiled typed path: a frame slot and the Go field names of
// the record fields after it.
type pathRef struct {
	slot   int
	fields []string
}

func (ref pathRef) value(s *state) reflect.Value {
	value := deref(s.values[ref.slot])
	for _, field := range ref.fields {
		if !value.IsValid() {
			return value
		}
		value = deref(value.FieldByName(field))
	}
	return value
}

func (ref pathRef) present(s *state) bool {
	value := s.values[ref.slot]
	for _, field := range ref.fields {
		value = deref(value)
		if !value.IsValid() {
			return false
		}
		value = value.FieldByName(field)
	}
	return present(value)
}

func (ref pathRef) boolean(s *state) bool {
	return ref.value(s).Bool()
}

// staticOp writes coalesced, pre-escaped static markup.
type staticOp string

func (markup staticOp) run(s *state) error {
	if err := rt.Raw(s.w, string(markup)); err != nil {
		return rt.WriteError(s.model.goID, err)
	}
	return nil
}

type writeFunc func(*rt.Writer, reflect.Value) error

// valueOp writes a path value through one contextual rt primitive.
type valueOp struct {
	path  pathRef
	write writeFunc
}

func (operation valueOp) run(s *state) error {
	if err := operation.write(s.w, operation.path.value(s)); err != nil {
		return rt.WriteError(s.model.goID, err)
	}
	return nil
}

func scalarWriter(typ compiler.Type, attribute bool) (writeFunc, bool) {
	switch {
	case typ.IsScalar(compiler.StringType) && attribute:
		return func(w *rt.Writer, v reflect.Value) error { return rt.Attr(w, v.String()) }, true
	case typ.IsScalar(compiler.StringType):
		return func(w *rt.Writer, v reflect.Value) error { return rt.Text(w, v.String()) }, true
	case typ.IsScalar(compiler.IntegerType) && attribute:
		return func(w *rt.Writer, v reflect.Value) error { return rt.AttrInt(w, v.Int()) }, true
	case typ.IsScalar(compiler.IntegerType):
		return func(w *rt.Writer, v reflect.Value) error { return rt.TextInt(w, v.Int()) }, true
	case typ.IsScalar(compiler.NumberType) && attribute:
		return func(w *rt.Writer, v reflect.Value) error { return rt.AttrNumber(w, v.Float()) }, true
	case typ.IsScalar(compiler.NumberType):
		return func(w *rt.Writer, v reflect.Value) error { return rt.TextNumber(w, v.Float()) }, true
	}
	return nil, false
}

// attributeWriter writes an exact-path attribute value of a non-boolean type.
func attributeWriter(typ compiler.Type) (writeFunc, bool) {
	switch {
	case isContextual(typ, compiler.URLType):
		return func(w *rt.Writer, v reflect.Value) error { return rt.URL(w, v.Interface().(ktml.URL)) }, true
	case isContextual(typ, compiler.TrustedURLType):
		return func(w *rt.Writer, v reflect.Value) error { return rt.TrustedURL(w, v.Interface().(ktml.TrustedURL)) }, true
	case isContextual(typ, compiler.JSONValueType):
		return writeJSONAttr, true
	}
	return scalarWriter(typ, true)
}

func writeJSONAttr(w *rt.Writer, v reflect.Value) error {
	return rt.JSONAttr(w, v.Interface().(ktml.JSONValue))
}

func writeScriptAttr(w *rt.Writer, v reflect.Value) error {
	return rt.ScriptAttr(w, v.Interface().(ktml.TrustedScript))
}

func writeScriptBool(w *rt.Writer, v reflect.Value) error {
	return rt.ScriptBool(w, v.Bool())
}

// ifOp is a condition, or a boolean attribute when then is its name.
type ifOp struct {
	test      func(*state) bool
	then      []op
	otherwise []op
}

func (operation ifOp) run(s *state) error {
	if operation.test(s) {
		return runOps(operation.then, s)
	}
	return runOps(operation.otherwise, s)
}

// outletOp renders a content input; absent optional content writes nothing.
type outletOp struct {
	slot int
	site string
}

func (operation outletOp) run(s *state) error {
	value := s.values[operation.slot]
	if !present(value) {
		return nil
	}
	return value.Interface().(ktml.Component).Render(s.context(operation.site), s.w)
}

// repetitionOp is a Keyed Repetition: a preflight that adds every key to one
// set, then a second pass that streams each body.
type repetitionOp struct {
	collection pathRef
	item       int
	key        pathRef
	integer    bool
	source     string
	identity   string
	body       []op
}

func (operation *repetitionOp) run(s *state) error {
	items := operation.collection.value(s)
	count := 0
	if items.IsValid() {
		count = items.Len()
	}
	if operation.integer {
		var keys rt.IntegerKeys
		keys.Reset(count)
		for index := range count {
			s.values[operation.item] = items.Index(index)
			if key := operation.key.value(s).Int(); !keys.Add(key) {
				return rt.DuplicateIntegerKey(s.model.goID, operation.source, key)
			}
		}
	} else {
		var keys rt.StringKeys
		keys.Reset(count)
		for index := range count {
			s.values[operation.item] = items.Index(index)
			if key := operation.key.value(s).String(); !keys.Add(key) {
				return rt.DuplicateStringKey(s.model.goID, operation.source, key)
			}
		}
	}
	development := s.program.options.Development
	scope := s.identity
	if development && scope == "" {
		scope = dev.Scope(s.ctx)
	}
	outer := s.identity
	defer func() { s.identity = outer }()
	for index := range count {
		if err := s.ctx.Err(); err != nil {
			return rt.Canceled(s.model.goID, operation.source, err)
		}
		s.values[operation.item] = items.Index(index)
		if development {
			key := operation.key.value(s)
			var identity string
			if operation.integer {
				identity = dev.IntegerFrame(scope, s.model.goID, operation.identity, key.Int())
			} else {
				identity = dev.StringFrame(scope, s.model.goID, operation.identity, key.String())
			}
			if err := dev.Begin(s.w, identity); err != nil {
				return rt.WriteError(s.model.goID, err)
			}
			s.identity = identity
		}
		if err := runOps(operation.body, s); err != nil {
			return err
		}
		if development {
			if err := dev.End(s.w, s.identity); err != nil {
				return rt.WriteError(s.model.goID, err)
			}
			s.identity = outer
		}
	}
	return nil
}

// argument is one supplied component call input: a path, a string built from
// literal text and string, integer, and number paths, or a literal.
type argument struct {
	name     string
	bag      bagFunc
	path     *pathRef
	isConcat bool
	concat   []part
	literal  reflect.Value
}

type part struct {
	literal string
	path    *pathRef
	format  func(reflect.Value) string
}

func (value argument) evaluate(s *state) reflect.Value {
	switch {
	case value.bag != nil:
		return reflect.ValueOf(value.bag(s))
	case value.path != nil:
		return value.path.value(s)
	case value.isConcat:
		var joined string
		for _, segment := range value.concat {
			if segment.path != nil {
				joined += segment.format(segment.path.value(s))
			} else {
				joined += segment.literal
			}
		}
		return reflect.ValueOf(joined)
	}
	return value.literal
}

// content is one fill of a content input, written in the caller.
type content struct {
	name string
	body []op
}

// closure returns the fill as a ktml.Component over the caller's scope, as
// generated fill closures are.
func (fill content) closure(s *state) ktml.Component {
	captured := *s
	captured.values = append([]reflect.Value(nil), s.values...)
	return ktml.ComponentFunc(func(ctx context.Context, target io.Writer) error {
		w, _ := rt.Wrap(target)
		inner := captured
		inner.ctx, inner.w = ctx, w
		return runOps(fill.body, &inner)
	})
}

// callOp calls a component. The target is looked up when the call runs, so a
// swapped callee is used without recompiling its callers.
type callOp struct {
	target    compiler.SymbolID
	name      string
	site      string
	arguments []argument
	contents  []content
}

func (operation *callOp) run(s *state) error {
	callee, ok := s.program.components[operation.target]
	if !ok {
		return fmt.Errorf("interp: component %s is not in the program", operation.name)
	}
	values := make([]reflect.Value, callee.slots)
	for index, info := range callee.inputs {
		if info.isContent() {
			for _, fill := range operation.contents {
				if fill.name == info.name {
					values[index] = reflect.ValueOf(fill.closure(s))
				}
			}
			continue
		}
		for _, supplied := range operation.arguments {
			if supplied.name == info.name {
				values[index] = supplied.evaluate(s)
			}
		}
	}
	return s.program.render(s.context(operation.site), s.w, callee, values)
}

// dispatchOp selects one Render Clause, or the default body, before the
// component writes. Nil or unknown states are ErrInvalidState.
type dispatchOp struct {
	kind     compiler.SelectorKind
	selector int
	input    string
	cases    map[string]clauseCase
	residual map[string]bool
	fallback []op
}

type clauseCase struct {
	bindings []binding
	body     []op
}

// binding is a clause payload binding: the frame slot and the payload's Go
// field.
type binding struct {
	slot  int
	field string
}

func (operation *dispatchOp) run(s *state) error {
	selector := s.values[operation.selector]
	switch operation.kind {
	case compiler.StateSelector:
		if !present(selector) {
			return rt.InvalidState(s.model.goID, operation.input)
		}
		name := variantName(selector)
		clause, specialized := operation.cases[name]
		if !specialized {
			if operation.residual[name] {
				return runOps(operation.fallback, s)
			}
			return rt.InvalidState(s.model.goID, operation.input)
		}
		if len(clause.bindings) > 0 {
			payload := payloadOf(selector)
			if !payload.IsValid() {
				return rt.InvalidState(s.model.goID, operation.input)
			}
			for _, bound := range clause.bindings {
				s.values[bound.slot] = payload.FieldByName(bound.field)
			}
		}
		return runOps(clause.body, s)
	case compiler.BooleanSelector:
		name := compiler.FalseCase
		if deref(selector).Bool() {
			name = compiler.TrueCase
		}
		return operation.caseBody(s, name, reflect.Value{})
	case compiler.OptionalSelector:
		if present(selector) {
			return operation.caseBody(s, compiler.SomeCase, deref(selector))
		}
		return operation.caseBody(s, compiler.NoneCase, reflect.Value{})
	}
	return fmt.Errorf("interp: unsupported selector kind %q", operation.kind)
}

func (operation *dispatchOp) caseBody(s *state, name string, value reflect.Value) error {
	clause, specialized := operation.cases[name]
	if !specialized {
		return runOps(operation.fallback, s)
	}
	for _, bound := range clause.bindings {
		s.values[bound.slot] = value
	}
	return runOps(clause.body, s)
}

// variantName returns the Variant name a generated state reports, through its
// Variant method and the Variant type's String method.
func variantName(state reflect.Value) string {
	variant := state.MethodByName("Variant")
	if !variant.IsValid() {
		return ""
	}
	results := variant.Call(nil)
	if len(results) != 1 {
		return ""
	}
	stringer, ok := results[0].Interface().(fmt.Stringer)
	if !ok {
		return ""
	}
	return stringer.String()
}

// payloadOf reads the payload of a generated payload Variant. Variants are
// sealed: the payload is the unexported payload field of an unexported type,
// which generated As<State><Variant> functions read. Reflection cannot call
// those by name, so the development interpreter reads the field of a copy.
func payloadOf(state reflect.Value) reflect.Value {
	if state.IsValid() && state.CanInterface() {
		switch built := state.Interface().(type) {
		case *storyState:
			if built == nil {
				return reflect.Value{}
			}
			return built.payload
		case storyState:
			return built.payload
		}
	}
	concrete := state
	for concrete.Kind() == reflect.Interface || concrete.Kind() == reflect.Pointer {
		if concrete.IsNil() {
			return reflect.Value{}
		}
		concrete = concrete.Elem()
	}
	if concrete.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	copied := reflect.New(concrete.Type()).Elem()
	copied.Set(concrete)
	field := copied.FieldByName("payload")
	if !field.IsValid() {
		return reflect.Value{}
	}
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
}
