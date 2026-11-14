package interp

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sort"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/dev"
	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/rt"
)

// story is one compiled story: the typed values of its Document and its
// content fills, compiled like the body of a call. model is the frame that
// owns the fills; it names them in render errors and Render Identity frames.
type story struct {
	semantic compiler.Story
	model    *component
	contents []content
}

// StoryProps is a story rendered with control overrides. It is the props
// value the render registry tracks for a story preview, and it renders
// through the program that created it.
type StoryProps struct {
	Story     compiler.SymbolID
	Overrides map[string]compiler.Value
	program   *Program
}

// Render renders the story through the program that created the props.
func (props StoryProps) Render(ctx context.Context, w io.Writer) error {
	if props.program == nil {
		return fmt.Errorf("interp: story %s has no program", props.Story)
	}
	return props.program.RenderStory(ctx, w, props.Story, props.Overrides)
}

// KTMLComponentID is the registry key of the story: its symbol ID.
func (props StoryProps) KTMLComponentID() string { return string(props.Story) }

// StoryProps returns the props of a story with overrides, for tracking.
func (p *Program) StoryProps(id compiler.SymbolID, overrides map[string]compiler.Value) StoryProps {
	return StoryProps{Story: id, Overrides: overrides, program: p}
}

// compileStories compiles every story of the snapshot. Stories are small, so
// a swap recompiles all of them against the new snapshot.
func (p *Program) compileStories(importPaths map[string]string) error {
	p.stories = make(map[compiler.SymbolID]*story)
	p.storyFiles = make(map[string][]compiler.SymbolID)
	for _, semantic := range p.snapshot.Stories() {
		target, ok := p.components[semantic.Component]
		if !ok {
			return fmt.Errorf("interp: story %s.%s: component is not in the program", semantic.ComponentName, semantic.Name)
		}
		u := &unit{snapshot: p.snapshot, options: p.options, file: semantic.File, importPaths: importPaths, visible: make(map[string]compiler.Component)}
		for _, visible := range p.snapshot.VisibleComponents(semantic.File) {
			u.visible[visible.Name] = visible
		}
		model := &component{id: semantic.ID, name: semantic.ComponentName + "." + semantic.Name, goID: target.goID + ".story." + semantic.Name, file: semantic.File}
		u.model = model
		declared, _ := p.snapshot.Component(semantic.Component)
		operation := &callOp{target: semantic.Component, name: semantic.ComponentName}
		call := &ast.ElementNode{Name: semantic.ComponentName, Children: semantic.Body}
		if err := u.callContent(operation, call, declared, scope{}); err != nil {
			return fmt.Errorf("interp: story %s.%s: %w", semantic.ComponentName, semantic.Name, err)
		}
		p.stories[semantic.ID] = &story{semantic: semantic, model: model, contents: operation.contents}
		p.storyFiles[semantic.File] = append(p.storyFiles[semantic.File], semantic.ID)
	}
	return nil
}

// Stories returns the stories of the program, ordered by file and position.
func (p *Program) Stories() []compiler.Story {
	return p.snapshot.Stories()
}

// Snapshot returns the validated workspace snapshot the program compiled.
func (p *Program) Snapshot() *compiler.Snapshot { return p.snapshot }

// RenderStory renders a story's component with the story's values, each
// replaced by its override when one is given. Content comes from the story
// body. A defaulted or optional input whose override is absent (a None
// value) renders as absent.
func (p *Program) RenderStory(ctx context.Context, w io.Writer, id compiler.SymbolID, overrides map[string]compiler.Value) error {
	compiled, ok := p.stories[id]
	if !ok {
		return fmt.Errorf("interp: no story %s in the workspace", id)
	}
	target, ok := p.components[compiled.semantic.Component]
	if !ok {
		return fmt.Errorf("interp: story %s: component is not in the program", id)
	}
	writer, owned := rt.Wrap(w)
	values := make([]reflect.Value, target.slots)
	s := &state{program: p, ctx: ctx, w: writer, model: compiled.model}
	if p.options.Development {
		s.instance = dev.Scope(ctx)
	}
	for index, info := range target.inputs {
		if info.isContent() {
			for _, fill := range compiled.contents {
				if fill.name == info.name {
					values[index] = reflect.ValueOf(fill.closure(s))
				}
			}
			continue
		}
		value, given := overrides[info.name]
		if !given {
			value, given = compiled.semantic.Value(info.name)
		}
		if !given || value.Kind == compiler.NoneValueKind {
			if info.mode == ast.RequiredInput {
				return rt.Finish(writer, owned, fmt.Errorf("interp: story %s has no value for required input %s", id, info.name))
			}
			continue
		}
		converted, err := p.storyValue(value, info.typ, info.mode == ast.OptionalInput)
		if err != nil {
			return rt.Finish(writer, owned, fmt.Errorf("interp: story %s input %s: %w", id, info.name, err))
		}
		values[index] = converted
	}
	return rt.Finish(writer, owned, p.render(ctx, writer, target, values))
}

// storyState is a Presentation State value built from a story. The
// interpreter reads it as it reads a generated sealed state: by its Variant
// method and its payload.
type storyState struct {
	variant storyVariant
	payload reflect.Value
}

type storyVariant string

func (v storyVariant) String() string { return string(v) }

// Variant reports the Variant name.
func (s storyState) Variant() storyVariant { return s.variant }

var storyStateType = reflect.TypeOf((*storyState)(nil))

// storyType is the Go type the interpreter reads a story value of typ as,
// with the shapes of generated values: records are structs of their Go field
// names, optional values pointers, and collections slices.
func (p *Program) storyType(typ compiler.Type, optional bool) (reflect.Type, error) {
	var result reflect.Type
	switch typ.Kind {
	case compiler.ScalarTypeKind:
		switch typ.Scalar {
		case compiler.StringType:
			result = reflect.TypeOf("")
		case compiler.BooleanType:
			result = reflect.TypeOf(false)
		case compiler.NumberType:
			result = reflect.TypeOf(float64(0))
		case compiler.IntegerType:
			result = reflect.TypeOf(int64(0))
		}
	case compiler.ContextualTypeKind:
		switch typ.Name {
		case compiler.URLType:
			result = reflect.TypeOf(ktml.URL{})
		case compiler.TrustedURLType:
			result = reflect.TypeOf(ktml.TrustedURL{})
		case compiler.JSONValueType:
			result = reflect.TypeOf(ktml.JSONValue{})
		case compiler.TrustedScriptType:
			result = reflect.TypeOf(ktml.TrustedScript{})
		case compiler.CSSValueType:
			result = reflect.TypeOf(ktml.CSSValue{})
		case compiler.AttributesType:
			result = reflect.TypeOf(ktml.Attributes{})
		}
	case compiler.RecordTypeKind:
		record, ok := p.snapshot.Record(typ.Record)
		if !ok {
			return nil, fmt.Errorf("unknown record %s", typ.Name)
		}
		fields := make([]reflect.StructField, 0, len(record.Fields))
		for _, field := range record.Fields {
			fieldType, err := p.storyType(field.Type, field.Optional)
			if err != nil {
				return nil, err
			}
			fields = append(fields, reflect.StructField{Name: field.GoName, Type: fieldType})
		}
		result = reflect.StructOf(fields)
	case compiler.CollectionTypeKind:
		element, err := p.storyType(*typ.Element, false)
		if err != nil {
			return nil, err
		}
		result = reflect.SliceOf(element)
	case compiler.StateTypeKind:
		return storyStateType, nil
	}
	if result == nil {
		return nil, fmt.Errorf("a story cannot give a %s value", typ)
	}
	if optional {
		result = reflect.PointerTo(result)
	}
	return result, nil
}

// storyValue converts a typed story value into the value the interpreter
// reads for typ.
func (p *Program) storyValue(value compiler.Value, typ compiler.Type, optional bool) (reflect.Value, error) {
	goType, err := p.storyType(typ, optional)
	if err != nil {
		return reflect.Value{}, err
	}
	if value.Kind == compiler.NoneValueKind {
		return reflect.Zero(goType), nil
	}
	var result reflect.Value
	switch typ.Kind {
	case compiler.ScalarTypeKind:
		switch typ.Scalar {
		case compiler.StringType:
			result = reflect.ValueOf(value.String)
		case compiler.BooleanType:
			result = reflect.ValueOf(value.Boolean)
		case compiler.NumberType:
			result = reflect.ValueOf(value.Number)
		case compiler.IntegerType:
			result = reflect.ValueOf(value.Integer)
		}
	case compiler.ContextualTypeKind:
		switch typ.Name {
		case compiler.URLType:
			result = reflect.ValueOf(ktml.NewURL(value.String))
		case compiler.TrustedURLType:
			result = reflect.ValueOf(ktml.TrustURL(value.String))
		case compiler.JSONValueType:
			result = reflect.ValueOf(ktml.TrustJSON(value.String))
		case compiler.TrustedScriptType:
			result = reflect.ValueOf(ktml.TrustScript(value.String))
		case compiler.CSSValueType:
			result = reflect.ValueOf(ktml.TrustCSS(value.String))
		case compiler.AttributesType:
			// A story gives only the empty bag.
			result = reflect.ValueOf(ktml.Attributes{})
		}
	case compiler.RecordTypeKind:
		record, _ := p.snapshot.Record(typ.Record)
		structType := goType
		if optional {
			structType = goType.Elem()
		}
		result = reflect.New(structType).Elem()
		if err := p.storyFields(result, value.Fields, record.Fields); err != nil {
			return reflect.Value{}, err
		}
	case compiler.CollectionTypeKind:
		sliceType := goType
		if optional {
			sliceType = goType.Elem()
		}
		result = reflect.MakeSlice(sliceType, 0, len(value.Items))
		for _, item := range value.Items {
			converted, err := p.storyValue(item, *typ.Element, false)
			if err != nil {
				return reflect.Value{}, err
			}
			result = reflect.Append(result, converted)
		}
	case compiler.StateTypeKind:
		state, ok := p.snapshot.State(typ.State)
		if !ok {
			return reflect.Value{}, fmt.Errorf("unknown state %s", typ.Name)
		}
		variant, ok := state.Variant(value.Variant)
		if !ok {
			return reflect.Value{}, fmt.Errorf("state %s has no Variant %s", state.Name, value.Variant)
		}
		built := &storyState{variant: storyVariant(variant.Name)}
		if variant.Payload {
			fields := make([]reflect.StructField, 0, len(variant.Fields))
			for _, field := range variant.Fields {
				fieldType, err := p.storyType(field.Type, field.Optional)
				if err != nil {
					return reflect.Value{}, err
				}
				fields = append(fields, reflect.StructField{Name: field.GoName, Type: fieldType})
			}
			built.payload = reflect.New(reflect.StructOf(fields)).Elem()
			if err := p.storyFields(built.payload, value.Fields, variant.Fields); err != nil {
				return reflect.Value{}, err
			}
		}
		// A state is already a pointer, so an optional state is the same
		// value: nil is absent.
		return reflect.ValueOf(built), nil
	}
	if !result.IsValid() {
		return reflect.Value{}, fmt.Errorf("a story cannot give a %s value", typ)
	}
	if optional {
		pointer := reflect.New(result.Type())
		pointer.Elem().Set(result)
		return pointer, nil
	}
	return result, nil
}

// storyFields sets the given fields of a record or payload struct. Fields that
// are not given keep their zero value; optional ones are absent.
func (p *Program) storyFields(target reflect.Value, given []compiler.FieldValue, declared []compiler.Field) error {
	for _, field := range declared {
		var value compiler.Value
		found := false
		for _, candidate := range given {
			if candidate.Name == field.Name {
				value, found = candidate.Value, true
			}
		}
		if !found {
			continue
		}
		converted, err := p.storyValue(value, field.Type, field.Optional)
		if err != nil {
			return fmt.Errorf("field %s: %w", field.Name, err)
		}
		target.FieldByName(field.GoName).Set(converted)
	}
	return nil
}

// storyDependencies returns the components a story renders: its component's
// closure and every component its content calls.
func (p *Program) storyDependencies(id compiler.SymbolID) []string {
	compiled, ok := p.stories[id]
	if !ok {
		return []string{string(id)}
	}
	seen := map[string]bool{string(id): true}
	var roots []string
	if target, ok := p.components[compiled.semantic.Component]; ok {
		roots = append(roots, target.goID)
	}
	for _, fill := range compiled.contents {
		visitCalls(fill.body, func(call *callOp) {
			if callee, ok := p.components[call.target]; ok {
				roots = append(roots, callee.goID)
			}
		})
	}
	for _, root := range roots {
		for _, dependency := range p.Dependencies(root) {
			seen[dependency] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for dependency := range seen {
		ids = append(ids, dependency)
	}
	sort.Strings(ids)
	return ids
}

// Symbol returns the symbol of the component or story a render error names
// by its Go ID.
func (p *Program) Symbol(goID string) (compiler.SymbolID, bool) {
	if model, ok := p.byGoID[goID]; ok {
		return model.id, true
	}
	for id, compiled := range p.stories {
		if compiled.model.goID == goID {
			return id, true
		}
	}
	return "", false
}
