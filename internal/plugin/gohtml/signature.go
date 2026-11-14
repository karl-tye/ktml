package gohtml

import (
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/htmlcheck"
)

// callSignature is the signature help of the component call at offset,
// Name [input: type, …], with the argument being written active: the
// attribute whose name or value holds the cursor, the input an attribute bag
// literal fills, or the slot a slot line names. Otherwise the first
// required argument not yet given is active. It reads the source lexically,
// so a call still being typed is found.
func callSignature(path string, source []byte, offset int) (map[string]any, bool) {
	cursor := htmlcheck.CursorAt(source, offset)
	var element, active string
	slot := false
	switch {
	case cursor.Bag:
		element, active = cursor.BagElement, cursor.BagAttribute
	case cursor.Kind == htmlcheck.AttributeValue:
		element, active = cursor.Element, cursor.Attribute
	case cursor.Kind == htmlcheck.AttributeName:
		element, active = cursor.Element, cursor.Prefix
	case cursor.Slot:
		element, slot = cursor.Body, true
	default:
		return nil, false
	}
	if element == "" || element[0] < 'A' || element[0] > 'Z' {
		return nil, false
	}
	component, ok := callTarget(path, element)
	if !ok || len(component.Inputs) == 0 {
		return nil, false
	}

	supplied := make(map[string]bool)
	for _, name := range cursor.Supplied {
		supplied[name] = true
	}
	label := component.Name + " ["
	parameters := make([]map[string]any, 0, len(component.Inputs))
	activeIndex, prefixIndex := -1, len(component.Inputs)
	for index, input := range component.Inputs {
		if index > 0 {
			label += ", "
		}
		start := len(utf16Prefix(label))
		label += inputSpelling(input)
		parameters = append(parameters, map[string]any{
			"label":         []int{start, len(utf16Prefix(label))},
			"documentation": map[string]any{"kind": "markdown", "value": inputDocumentation(input)},
		})
		switch {
		case input.Name == active && slot == input.IsContent():
			activeIndex = index
		case slot && input.IsContent() && input.Name != compiler.ChildrenInput && strings.HasPrefix(input.Name, cursor.Prefix):
			prefixIndex = min(prefixIndex, index)
		case !slot && active != "" && !input.IsContent() && !supplied[input.Name] && strings.HasPrefix(input.Name, active):
			// A name being typed selects the first input it begins.
			prefixIndex = min(prefixIndex, index)
		}
	}
	if activeIndex < 0 && prefixIndex < len(component.Inputs) {
		activeIndex = prefixIndex
	}
	label += "]"
	if activeIndex < 0 && !slot {
		for index, input := range component.Inputs {
			if !input.IsContent() && input.Mode == compiler.RequiredInput && !supplied[input.Name] {
				activeIndex = index
				break
			}
		}
	}
	signature := map[string]any{"label": label, "parameters": parameters}
	help := map[string]any{"signatures": []any{signature}, "activeSignature": 0}
	if activeIndex >= 0 {
		signature["activeParameter"] = activeIndex
		help["activeParameter"] = activeIndex
	}
	return help, true
}

// callTarget resolves a called component in path's scope: from the latest
// analysis, or, while the document does not analyze, the last one that did.
func callTarget(path, name string) (compiler.Component, bool) {
	var snapshots []*compiler.Snapshot
	if snapshot, err := semanticSnapshot(path); err == nil {
		snapshots = append(snapshots, snapshot)
	}
	snapshots = append(snapshots, semanticWorkspace(path).LastValid(), editingWorkspace(path).LastValid())
	for _, snapshot := range snapshots {
		if component, ok := snapshot.Resolve(path, name); ok {
			return component, true
		}
	}
	return compiler.Component{}, false
}

// inputSpelling writes an input as its declaration does.
func inputSpelling(input compiler.Input) string {
	switch {
	case input.Mode == compiler.DefaultedInput && input.Default != nil:
		return input.Name + ": " + input.Type.String() + " = " + input.Default.Raw
	case input.Mode == compiler.OptionalInput:
		return input.Name + "?: " + input.Type.String()
	}
	return input.Name + ": " + input.Type.String()
}

func inputDocumentation(input compiler.Input) string {
	switch {
	case input.Name == compiler.ChildrenInput && input.IsContent():
		return "Children: the call body's content."
	case input.IsContent():
		return "Named slot: fill it in the call body with `slot " + input.Name + " { … }`."
	case input.Type.Kind == compiler.ContextualTypeKind && input.Type.Name == compiler.AttributesType:
		return "Attribute bag: `" + input.Name + "=( … )`."
	case input.Mode == compiler.DefaultedInput && input.Default != nil:
		return "Defaults to `" + input.Default.Raw + "`."
	case input.Mode == compiler.OptionalInput:
		return "Optional."
	}
	return "Required."
}

// utf16Prefix returns s as UTF-16 code units, for LSP label offsets.
func utf16Prefix(s string) []uint16 {
	var units []uint16
	for _, r := range s {
		if r >= 0x10000 {
			units = append(units, 0, 0)
		} else {
			units = append(units, 0)
		}
	}
	return units
}
