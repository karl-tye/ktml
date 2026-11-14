package compiler

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/karl-tye/ktml/internal/ast"
)

// ContentUseKind distinguishes rendering content from supplying it.
type ContentUseKind string

const (
	// OutletUse renders a content input of the enclosing component.
	OutletUse ContentUseKind = "outlet"
	// FillUse supplies a named slot of a called component.
	FillUse ContentUseKind = "fill"
)

// ContentUse is one outlet or slot fill. Component owns Input: the enclosing
// component for an outlet, the called component for a fill. Range is the
// outlet name or the fill's slot name.
type ContentUse struct {
	Kind      ContentUseKind
	Component SymbolID
	Input     string
	Range     SourceRange
}

// resolveOutlet records a bare identifier naming a content input of the
// enclosing component as an outlet. It reports false for every other element.
func (s *Snapshot) resolveOutlet(path string, caller pathScope, element *ast.ElementNode) bool {
	input, ok := inputNamed(caller.component, element.Name)
	if !ok || !input.IsContent() {
		if element.Name == ChildrenInput && caller.component.ID != "" && !ok {
			if _, grouped := s.groups[caller.component.ID]; grouped || s.isSignature(caller.component) {
				s.addDiagnostic(path, "undeclared-content", fmt.Sprintf("render clauses of %s cannot render children; declare children?: content in the Component Signature", caller.component.Name), element.Pos(), element.Pos()+len(element.Name))
				return true
			}
		}
		return false
	}
	nameRange := SourceRange{File: path, Start: element.Pos(), End: element.Pos() + len(element.Name)}
	if len(element.Attributes) > 0 || len(element.Children) > 0 {
		s.addDiagnostic(path, "outlet-with-content", fmt.Sprintf("%s is a content input of %s; an outlet takes no attributes or body", element.Name, caller.component.Name), element.Pos(), element.End())
		return true
	}
	s.contentUses[path] = append(s.contentUses[path], ContentUse{Kind: OutletUse, Component: caller.component.ID, Input: input.Name, Range: nameRange})
	if caller.sources != nil {
		s.outletSources[element] = "o" + strconv.Itoa(caller.sources.outlets)
		caller.sources.outlets++
	}
	return true
}

// CallSource returns the c<n> Render Identity source of a validated component
// call: its position among the calling component's calls in source pre-order,
// across its default and clause bodies.
func (s *Snapshot) CallSource(call *ast.ElementNode) (string, bool) {
	if s == nil {
		return "", false
	}
	source, ok := s.callSources[call]
	return source, ok
}

// OutletSource returns the o<n> Render Identity source of a validated content
// outlet, numbered like CallSource.
func (s *Snapshot) OutletSource(outlet *ast.ElementNode) (string, bool) {
	if s == nil {
		return "", false
	}
	source, ok := s.outletSources[outlet]
	return source, ok
}

// isSignature reports whether a component is declared by a Component
// Signature, whose clause group may not have resolved.
func (s *Snapshot) isSignature(component Component) bool {
	file := s.files[component.File]
	if file.doc == nil {
		return false
	}
	for _, node := range file.doc.Nodes {
		if declaration, ok := node.(*ast.ElementDecl); ok && declaration.Name == component.Name && declaration.Pos() == component.Range.Start {
			return declaration.IsSignature()
		}
	}
	return false
}

// resolveSlotFill validates one slot fill against the called component.
func (s *Snapshot) resolveSlotFill(path string, caller pathScope, target Component, slot *ast.SlotNode) {
	nameRange := SourceRange{File: path, Start: slot.NameRange.Start, End: slot.NameRange.End}
	if slot.Name == ChildrenInput {
		s.addDiagnostic(path, "slot-children", fmt.Sprintf("children of %s are the unlabelled call body; remove slot children", target.Name), slot.KeywordRange.Start, slot.NameRange.End)
		return
	}
	input, ok := inputNamed(target, slot.Name)
	if !ok || !input.IsContent() {
		s.addDiagnostic(path, "unknown-slot", fmt.Sprintf("component %s has no content input %s", target.Name, slot.Name), nameRange.Start, nameRange.End)
		return
	}
	s.contentUses[path] = append(s.contentUses[path], ContentUse{Kind: FillUse, Component: target.ID, Input: input.Name, Range: nameRange})
}

// validateCallContent checks the slot fills and unlabelled children of a call.
func (s *Snapshot) validateCallContent(path string, target Component, call *ast.ElementNode) {
	filled := make(map[string]bool)
	hasChildren := false
	for _, child := range call.Children {
		slot, fill := child.(*ast.SlotNode)
		if !fill {
			hasChildren = true
			continue
		}
		if slot.Name == ChildrenInput {
			continue
		}
		if filled[slot.Name] {
			s.addDiagnostic(path, "duplicate-slot", fmt.Sprintf("slot %s of %s is filled more than once", slot.Name, target.Name), slot.KeywordRange.Start, slot.NameRange.End)
		}
		filled[slot.Name] = true
	}
	children, acceptsChildren := inputNamed(target, ChildrenInput)
	acceptsChildren = acceptsChildren && children.IsContent()
	if hasChildren && !acceptsChildren {
		s.addDiagnostic(path, "unexpected-children", fmt.Sprintf("component %s does not render children", target.Name), call.Pos(), call.End())
	}
	for _, input := range target.Inputs {
		if !input.IsContent() || input.Mode != RequiredInput {
			continue
		}
		if input.Name == ChildrenInput && !hasChildren {
			s.addDiagnostic(path, "missing-content", fmt.Sprintf("component %s requires children; supply them in the call body", target.Name), call.Pos(), call.Pos()+len(call.Name))
		} else if input.Name != ChildrenInput && !filled[input.Name] {
			s.addDiagnostic(path, "missing-content", fmt.Sprintf("component %s requires slot %s; fill it with slot %s { … }", target.Name, input.Name, input.Name), call.Pos(), call.Pos()+len(call.Name))
		}
	}
}

// ContentUses returns the outlets and slot fills of one component document in
// source order.
func (s *Snapshot) ContentUses(file string) []ContentUse {
	if s == nil {
		return nil
	}
	path, err := canonicalPath(file)
	if err != nil {
		return nil
	}
	uses := append([]ContentUse(nil), s.contentUses[path]...)
	sort.Slice(uses, func(i, j int) bool { return uses[i].Range.Start < uses[j].Range.Start })
	return uses
}

// ContentUseAt returns the outlet or slot fill name at offset.
func (s *Snapshot) ContentUseAt(file string, offset int) (ContentUse, bool) {
	for _, use := range s.ContentUses(file) {
		if offset >= use.Range.Start && offset <= use.Range.End {
			return use, true
		}
	}
	return ContentUse{}, false
}

// ContentReferences returns every outlet and fill of one content input across
// the workspace, ordered by file and offset.
func (s *Snapshot) ContentReferences(component SymbolID, input string) []SourceRange {
	if s == nil {
		return nil
	}
	var ranges []SourceRange
	for _, uses := range s.contentUses {
		for _, use := range uses {
			if use.Component == component && use.Input == input {
				ranges = append(ranges, use.Range)
			}
		}
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].File != ranges[j].File {
			return ranges[i].File < ranges[j].File
		}
		return ranges[i].Start < ranges[j].Start
	})
	return ranges
}

// ContentSignature renders a content input for hover text.
func ContentSignature(component Component, input Input) string {
	kind := "slot"
	if input.Name == ChildrenInput {
		kind = "children of " + component.Name
		marker := ":"
		if input.Mode == OptionalInput {
			marker = "?:"
		}
		return input.Name + marker + " content (" + kind + ")"
	}
	marker := ":"
	if input.Mode == OptionalInput {
		marker = "?:"
	}
	return kind + " " + input.Name + marker + " content (" + component.Name + ")"
}
