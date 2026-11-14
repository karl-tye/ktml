package alpine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/htmlcheck"
)

// ReferenceAt returns the reference whose name covers offset.
func (d *Document) ReferenceAt(offset int) *Reference {
	for _, reference := range d.References {
		if offset >= reference.Start && offset <= reference.End {
			return reference
		}
	}
	return nil
}

// BindingAt returns the binding declared at offset.
func (d *Document) BindingAt(offset int) *Binding {
	for _, binding := range d.Bindings {
		if offset >= binding.Start && offset <= binding.End {
			return binding
		}
	}
	return nil
}

// SymbolAt returns the binding at offset, declared or referenced there.
func (d *Document) SymbolAt(offset int) *Binding {
	if reference := d.ReferenceAt(offset); reference != nil && reference.Binding != nil {
		return reference.Binding
	}
	return d.BindingAt(offset)
}

// ReferencesTo returns the references bound to binding, in source order.
func (d *Document) ReferencesTo(binding *Binding) []*Reference {
	var references []*Reference
	for _, reference := range d.References {
		if reference.Binding == binding {
			references = append(references, reference)
		}
	}
	sort.Slice(references, func(i, j int) bool { return references[i].Start < references[j].Start })
	return references
}

// ExpressionAt returns the Alpine value holding a source offset.
func (d *Document) ExpressionAt(offset int) *Expression {
	for _, expression := range d.Expressions {
		quoted := expression.Attribute.Quoted
		if quoted != nil && offset > quoted.P && offset < quoted.E {
			return expression
		}
	}
	return nil
}

// Describe documents a binding for hover.
func Describe(binding *Binding, line int) string {
	kind := binding.Kind.String()
	declared := "x-data"
	switch binding.Kind {
	case ForItem, ForIndex:
		declared = "x-for"
	case Ref:
		return fmt.Sprintf("```js\n$refs.%s: HTMLElement\n```\nThe element with x-ref=\"%s\", line %d.", binding.Name, binding.Name, line)
	case Local:
		declared = "the expression"
	}
	return fmt.Sprintf("```js\n(%s) %s: %s\n```\nDeclared by %s at line %d.", kind, binding.Name, binding.Type, declared, line)
}

// Item is an Alpine completion that replaces source[Start:End] with Text.
type Item struct {
	Label, Detail, Documentation string
	Kind                         int
	Start, End                   int
	Text                         string
}

// Complete returns Alpine completions at a source offset: directive,
// event, and modifier names in attribute position, and names in scope in
// an Alpine value.
func (d *Document) Complete(source []byte, offset int) []Item {
	cursor := htmlcheck.CursorAt(source, offset)
	switch cursor.Kind {
	case htmlcheck.AttributeName:
		return completeDirective(cursor, offset)
	case htmlcheck.AttributeValue:
		if expression := d.ExpressionAt(offset); expression != nil {
			return d.completeValue(expression, offset)
		}
	}
	return nil
}

func completeDirective(cursor htmlcheck.Cursor, offset int) []Item {
	prefix := cursor.Prefix
	var items []Item
	add := func(label, detail, documentation string, start int) {
		items = append(items, Item{Label: label, Detail: detail, Documentation: documentation, Kind: 14, Start: start, End: offset, Text: label})
	}
	if dot := strings.LastIndex(prefix, "."); dot > 0 {
		directive, ok := ParseDirective(prefix[:dot])
		if !ok {
			return nil
		}
		typed := prefix[dot+1:]
		for _, modifier := range Modifiers(directive) {
			if strings.HasPrefix(modifier, typed) {
				add(modifier, "modifier", "", cursor.PrefixStart+dot+1)
			}
		}
		return items
	}
	switch {
	case strings.HasPrefix(prefix, "@") || strings.HasPrefix(prefix, "x-on:"):
		head := prefix[:strings.Index(prefix, "@")+1]
		if strings.HasPrefix(prefix, "x-on:") {
			head = "x-on:"
		}
		for _, event := range Events() {
			if strings.HasPrefix(head+event, prefix) {
				add(head+event, "event", "Runs code when "+event+" fires.", cursor.PrefixStart)
			}
		}
	case strings.HasPrefix(prefix, ":") || strings.HasPrefix(prefix, "x-bind:"):
		head := ":"
		if strings.HasPrefix(prefix, "x-bind:") {
			head = "x-bind:"
		}
		for _, attribute := range []string{"class", "style", "disabled", "hidden", "href", "value", "checked", "selected", "id", "title", "aria-expanded", "aria-hidden", "aria-label", "key"} {
			if strings.HasPrefix(head+attribute, prefix) {
				add(head+attribute, "bound attribute", "", cursor.PrefixStart)
			}
		}
	case strings.HasPrefix(prefix, "x"):
		for _, name := range Directives() {
			if strings.HasPrefix("x-"+name, prefix) {
				description, _ := DirectiveDescription(name)
				add("x-"+name, "Alpine directive", description, cursor.PrefixStart)
			}
		}
	}
	return items
}

func (d *Document) completeValue(expression *Expression, offset int) []Item {
	text := expression.Text
	virtual := text.Offset(offset)
	if virtual < 0 {
		return nil
	}
	value := text.Value
	start := virtual
	for start > 0 && identPart(value[start-1]) {
		start--
	}
	prefix := value[start:virtual]
	sourceStart := text.Source(start)
	var items []Item
	seen := make(map[string]bool)
	add := func(label string, t *Type, detail string) {
		if seen[label] || !strings.HasPrefix(label, prefix) {
			return
		}
		seen[label] = true
		if t != nil {
			detail = t.String()
		}
		items = append(items, Item{Label: label, Detail: detail, Kind: 6, Start: sourceStart, End: offset, Text: label})
	}
	if start > 0 && value[start-1] == '.' {
		object := start - 1
		for object > 0 && (identPart(value[object-1]) || value[object-1] == '.') {
			object--
		}
		path := strings.Split(value[object:start-1], ".")
		switch {
		case len(path) == 1 && path[0] == "this", len(path) == 1 && path[0] == "$data":
			scope := expression.Method
			if scope == nil {
				scope = expression.Scope.dataRoot()
			}
			for ; scope != nil && scope.Kind != OpaqueScope; scope = scope.Parent {
				for _, binding := range scope.Bindings {
					if scope.Kind == DataScope {
						add(binding.Name, binding.Type, "")
					}
				}
			}
		case len(path) >= 1 && path[len(path)-1] == "$refs":
			root := expression.Scope.dataRoot()
			if expression.Method != nil {
				root = expression.Method
			}
			if root != nil {
				names := make([]string, 0, len(root.Refs))
				for name := range root.Refs {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					add(name, nil, "HTMLElement")
				}
			}
		default:
			t := d.lookupType(expression, path[0])
			for _, field := range path[1:] {
				t = fieldType(t, field)
			}
			if t != nil {
				for _, field := range t.Fields {
					add(field.Name, field.Type, "")
				}
			}
		}
		return items
	}
	for scope := expression.Scope; scope != nil && scope.Kind != OpaqueScope; scope = scope.Parent {
		for _, binding := range scope.Bindings {
			add(binding.Name, binding.Type, "")
		}
	}
	for _, magic := range Magics() {
		if magic == "$event" && !expression.Handler {
			continue
		}
		add(magic, nil, "Alpine magic")
	}
	return items
}

func fieldType(t *Type, name string) *Type {
	if field := t.Field(name); field != nil {
		return field.Type
	}
	return nil
}

func (d *Document) lookupType(expression *Expression, name string) *Type {
	for scope := expression.Scope; scope != nil && scope.Kind != OpaqueScope; scope = scope.Parent {
		if binding := scope.lookup(name); binding != nil {
			return binding.Type
		}
	}
	return nil
}

// Hover documents the Alpine name, directive, or magic at offset.
func (d *Document) Hover(source []byte, offset int) (string, int, int, bool) {
	line := func(at int) int { return strings.Count(string(source[:at]), "\n") + 1 }
	if reference := d.ReferenceAt(offset); reference != nil {
		if reference.Binding != nil {
			return Describe(reference.Binding, line(reference.Binding.Start)), reference.Start, reference.End, true
		}
		if text, ok := MagicDescription(reference.Name); ok {
			return "```js\n" + reference.Name + "\n```\n" + text, reference.Start, reference.End, true
		}
		return "", 0, 0, false
	}
	if binding := d.BindingAt(offset); binding != nil {
		return Describe(binding, line(binding.Start)), binding.Start, binding.End, true
	}
	for _, expression := range d.Expressions {
		entry := expression.Attribute
		if offset < entry.P || offset > entry.P+len(entry.Name) {
			continue
		}
		description, ok := DirectiveDescription(expression.Directive.Name)
		if !ok {
			return "", 0, 0, false
		}
		return "```html\nx-" + expression.Directive.Name + "\n```\n" + description + "\n\n[Alpine docs](https://alpinejs.dev/directives/" + expression.Directive.Name + ")", entry.P, entry.P + len(entry.Name), true
	}
	return "", 0, 0, false
}
