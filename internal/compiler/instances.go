package compiler

import "github.com/karl-tye/ktml/internal/ast"

// InstanceShapeKind is how the development variant marks a component instance
// rendered by one default or clause body (ADR-0004).
type InstanceShapeKind int

const (
	// MultiRootInstance bodies are surrounded by ktml:c boundary comments.
	MultiRootInstance InstanceShapeKind = iota
	// SingleRootInstance bodies are exactly one HTML element, which carries
	// the identity as its data-ktml-i attribute.
	SingleRootInstance
	// DocumentInstance bodies may render a whole document: they start with
	// doctype, or are exactly one call of a document component. Nothing may
	// precede a doctype, so they carry no marker and are patched as the whole
	// document.
	DocumentInstance
)

// InstanceShape classifies a default or clause body of a component declared in
// file. markup reports whether an element node writes an HTML element rather
// than calling a component or rendering a content outlet.
func (s *Snapshot) InstanceShape(file string, body []ast.Node, markup func(*ast.ElementNode) bool) InstanceShapeKind {
	if s.documentBody(file, body, make(map[SymbolID]bool)) {
		return DocumentInstance
	}
	if len(body) == 1 {
		if element, ok := body[0].(*ast.ElementNode); ok && markup(element) {
			return SingleRootInstance
		}
	}
	return MultiRootInstance
}

// DocumentComponent reports whether any default or clause body of a component
// may render a whole document.
func (s *Snapshot) DocumentComponent(id SymbolID) bool {
	return s.documentComponent(id, make(map[SymbolID]bool))
}

func (s *Snapshot) documentBody(file string, body []ast.Node, visiting map[SymbolID]bool) bool {
	if len(body) == 0 {
		return false
	}
	if _, document := body[0].(*ast.DoctypeNode); document {
		return true
	}
	call, ok := body[0].(*ast.ElementNode)
	if !ok || len(body) != 1 || s == nil {
		return false
	}
	path, err := canonicalPath(file)
	if err != nil {
		return false
	}
	callee, ok := s.visible[path][call.Name]
	return ok && s.documentComponent(callee, visiting)
}

func (s *Snapshot) documentComponent(id SymbolID, visiting map[SymbolID]bool) bool {
	if s == nil || visiting[id] {
		return false
	}
	visiting[id] = true
	component, ok := s.components[id]
	if !ok {
		return false
	}
	if group, grouped := s.groups[id]; grouped {
		for _, clause := range group.Clauses {
			if s.documentBody(component.File, clause.Body, visiting) {
				return true
			}
		}
		return s.documentBody(component.File, group.DefaultBody, visiting)
	}
	file := s.files[component.File]
	if file.doc == nil {
		return false
	}
	for _, node := range file.doc.Nodes {
		if declaration, ok := node.(*ast.ElementDecl); ok && declaration.Name == component.Name && declaration.Pos() == component.Range.Start {
			return s.documentBody(component.File, declaration.Body, visiting)
		}
	}
	return false
}
