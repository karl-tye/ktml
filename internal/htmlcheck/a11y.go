package htmlcheck

import (
	"fmt"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

// dynamicFor marks a label whose for value is interpolated: it may name any
// control.
const dynamicFor = "\x00dynamic"

// labelTargets returns the for values of every label in the workspace.
func labelTargets(snapshot *compiler.Snapshot) map[string]bool {
	targets := make(map[string]bool)
	var walk func([]ast.Node)
	walk = func(nodes []ast.Node) {
		for _, node := range nodes {
			switch value := node.(type) {
			case *ast.ElementDecl:
				walk(value.Body)
			case *ast.RenderClause:
				walk(value.Body)
			case *ast.ConditionNode:
				walk(value.Then)
				walk(value.Else)
			case *ast.RepetitionNode:
				walk(value.Body)
			case *ast.SlotNode:
				walk(value.Body)
			case *ast.ElementNode:
				for _, entry := range ast.NamedAttributes(value.Attributes) {
					switch {
					case strings.EqualFold(entry.Name, "for"):
						if literal, ok := literalValue(entry); ok {
							targets[literal] = true
						} else {
							targets[dynamicFor] = true
						}
					case strings.EqualFold(entry.Name, "x-bind:for"), entry.Name == ":for":
						targets[dynamicFor] = true
					}
				}
				walk(value.Children)
			}
		}
	}
	for _, document := range snapshot.Documents() {
		walk(document.Nodes)
	}
	return targets
}

// nameState is what static analysis knows of an accessible name.
type nameState int

const (
	unnamed nameState = iota
	named
	unknownName
)

func combine(a, b nameState) nameState {
	if a == named || b == named {
		return named
	}
	if a == unknownName || b == unknownName {
		return unknownName
	}
	return unnamed
}

// nameFrom computes whether content gives an element an accessible name.
func (a *analyzer) nameFrom(nodes []ast.Node) nameState {
	state := unnamed
	for _, node := range nodes {
		switch value := node.(type) {
		case *ast.TextNode:
			if value.Quoted == nil {
				continue
			}
			for _, segment := range value.Quoted.Segments {
				if segment.Kind == ast.PathSegment || strings.TrimSpace(segment.Literal) != "" {
					return named
				}
			}
		case *ast.ConditionNode:
			state = combine(state, combine(a.nameFrom(value.Then), a.nameFrom(value.Else)))
		case *ast.RepetitionNode:
			state = combine(state, a.nameFrom(value.Body))
		case *ast.SlotNode:
			state = combine(state, unknownName)
		case *ast.ElementNode:
			state = combine(state, a.elementName(value))
		}
		if state == named {
			return named
		}
	}
	return state
}

func (a *analyzer) elementName(element *ast.ElementNode) nameState {
	if _, ok := a.snapshot.Resolve(a.file, element.Name); ok || a.content[element.Name] || strings.Contains(element.Name, "-") {
		return unknownName
	}
	attributes := collectAttributes(element)
	if hidden, ok := attributes.static("aria-hidden"); ok && strings.EqualFold(hidden, "true") || attributes.has("hidden") {
		return unnamed
	}
	if attributes.has("aria-label", "aria-labelledby", "title") {
		return named
	}
	if attributes.spread || attributes.has("x-text", "x-html", ":aria-label", "x-bind:aria-label", ":title", "x-bind:title", "aria-hidden") {
		return unknownName
	}
	name := strings.ToLower(element.Name)
	switch name {
	case "img", "area":
		alt, has := attributes.get("alt")
		if !has {
			return unknownName
		}
		if value, static := literalValue(alt); static && strings.TrimSpace(value) == "" {
			return unnamed
		}
		return named
	case "input":
		return unknownName
	case "svg", "math":
		for _, child := range element.Children {
			if title, ok := child.(*ast.ElementNode); ok && strings.EqualFold(title.Name, "title") {
				return named
			}
		}
		return unnamed
	}
	return a.nameFrom(element.Children)
}

// interactiveDescendant reports elements that the content models of a and
// button forbid as descendants.
func interactiveDescendant(name string, attributes attributeSet) bool {
	switch name {
	case "button", "select", "textarea", "details", "iframe", "embed", "label":
		return true
	case "a":
		return attributes.has("href")
	case "input":
		value, static := attributes.static("type")
		return !static || !strings.EqualFold(value, "hidden")
	case "audio", "video":
		return attributes.has("controls")
	case "img":
		return attributes.has("usemap")
	}
	return attributes.has("tabindex")
}

func (a *analyzer) checkAccessibility(element *ast.ElementNode, name string, attributes attributeSet, stack []frame) {
	nameEnd := element.P + len(element.Name)
	hidden := attributes.has("hidden")
	if class, ok := attributes.static("class"); ok {
		// Tailwind's hidden and invisible remove the element from the
		// accessibility tree, as file inputs behind a styled button are.
		for _, token := range strings.Fields(class) {
			hidden = hidden || token == "hidden" || token == "invisible"
		}
	}
	if value, ok := attributes.static("aria-hidden"); ok && strings.EqualFold(value, "true") {
		hidden = true
	}
	inputType, staticType := "text", true
	if entry, ok := attributes.get("type"); ok {
		inputType, staticType = literalValue(entry)
		inputType = strings.ToLower(strings.TrimSpace(inputType))
	}
	if attributes.bound("type") {
		staticType = false
	}

	switch {
	case name == "img" || name == "input" && staticType && inputType == "image":
		role, _ := attributes.static("role")
		role = strings.ToLower(strings.TrimSpace(role))
		if hidden || role == "none" || role == "presentation" || attributes.has("alt", "aria-label", "aria-labelledby", "title") || attributes.bound("alt") || attributes.bound("aria-label") {
			break
		}
		fix := Fix{Title: `Mark the image decorative (alt="")`, Edits: []Edit{a.insertAttribute(element, `alt=""`)}}
		a.report(element.P, nameEnd, Warning, "a11y-missing-alt", fmt.Sprintf("%s has no alt text; describe the image with alt, or use alt=\"\" if it is decorative", name), fix)
	}

	switch name {
	case "input", "select", "textarea":
		if name == "input" && (!staticType || inputType == "hidden" || inputType == "submit" || inputType == "reset" || inputType == "button" || inputType == "image") {
			break
		}
		if hidden || attributes.has("aria-label", "aria-labelledby", "title", "placeholder") || attributes.bound("aria-label") || attributes.bound("aria-labelledby") || attributes.bound("title") || attributes.bound("id") {
			break
		}
		if entry, ok := attributes.get("id"); ok {
			id, static := literalValue(entry)
			if !static || a.labelFor[id] || a.labelFor[dynamicFor] {
				break
			}
		}
		if inCall(stack) || a.wrappable(stack) || element == a.firstLabelable && a.callerMayWrap(a.componentName, a.file, 0) {
			// A caller may wrap the slot content, or the component, in a
			// label, which labels the first labelable element in it.
			break
		}
		a.report(element.P, nameEnd, Warning, "a11y-missing-label", fmt.Sprintf("%s has no label; wrap it in a label, point a label for at its id, or give it aria-label", name))
	}

	switch {
	case name == "button", name == "a" && attributes.has("href"), name == "input" && staticType && inputType == "button":
		if hidden || attributes.has("aria-label", "aria-labelledby", "title", "role") || attributes.spread ||
			attributes.has("x-text", "x-html", ":aria-label", "x-bind:aria-label", ":title", "x-bind:title", ":aria-labelledby", "x-bind:aria-labelledby") {
			break
		}
		if name == "input" {
			if !attributes.has("value") && !attributes.bound("value") {
				a.report(element.P, nameEnd, Warning, "a11y-missing-name", "input type=\"button\" has no accessible name; give it a value or aria-label")
			}
			break
		}
		if a.nameFrom(element.Children) == unnamed {
			a.report(element.P, nameEnd, Warning, "a11y-missing-name", fmt.Sprintf("%s has no accessible name; add text content, aria-label, or aria-labelledby", name))
		}
	}

	if interactiveDescendant(name, attributes) || name == "a" {
		for _, ancestor := range ancestors(stack) {
			if ancestor.name != "a" && ancestor.name != "button" {
				continue
			}
			if name == "a" && ancestor.name == "button" && !attributes.has("href") {
				break
			}
			a.report(element.P, nameEnd, Warning, "a11y-nested-interactive", fmt.Sprintf("%s cannot be inside %s; nested interactive elements confuse keyboard focus, clicks, and screen readers", name, ancestor.name))
			break
		}
	}
}

// labelable are the elements a label can label.
func labelable(element *ast.ElementNode) bool {
	switch strings.ToLower(element.Name) {
	case "button", "meter", "output", "progress", "select", "textarea":
		return true
	case "input":
		value, static := collectAttributes(element).static("type")
		return !static || !strings.EqualFold(value, "hidden")
	}
	return false
}

// firstLabelable returns the first labelable element of a component body
// outside component call content.
func firstLabelable(a *analyzer, body []ast.Node) *ast.ElementNode {
	var found *ast.ElementNode
	var walk func([]ast.Node)
	walk = func(nodes []ast.Node) {
		for _, node := range nodes {
			if found != nil {
				return
			}
			switch value := node.(type) {
			case *ast.ConditionNode:
				walk(value.Then)
				walk(value.Else)
			case *ast.RepetitionNode:
				walk(value.Body)
			case *ast.ElementNode:
				if _, call := a.snapshot.Resolve(a.file, value.Name); call {
					continue
				}
				if labelable(value) {
					found = value
					return
				}
				walk(value.Children)
			}
		}
	}
	walk(body)
	return found
}

// callerMayWrap reports whether some call of a component may sit inside a
// label: lexically, through content passed to another component, or at
// the root of a caller that may itself be wrapped. A component nothing
// calls is rendered from Go and is never wrapped.
func (a *analyzer) callerMayWrap(name, file string, depth int) bool {
	component, ok := a.snapshot.Resolve(file, name)
	if !ok || depth > 4 {
		return true
	}
	for _, reference := range a.snapshot.References(component.ID) {
		if reference.Kind != compiler.CallReference {
			continue
		}
		document := a.snapshot.Document(reference.Range.File)
		if document == nil {
			return true
		}
		caller, chain, content, found := callChain(document, reference.Range.Start)
		if !found || content {
			return true
		}
		for _, ancestor := range chain {
			if ancestor == "label" {
				return true
			}
		}
		if len(chain) == 0 && a.callerMayWrap(caller, reference.Range.File, depth+1) {
			return true
		}
	}
	return false
}

// callChain finds the call element at offset and returns its component,
// its element ancestors, and whether it is content passed to another call.
func callChain(document *ast.Document, offset int) (string, []string, bool, bool) {
	var chain []string
	content, found := false, false
	var walk func([]ast.Node, []string, bool) bool
	walk = func(nodes []ast.Node, ancestors []string, inContent bool) bool {
		for _, node := range nodes {
			switch value := node.(type) {
			case *ast.ConditionNode:
				if walk(value.Then, ancestors, inContent) || walk(value.Else, ancestors, inContent) {
					return true
				}
			case *ast.RepetitionNode:
				if walk(value.Body, ancestors, inContent) {
					return true
				}
			case *ast.SlotNode:
				if walk(value.Body, ancestors, true) {
					return true
				}
			case *ast.ElementNode:
				if value.P == offset {
					chain, content, found = ancestors, inContent, true
					return true
				}
				nested := append(append([]string(nil), ancestors...), strings.ToLower(value.Name))
				nestedContent := inContent || startsUpper(value.Name)
				if walk(value.Children, nested, nestedContent) {
					return true
				}
			}
		}
		return false
	}
	for _, node := range document.Nodes {
		var name string
		var body []ast.Node
		switch declaration := node.(type) {
		case *ast.ElementDecl:
			name, body = declaration.Name, declaration.Body
		case *ast.RenderClause:
			name, body = declaration.Name, declaration.Body
		default:
			continue
		}
		if walk(body, nil, false) {
			return name, chain, content, found
		}
	}
	return "", nil, false, false
}

// wrappable reports whether a caller could wrap the element in a label: its
// ancestors in the component are all phrasing content, which a label may
// contain.
func (a *analyzer) wrappable(stack []frame) bool {
	for _, ancestor := range ancestors(stack) {
		if ancestor.name == "label" {
			return true
		}
	}
	for _, ancestor := range ancestors(stack) {
		info, ok := LookupElement(ancestor.name)
		if ok && info.flags&phrasing == 0 {
			return false
		}
	}
	return true
}

// insertAttribute adds an attribute to an element's header.
func (a *analyzer) insertAttribute(element *ast.ElementNode, attribute string) Edit {
	if element.AttributeGroup != nil {
		return Edit{element.AttributeGroup.Start + 1, element.AttributeGroup.Start + 1, attribute + " "}
	}
	at := element.P + len(element.Name)
	return Edit{at, at, " " + attribute}
}
