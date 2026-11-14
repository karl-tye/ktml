package explorer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/karl-tye/ktml/internal/compiler"
)

// Control values travel between the browser and the Explorer as JSON, typed
// only by the component's input types on the server:
//
//	string, URL       "text"
//	boolean           true
//	number, integer   1.5, 3
//	trusted value     {"fixture": "name"}   (never free text)
//	attribute bag     []                    (only the empty bag)
//	record            {"field": value, …}
//	collection        [value, …]
//	state             {"variant": "Name", "fields": {"field": value, …}}
//	None, or default  null
//
// Every value is decoded strictly before it renders, so the browser cannot
// supply a trusted value that is not a fixture of the story's Document.

// typer resolves the types and fixtures a story's values may use.
type typer struct {
	snapshot *compiler.Snapshot
	fixtures []compiler.Fixture
}

func newTyper(snapshot *compiler.Snapshot, story compiler.Story) typer {
	return typer{snapshot: snapshot, fixtures: snapshot.Fixtures(story.File)}
}

// fixturesOf returns the fixtures whose type is typ.
func (t typer) fixturesOf(typ compiler.Type) []compiler.Fixture {
	var matching []compiler.Fixture
	for _, fixture := range t.fixtures {
		if fixture.Type.Equal(typ) {
			matching = append(matching, fixture)
		}
	}
	return matching
}

// encode returns the JSON form of a typed story value.
func encode(value compiler.Value) any {
	switch value.Kind {
	case compiler.NoneValueKind, "":
		return nil
	case compiler.FixtureValueKind:
		return map[string]any{"fixture": value.Fixture}
	case compiler.AttributesValueKind:
		return []any{}
	case compiler.ScalarValueKind:
		switch {
		case value.Type.Kind == compiler.ContextualTypeKind:
			return value.String
		case value.Type.IsScalar(compiler.BooleanType):
			return value.Boolean
		case value.Type.IsScalar(compiler.NumberType):
			return value.Number
		case value.Type.IsScalar(compiler.IntegerType):
			return json.Number(strconv.FormatInt(value.Integer, 10))
		}
		return value.String
	case compiler.RecordValueKind:
		fields := make(map[string]any, len(value.Fields))
		for _, field := range value.Fields {
			fields[field.Name] = encode(field.Value)
		}
		return fields
	case compiler.CollectionValueKind:
		items := make([]any, len(value.Items))
		for index, item := range value.Items {
			items[index] = encode(item)
		}
		return items
	case compiler.VariantValueKind:
		fields := make(map[string]any, len(value.Fields))
		for _, field := range value.Fields {
			fields[field.Name] = encode(field.Value)
		}
		return map[string]any{"variant": value.Variant, "fields": fields}
	}
	return nil
}

// storyValues returns the JSON value of every value input of a story's
// component: the story's value, or null for an input it leaves to its
// default or to None.
func storyValues(component compiler.Component, story compiler.Story) map[string]any {
	values := make(map[string]any)
	for _, input := range component.Inputs {
		if input.IsContent() || !storyInput(input) {
			continue
		}
		if value, ok := story.Value(input.Name); ok {
			values[input.Name] = encode(value)
		} else {
			values[input.Name] = nil
		}
	}
	return values
}

// storyInput reports whether a story can give an input a value.
func storyInput(input compiler.Input) bool {
	return !input.IsContent() && !(input.Type.Kind == compiler.ContextualTypeKind && input.Type.Name == compiler.AttributesType)
}

// decodeJSON decodes control values with exact integers.
func decodeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

// decode types a JSON value as typ. optional allows null.
func (t typer) decode(raw any, typ compiler.Type, optional bool) (compiler.Value, error) {
	if raw == nil {
		if !optional {
			return compiler.Value{}, fmt.Errorf("a %s value is required", typ)
		}
		return compiler.Value{Kind: compiler.NoneValueKind, Type: typ}, nil
	}
	switch typ.Kind {
	case compiler.ScalarTypeKind:
		return decodeScalar(raw, typ)
	case compiler.ContextualTypeKind:
		if typ.Name == compiler.AttributesType {
			if items, ok := raw.([]any); !ok || len(items) > 0 {
				return compiler.Value{}, fmt.Errorf("a story gives only the empty attribute bag []")
			}
			return compiler.Value{Kind: compiler.AttributesValueKind, Type: typ}, nil
		}
		if typ.Name == compiler.URLType {
			if text, ok := raw.(string); ok {
				return compiler.Value{Kind: compiler.ScalarValueKind, Type: typ, String: text}, nil
			}
		}
		object, _ := raw.(map[string]any)
		name, _ := object["fixture"].(string)
		for _, fixture := range t.fixturesOf(typ) {
			if fixture.Name == name {
				return compiler.Value{Kind: compiler.FixtureValueKind, Type: typ, Fixture: name, String: fixture.Value}, nil
			}
		}
		return compiler.Value{}, fmt.Errorf("%s values come only from this story document's fixtures; %q is not one", typ, name)
	case compiler.RecordTypeKind:
		record, ok := t.snapshot.Record(typ.Record)
		object, isObject := raw.(map[string]any)
		if !ok || !isObject {
			return compiler.Value{}, fmt.Errorf("record %s requires an object", typ)
		}
		fields, err := t.decodeFields(object, record.Fields)
		return compiler.Value{Kind: compiler.RecordValueKind, Type: typ, Fields: fields}, err
	case compiler.CollectionTypeKind:
		items, ok := raw.([]any)
		if !ok {
			return compiler.Value{}, fmt.Errorf("%s requires a list", typ)
		}
		value := compiler.Value{Kind: compiler.CollectionValueKind, Type: typ}
		for index, item := range items {
			decoded, err := t.decode(item, *typ.Element, false)
			if err != nil {
				return compiler.Value{}, fmt.Errorf("item %d: %w", index, err)
			}
			value.Items = append(value.Items, decoded)
		}
		return value, nil
	case compiler.StateTypeKind:
		state, ok := t.snapshot.State(typ.State)
		object, isObject := raw.(map[string]any)
		if !ok || !isObject {
			return compiler.Value{}, fmt.Errorf("state %s requires a Variant", typ)
		}
		name, _ := object["variant"].(string)
		variant, ok := state.Variant(name)
		if !ok {
			return compiler.Value{}, fmt.Errorf("state %s has no Variant %q", state.Name, name)
		}
		value := compiler.Value{Kind: compiler.VariantValueKind, Type: typ, Variant: variant.Name}
		if variant.Payload {
			payload, _ := object["fields"].(map[string]any)
			fields, err := t.decodeFields(payload, variant.Fields)
			if err != nil {
				return compiler.Value{}, fmt.Errorf("%s.%s: %w", state.Name, variant.Name, err)
			}
			value.Fields = fields
		}
		return value, nil
	}
	return compiler.Value{}, fmt.Errorf("the Explorer cannot give a %s value", typ)
}

func (t typer) decodeFields(object map[string]any, declared []compiler.Field) ([]compiler.FieldValue, error) {
	var fields []compiler.FieldValue
	for _, field := range declared {
		raw, given := object[field.Name]
		if !given && field.Optional {
			continue
		}
		value, err := t.decode(raw, field.Type, field.Optional)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.Name, err)
		}
		fields = append(fields, compiler.FieldValue{Name: field.Name, Value: value})
	}
	return fields, nil
}

func decodeScalar(raw any, typ compiler.Type) (compiler.Value, error) {
	value := compiler.Value{Kind: compiler.ScalarValueKind, Type: typ}
	switch typ.Scalar {
	case compiler.StringType:
		text, ok := raw.(string)
		if !ok {
			return compiler.Value{}, fmt.Errorf("a string is required")
		}
		value.String = text
	case compiler.BooleanType:
		flag, ok := raw.(bool)
		if !ok {
			return compiler.Value{}, fmt.Errorf("a boolean is required")
		}
		value.Boolean = flag
	case compiler.NumberType:
		number, err := jsonNumber(raw)
		if err != nil {
			return compiler.Value{}, err
		}
		value.Number = number
	case compiler.IntegerType:
		switch typed := raw.(type) {
		case json.Number:
			integer, err := strconv.ParseInt(typed.String(), 10, 64)
			if err != nil {
				return compiler.Value{}, fmt.Errorf("a whole number is required")
			}
			value.Integer = integer
		case float64:
			if typed != math.Trunc(typed) || math.Abs(typed) > 1<<53 {
				return compiler.Value{}, fmt.Errorf("a whole number is required")
			}
			value.Integer = int64(typed)
		default:
			return compiler.Value{}, fmt.Errorf("a whole number is required")
		}
	}
	return value, nil
}

func jsonNumber(raw any) (float64, error) {
	switch typed := raw.(type) {
	case json.Number:
		number, err := typed.Float64()
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return 0, fmt.Errorf("a number is required")
		}
		return number, nil
	case float64:
		return typed, nil
	}
	return 0, fmt.Errorf("a number is required")
}

// overrides decodes every input value. An input whose value does not type
// against the current component keeps the story's value and reports why.
func (t typer) overrides(component compiler.Component, values map[string]any) (map[string]compiler.Value, map[string]error) {
	decoded := make(map[string]compiler.Value)
	failures := make(map[string]error)
	for _, input := range component.Inputs {
		raw, given := values[input.Name]
		if !given || !storyInput(input) {
			continue
		}
		value, err := t.decode(raw, input.Type, input.Mode != compiler.RequiredInput)
		if err != nil {
			failures[input.Name] = err
			continue
		}
		decoded[input.Name] = value
	}
	return decoded, failures
}

// zero is the JSON value a new control starts with: false, zero, empty, the
// first Variant, or the first fixture of the type.
func (t typer) zero(typ compiler.Type, optional bool) any {
	if optional {
		return nil
	}
	switch typ.Kind {
	case compiler.ScalarTypeKind:
		switch typ.Scalar {
		case compiler.BooleanType:
			return false
		case compiler.NumberType:
			return json.Number("0")
		case compiler.IntegerType:
			return json.Number("0")
		}
		return ""
	case compiler.ContextualTypeKind:
		switch typ.Name {
		case compiler.URLType:
			return ""
		case compiler.AttributesType:
			return []any{}
		}
		name := ""
		if fixtures := t.fixturesOf(typ); len(fixtures) > 0 {
			name = fixtures[0].Name
		}
		return map[string]any{"fixture": name}
	case compiler.RecordTypeKind:
		record, _ := t.snapshot.Record(typ.Record)
		return t.zeroFields(record.Fields)
	case compiler.CollectionTypeKind:
		return []any{}
	case compiler.StateTypeKind:
		state, _ := t.snapshot.State(typ.State)
		if len(state.Variants) == 0 {
			return nil
		}
		return t.zeroVariant(state.Variants[0])
	}
	return nil
}

func (t typer) zeroFields(fields []compiler.Field) map[string]any {
	object := make(map[string]any, len(fields))
	for _, field := range fields {
		object[field.Name] = t.zero(field.Type, field.Optional)
	}
	return object
}

func (t typer) zeroVariant(variant compiler.Variant) map[string]any {
	return map[string]any{"variant": variant.Name, "fields": t.zeroFields(variant.Fields)}
}

// Edit is one control change.
type Edit struct {
	// Op is set, none, some, add, remove, or variant.
	Op string `json:"op"`
	// Path addresses the value: an input name, then field names, item
	// indexes, and payload field names.
	Path []any `json:"path"`
	// Value is the new value of set, or the Variant name of variant.
	Value any `json:"value"`
}

// structural reports whether an edit changes which controls exist.
func (edit Edit) structural() bool {
	return edit.Op != "set"
}

// slotOf is the place a path addresses: its type, whether it is optional,
// and how to read and write it.
type slotOf struct {
	typ      compiler.Type
	optional bool
	get      func() any
	set      func(any)
}

// resolve walks a path through values with the component's types.
func (t typer) resolve(component compiler.Component, values map[string]any, path []any) (slotOf, error) {
	if len(path) == 0 {
		return slotOf{}, fmt.Errorf("empty path")
	}
	name, _ := path[0].(string)
	var input compiler.Input
	found := false
	for _, candidate := range component.Inputs {
		if candidate.Name == name && storyInput(candidate) {
			input, found = candidate, true
		}
	}
	if !found {
		return slotOf{}, fmt.Errorf("component %s has no input %q", component.Name, name)
	}
	slot := slotOf{
		typ: input.Type, optional: input.Mode != compiler.RequiredInput,
		get: func() any { return values[name] },
		set: func(value any) { values[name] = value },
	}
	for _, segment := range path[1:] {
		current := slot.get()
		switch slot.typ.Kind {
		case compiler.RecordTypeKind:
			record, _ := t.snapshot.Record(slot.typ.Record)
			object, ok := current.(map[string]any)
			field, exists := fieldNamed(record.Fields, segment)
			if !ok || !exists {
				return slotOf{}, fmt.Errorf("no field %v", segment)
			}
			slot = slotOf{typ: field.Type, optional: field.Optional, get: func() any { return object[field.Name] }, set: func(value any) { object[field.Name] = value }}
		case compiler.StateTypeKind:
			state, _ := t.snapshot.State(slot.typ.State)
			object, ok := current.(map[string]any)
			if !ok {
				return slotOf{}, fmt.Errorf("no Variant at %v", segment)
			}
			name, _ := object["variant"].(string)
			variant, _ := state.Variant(name)
			field, exists := fieldNamed(variant.Fields, segment)
			payload, isPayload := object["fields"].(map[string]any)
			if !exists || !isPayload {
				return slotOf{}, fmt.Errorf("no payload field %v", segment)
			}
			slot = slotOf{typ: field.Type, optional: field.Optional, get: func() any { return payload[field.Name] }, set: func(value any) { payload[field.Name] = value }}
		case compiler.CollectionTypeKind:
			items, ok := current.([]any)
			index, isIndex := pathIndex(segment)
			if !ok || !isIndex || index < 0 || index >= len(items) {
				return slotOf{}, fmt.Errorf("no item %v", segment)
			}
			slot = slotOf{typ: *slot.typ.Element, get: func() any { return items[index] }, set: func(value any) { items[index] = value }}
		default:
			return slotOf{}, fmt.Errorf("%s has no part %v", slot.typ, segment)
		}
	}
	return slot, nil
}

func fieldNamed(fields []compiler.Field, segment any) (compiler.Field, bool) {
	name, _ := segment.(string)
	for _, field := range fields {
		if field.Name == name {
			return field, true
		}
	}
	return compiler.Field{}, false
}

func pathIndex(segment any) (int, bool) {
	switch typed := segment.(type) {
	case json.Number:
		index, err := strconv.Atoi(typed.String())
		return index, err == nil
	case float64:
		return int(typed), typed == math.Trunc(typed)
	case int:
		return typed, true
	}
	return 0, false
}

// apply makes one edit to values in place. The result is typed by decode
// before it renders.
func (t typer) apply(component compiler.Component, values map[string]any, edit Edit) error {
	if edit.Op == "remove" {
		if len(edit.Path) < 2 {
			return fmt.Errorf("remove needs an item path")
		}
		parent, err := t.resolve(component, values, edit.Path[:len(edit.Path)-1])
		if err != nil {
			return err
		}
		items, ok := parent.get().([]any)
		index, isIndex := pathIndex(edit.Path[len(edit.Path)-1])
		if !ok || !isIndex || index < 0 || index >= len(items) {
			return fmt.Errorf("no item to remove")
		}
		parent.set(append(items[:index:index], items[index+1:]...))
		return nil
	}
	slot, err := t.resolve(component, values, edit.Path)
	if err != nil {
		return err
	}
	switch edit.Op {
	case "set":
		slot.set(edit.Value)
	case "none":
		if !slot.optional {
			return fmt.Errorf("a required value cannot be None")
		}
		slot.set(nil)
	case "some":
		slot.set(t.zero(slot.typ, false))
	case "add":
		items, ok := slot.get().([]any)
		if !ok || slot.typ.Kind != compiler.CollectionTypeKind {
			return fmt.Errorf("add needs a collection")
		}
		slot.set(append(items, t.zero(*slot.typ.Element, false)))
	case "variant":
		state, ok := t.snapshot.State(slot.typ.State)
		name, _ := edit.Value.(string)
		variant, exists := state.Variant(name)
		if !ok || !exists {
			return fmt.Errorf("no Variant %q", name)
		}
		slot.set(t.zeroVariant(variant))
	default:
		return fmt.Errorf("unknown edit %q", edit.Op)
	}
	return nil
}
