package explorer

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
)

// controls renders the typed controls of every input of a story's component
// for the current values. Every control names its value by a JSON path and
// says which edit it makes; the browser sends the edit and the server types
// it, so the controls carry no type logic of their own.
func (t typer) controls(component compiler.Component, story compiler.Story, values map[string]any, failures map[string]error) string {
	var b strings.Builder
	b.WriteString(`<form class="controls" data-controls onsubmit="return false">`)
	for _, input := range component.Inputs {
		b.WriteString(`<div class="control" data-input="` + attr(input.Name) + `">`)
		b.WriteString(`<div class="control-head"><span class="name">` + text(input.Name) + `</span> <span class="type">` + text(typeLabel(input)) + `</span>`)
		if input.Mode == compiler.DefaultedInput && input.Default != nil {
			b.WriteString(` <span class="mode">default ` + text(input.Default.Raw) + `</span>`)
		}
		b.WriteString(`</div>`)
		switch {
		case input.IsContent():
			if story.HasBody {
				b.WriteString(`<p class="note">Given by the story body.</p>`)
			} else {
				b.WriteString(`<p class="note">Not given; add it to the story body.</p>`)
			}
		case !storyInput(input):
			b.WriteString(bagNote)
		default:
			path := []any{input.Name}
			value := values[input.Name]
			switch input.Mode {
			case compiler.DefaultedInput:
				t.optional(&b, path, input.Type, value, "use default")
			case compiler.OptionalInput:
				t.optional(&b, path, input.Type, value, "None")
			default:
				t.control(&b, path, input.Type, value)
			}
		}
		if err := failures[input.Name]; err != nil {
			b.WriteString(`<p class="control-error">` + text(err.Error()) + `; the story's value renders</p>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</form>`)
	return b.String()
}

// bagNote stands for an attribute bag's control: a story gives only the
// empty bag.
const bagNote = `<p class="note">Empty attribute bag (a story gives no other).</p>`

func typeLabel(input compiler.Input) string {
	label := input.Type.String()
	if input.Mode == compiler.OptionalInput {
		label += "?"
	}
	return label
}

// optional renders the None/Some (or default) switch of an optional value
// and, when present, its control.
func (t typer) optional(b *strings.Builder, path []any, typ compiler.Type, value any, absent string) {
	op := "none"
	checked := ""
	if value == nil {
		op, checked = "some", " checked"
	}
	b.WriteString(`<label class="absent"><input type="checkbox" data-op="` + op + `" data-path="` + pathAttr(path) + `"` + checked + `> ` + text(absent) + `</label>`)
	if value != nil {
		t.control(b, path, typ, value)
	}
}

func (t typer) control(b *strings.Builder, path []any, typ compiler.Type, value any) {
	p := pathAttr(path)
	switch typ.Kind {
	case compiler.ScalarTypeKind:
		switch typ.Scalar {
		case compiler.BooleanType:
			checked := ""
			if flag, _ := value.(bool); flag {
				checked = " checked"
			}
			b.WriteString(`<input type="checkbox" data-op="set" data-kind="boolean" data-path="` + p + `"` + checked + `>`)
		case compiler.NumberType, compiler.IntegerType:
			step, kind := "any", "number"
			if typ.Scalar == compiler.IntegerType {
				step, kind = "1", "integer"
			}
			b.WriteString(`<input type="number" step="` + step + `" data-op="set" data-kind="` + kind + `" data-path="` + p + `" value="` + attr(scalarText(value)) + `">`)
		default:
			b.WriteString(`<input type="text" data-op="set" data-kind="string" data-path="` + p + `" value="` + attr(scalarText(value)) + `">`)
		}
	case compiler.ContextualTypeKind:
		if typ.Name == compiler.AttributesType {
			b.WriteString(bagNote)
			return
		}
		if typ.Name == compiler.URLType {
			b.WriteString(`<input type="text" data-op="set" data-kind="string" data-path="` + p + `" value="` + attr(scalarText(value)) + `">`)
			return
		}
		object, _ := value.(map[string]any)
		current, _ := object["fixture"].(string)
		fixtures := t.fixturesOf(typ)
		if len(fixtures) == 0 {
			b.WriteString(`<p class="note">Declare a fixture: ` + text(typ.String()) + ` in the story document to choose one.</p>`)
			return
		}
		b.WriteString(`<select data-op="set" data-kind="fixture" data-path="` + p + `">`)
		for _, fixture := range fixtures {
			selected := ""
			if fixture.Name == current {
				selected = " selected"
			}
			b.WriteString(`<option value="` + attr(fixture.Name) + `"` + selected + `>` + text(fixture.Name) + `</option>`)
		}
		b.WriteString(`</select>`)
	case compiler.RecordTypeKind:
		record, _ := t.snapshot.Record(typ.Record)
		object, _ := value.(map[string]any)
		b.WriteString(`<fieldset class="record">`)
		t.fields(b, path, record.Fields, object)
		b.WriteString(`</fieldset>`)
	case compiler.CollectionTypeKind:
		items, _ := value.([]any)
		b.WriteString(`<ol class="collection">`)
		for index, item := range items {
			itemPath := append(append([]any(nil), path...), index)
			b.WriteString(`<li>`)
			t.control(b, itemPath, *typ.Element, item)
			b.WriteString(`<button type="button" data-op="remove" data-path="` + pathAttr(itemPath) + `">Remove</button></li>`)
		}
		b.WriteString(`</ol><button type="button" data-op="add" data-path="` + p + `">Add ` + text(typ.Element.String()) + `</button>`)
	case compiler.StateTypeKind:
		state, _ := t.snapshot.State(typ.State)
		object, _ := value.(map[string]any)
		current, _ := object["variant"].(string)
		b.WriteString(`<select data-op="variant" data-path="` + p + `">`)
		for _, variant := range state.Variants {
			selected := ""
			if variant.Name == current {
				selected = " selected"
			}
			b.WriteString(`<option value="` + attr(variant.Name) + `"` + selected + `>` + text(variant.Name) + `</option>`)
		}
		b.WriteString(`</select>`)
		if variant, ok := state.Variant(current); ok && len(variant.Fields) > 0 {
			payload, _ := object["fields"].(map[string]any)
			b.WriteString(`<fieldset class="payload">`)
			t.fields(b, path, variant.Fields, payload)
			b.WriteString(`</fieldset>`)
		}
	default:
		b.WriteString(`<p class="note">No control for ` + text(typ.String()) + `.</p>`)
	}
}

func (t typer) fields(b *strings.Builder, path []any, fields []compiler.Field, object map[string]any) {
	for _, field := range fields {
		fieldPath := append(append([]any(nil), path...), field.Name)
		label := field.Type.String()
		if field.Optional {
			label += "?"
		}
		b.WriteString(`<div class="field"><div class="control-head"><span class="name">` + text(field.Name) + `</span> <span class="type">` + text(label) + `</span></div>`)
		if field.Optional {
			t.optional(b, fieldPath, field.Type, object[field.Name], "None")
		} else {
			t.control(b, fieldPath, field.Type, object[field.Name])
		}
		b.WriteString(`</div>`)
	}
}

func scalarText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func pathAttr(path []any) string {
	encoded, _ := json.Marshal(path)
	return attr(string(encoded))
}

func attr(value string) string { return html.EscapeString(value) }

func text(value string) string { return html.EscapeString(value) }
