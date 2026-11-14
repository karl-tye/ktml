package htmlcheck

import (
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

// LSP CompletionItemKind values.
const (
	KindProperty = 10
	KindValue    = 12
	KindKeyword  = 14
	KindEnum     = 20
)

// Item is a completion item that replaces source[Start:End] with Text.
type Item struct {
	Label         string
	Kind          int
	Detail        string
	Documentation string
	Start, End    int
	Text          string
}

// Complete returns HTML completions at offset: element names, attribute
// names of HTML elements, and keyword attribute values. component reports
// whether a name is a component in scope; components complete through the
// KTML tooling, and framework attributes through their analyzers.
func Complete(source []byte, offset int, component func(string) bool) []Item {
	cursor := CursorAt(source, offset)
	var items []Item
	add := func(label string, kind int, detail, documentation string) {
		if !strings.HasPrefix(strings.ToLower(label), strings.ToLower(cursor.Prefix)) {
			return
		}
		items = append(items, Item{Label: label, Kind: kind, Detail: detail, Documentation: documentation, Start: cursor.PrefixStart, End: offset, Text: label})
	}
	switch cursor.Kind {
	case ElementName:
		if startsUpper(cursor.Prefix) {
			return nil
		}
		for _, element := range Elements() {
			add(element.Name, KindProperty, "HTML element", element.Description+"\n\n"+mdnElement(element.Name))
		}
	case AttributeName:
		// A bag's element is unknown: it offers the global attributes.
		if !cursor.Bag && !htmlElement(cursor.Element, component) || frameworkAttribute(cursor.Prefix) {
			return nil
		}
		name := strings.ToLower(cursor.Element)
		for _, attribute := range ElementAttributes(name) {
			description, _ := AttributeDescription(attribute)
			detail := name + " attribute"
			if cursor.Bag {
				detail = "global attribute"
			}
			add(attribute, KindProperty, detail, description)
		}
		for _, attribute := range AriaAttributes() {
			add(attribute, KindProperty, "ARIA", ariaAttributes[attribute].description)
		}
		if strings.HasPrefix(cursor.Prefix, "on") {
			for _, handler := range EventHandlers() {
				add(handler, KindProperty, "event handler", "")
			}
		}
	case AttributeValue:
		if !cursor.Bag && !htmlElement(cursor.Element, component) {
			return nil
		}
		values, _ := AttributeValues(cursor.Element, cursor.Attribute)
		for _, value := range values {
			if value != "" {
				add(value, KindEnum, strings.ToLower(cursor.Attribute)+" value", "")
			}
		}
	}
	return items
}

func htmlElement(name string, component func(string) bool) bool {
	if name == "" || component != nil && component(name) {
		return false
	}
	info, ok := LookupElement(name)
	return ok && info.flags&foreign == 0
}

func mdnElement(name string) string {
	return "[MDN](https://developer.mozilla.org/docs/Web/HTML/Element/" + name + ")"
}

// Hover documents the HTML element or attribute name at offset, returning
// markdown and the range of the name.
func Hover(snapshot *compiler.Snapshot, file string, offset int) (string, int, int, bool) {
	document := snapshot.Document(file)
	if document == nil {
		return "", 0, 0, false
	}
	var result string
	var start, end int
	found := false
	var visit func([]ast.Node)
	visit = func(nodes []ast.Node) {
		for _, node := range nodes {
			if found {
				return
			}
			switch value := node.(type) {
			case *ast.ElementDecl:
				visit(value.Body)
			case *ast.RenderClause:
				visit(value.Body)
			case *ast.ConditionNode:
				visit(value.Then)
				visit(value.Else)
			case *ast.RepetitionNode:
				visit(value.Body)
			case *ast.SlotNode:
				visit(value.Body)
			case *ast.ElementNode:
				if offset < value.P || offset > value.E {
					continue
				}
				if _, call := snapshot.Resolve(file, value.Name); call {
					visit(value.Children)
					continue
				}
				info, known := LookupElement(value.Name)
				if known && offset >= value.P && offset <= value.P+len(value.Name) {
					result = "```html\n<" + info.Name + ">\n```\n" + info.Description
					if info.Void() {
						result += " A void element: it has no content."
					}
					result += "\n\n" + mdnElement(info.Name)
					start, end, found = value.P, value.P+len(value.Name), true
					return
				}
				if known && info.flags&foreign == 0 {
					for _, entry := range ast.NamedAttributes(value.Attributes) {
						nameEnd := entry.P + len(entry.Name)
						if entry.Name == "" || offset < entry.P || offset > nameEnd {
							continue
						}
						description, ok := AttributeDescription(entry.Name)
						if !ok {
							continue
						}
						result = "```html\n" + strings.ToLower(entry.Name) + "\n```\n" + description
						if values, _ := AttributeValues(info.Name, entry.Name); len(values) > 0 && len(values) <= 12 {
							result += "\n\nValues: " + quoteList(values)
						}
						start, end, found = entry.P, nameEnd, true
						return
					}
				}
				visit(value.Children)
			}
		}
	}
	visit(document.Nodes)
	return result, start, end, found
}
