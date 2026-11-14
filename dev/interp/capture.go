package interp

import (
	"fmt"
	"reflect"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

// CaptureItemLimit bounds the items a captured collection keeps.
const CaptureItemLimit = 10

// Captured is a rendered root's props read back as typed story values.
type Captured struct {
	Component compiler.Component
	// Values are the inputs the props give, in input order. Absent optional
	// and defaulted inputs are left out.
	Values []compiler.StoryValue
	// Content names the content inputs the props fill; their Go-built
	// content cannot be read back.
	Content []string
	// Unsupported names a value a story cannot give, such as a nil
	// required state.
	Unsupported string
	// Truncated reports a collection cut to CaptureItemLimit items.
	Truncated bool
}

// Capture reads a generated props value back into the typed values a story
// gives, through the same reflect shapes Render reads. The values are copied
// out, so the props may change afterwards.
func (p *Program) Capture(props ktml.Component) (Captured, error) {
	model, values, err := p.root(props)
	if err != nil {
		return Captured{}, err
	}
	declared, ok := p.snapshot.Component(model.id)
	if !ok {
		return Captured{}, fmt.Errorf("interp: no component %s in the workspace", model.id)
	}
	captured := Captured{Component: declared}
	for index, info := range model.inputs {
		value := values[index]
		if info.isContent() {
			if present(value) {
				captured.Content = append(captured.Content, info.name)
			}
			continue
		}
		if info.mode != ast.RequiredInput && !present(value) {
			continue
		}
		read, ok := captured.read(p, value, info.typ, info.name)
		if !ok {
			if info.mode == ast.RequiredInput {
				read = captured.zero(info.typ, info.name)
			} else {
				continue
			}
		}
		captured.Values = append(captured.Values, compiler.StoryValue{Input: info.name, Value: read})
	}
	return captured, nil
}

// read converts one value. ok is false for an absent value.
func (c *Captured) read(p *Program, value reflect.Value, typ compiler.Type, path string) (compiler.Value, bool) {
	if typ.Kind == compiler.StateTypeKind {
		return c.readState(p, value, typ, path)
	}
	value = deref(value)
	if !value.IsValid() {
		return compiler.Value{Kind: compiler.NoneValueKind, Type: typ}, false
	}
	switch typ.Kind {
	case compiler.ScalarTypeKind:
		result := compiler.Value{Kind: compiler.ScalarValueKind, Type: typ}
		switch typ.Scalar {
		case compiler.StringType:
			result.String = value.String()
		case compiler.BooleanType:
			result.Boolean = value.Bool()
		case compiler.IntegerType:
			result.Integer = value.Int()
			result.Number = float64(result.Integer)
		case compiler.NumberType:
			if value.CanFloat() {
				result.Number = value.Float()
			} else if value.CanInt() {
				result.Number = float64(value.Int())
			}
		}
		return result, true
	case compiler.ContextualTypeKind:
		if typ.Name == compiler.AttributesType {
			// A story gives only the empty bag, so the bag's entries are
			// left out.
			return compiler.Value{Kind: compiler.AttributesValueKind, Type: typ}, true
		}
		text := ""
		if stringer, ok := value.Interface().(fmt.Stringer); ok {
			text = stringer.String()
		}
		if typ.Name == compiler.URLType {
			return compiler.Value{Kind: compiler.ScalarValueKind, Type: typ, String: text}, true
		}
		return compiler.Value{Kind: compiler.FixtureValueKind, Type: typ, String: text}, true
	case compiler.RecordTypeKind:
		record, ok := p.snapshot.Record(typ.Record)
		if !ok {
			return compiler.Value{}, false
		}
		return compiler.Value{Kind: compiler.RecordValueKind, Type: typ, Fields: c.readFields(p, value, record.Fields, path)}, true
	case compiler.CollectionTypeKind:
		result := compiler.Value{Kind: compiler.CollectionValueKind, Type: typ, Items: []compiler.Value{}}
		if typ.Element == nil || value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
			return result, true
		}
		for index := 0; index < value.Len(); index++ {
			if index == CaptureItemLimit {
				c.Truncated = true
				break
			}
			item, ok := c.read(p, value.Index(index), *typ.Element, path+"[]")
			if !ok {
				item = c.zero(*typ.Element, path+"[]")
			}
			result.Items = append(result.Items, item)
		}
		return result, true
	}
	return compiler.Value{}, false
}

func (c *Captured) readFields(p *Program, value reflect.Value, fields []compiler.Field, path string) []compiler.FieldValue {
	result := []compiler.FieldValue{}
	if value.Kind() != reflect.Struct {
		return result
	}
	for _, field := range fields {
		got := value.FieldByName(field.GoName)
		if !got.IsValid() {
			continue
		}
		read, ok := c.read(p, got, field.Type, path+"."+field.Name)
		if !ok {
			if field.Optional {
				continue
			}
			read = c.zero(field.Type, path+"."+field.Name)
		}
		result = append(result, compiler.FieldValue{Name: field.Name, Value: read})
	}
	return result
}

func (c *Captured) readState(p *Program, value reflect.Value, typ compiler.Type, path string) (compiler.Value, bool) {
	if !present(value) {
		return compiler.Value{Kind: compiler.NoneValueKind, Type: typ}, false
	}
	state, ok := p.snapshot.State(typ.State)
	if !ok {
		return compiler.Value{}, false
	}
	variant, ok := state.Variant(variantName(value))
	if !ok {
		return compiler.Value{}, false
	}
	result := compiler.Value{Kind: compiler.VariantValueKind, Type: typ, Variant: variant.Name}
	if variant.Payload {
		result.Fields = c.readFields(p, deref(payloadOf(value)), variant.Fields, path+"."+variant.Name)
	}
	return result, true
}

// zero is the value a story gives a required value the props leave unset,
// such as a nil slice. A required state has none: Render would fail too.
func (c *Captured) zero(typ compiler.Type, path string) compiler.Value {
	value := zeroValue(typ)
	if value.Kind == compiler.NoneValueKind && c.Unsupported == "" {
		c.Unsupported = path
	}
	return value
}

func zeroValue(typ compiler.Type) compiler.Value {
	switch typ.Kind {
	case compiler.CollectionTypeKind:
		return compiler.Value{Kind: compiler.CollectionValueKind, Type: typ, Items: []compiler.Value{}}
	case compiler.ScalarTypeKind:
		return compiler.Value{Kind: compiler.ScalarValueKind, Type: typ}
	case compiler.ContextualTypeKind:
		switch typ.Name {
		case compiler.URLType:
			return compiler.Value{Kind: compiler.ScalarValueKind, Type: typ}
		case compiler.AttributesType:
			return compiler.Value{Kind: compiler.AttributesValueKind, Type: typ}
		}
		return compiler.Value{Kind: compiler.FixtureValueKind, Type: typ}
	case compiler.RecordTypeKind:
		return compiler.Value{Kind: compiler.RecordValueKind, Type: typ, Fields: []compiler.FieldValue{}}
	}
	return compiler.Value{Kind: compiler.NoneValueKind, Type: typ}
}
