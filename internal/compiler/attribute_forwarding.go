package compiler

import (
	"fmt"

	"github.com/karl-tye/ktml/internal/ast"
)

// resolveSpread types ...path: a present Attributes value.
func (s *Snapshot) resolveSpread(path string, caller pathScope, entry *ast.AttributeNode) {
	expression := entry.Spread.Path
	resolved, ok := s.resolvePath(path, caller, expression)
	if !ok {
		return
	}
	last := resolved.Parts[len(resolved.Parts)-1]
	switch {
	case resolved.Optional:
		s.addDiagnostic(path, "optional-value-access", fmt.Sprintf("optional %s %s must be narrowed before it is spread; write if %s? { ...%s }", last.kind(), last.Name, expression.String(), expression.String()), expression.P, expression.E)
	case resolved.Type.Kind != ContextualTypeKind || resolved.Type.Name != AttributesType:
		s.addDiagnostic(path, "spread-type-mismatch", fmt.Sprintf("only an Attributes value can be spread; %s is %s", expression.String(), resolved.Type), expression.P, expression.E)
	}
}

// validateSpreads allows at most one spread to be written per element, so
// forwarded names never collide with each other. Spreads in exclusive
// branches of one conditional group are allowed.
func (s *Snapshot) validateSpreads(path string, entries []*ast.AttributeNode) {
	var spreads []attributeOccurrence
	var walk func([]*ast.AttributeNode, []attributeBranch)
	walk = func(entries []*ast.AttributeNode, branches []attributeBranch) {
		for _, entry := range entries {
			switch {
			case entry.Condition != nil:
				group := entry.Condition
				walk(group.Then, append(append([]attributeBranch(nil), branches...), attributeBranch{group, 0}))
				walk(group.Else, append(append([]attributeBranch(nil), branches...), attributeBranch{group, 1}))
			case entry.Spread != nil:
				current := attributeOccurrence{attribute: entry, branches: branches}
				for _, earlier := range spreads {
					if !earlier.exclusive(current) {
						s.addDiagnostic(path, "multiple-spreads", "an element forwards at most one Attributes value; join several in Go with ktml.JoinAttributes", entry.P, entry.E)
						break
					}
				}
				spreads = append(spreads, current)
			}
		}
	}
	walk(entries, nil)
}

// reportCallSpreads reports spreads among a component call's inputs.
func (s *Snapshot) reportCallSpreads(path, target string, call *ast.AttributeNode) {
	s.addDiagnostic(path, "spread-call-input", fmt.Sprintf("component %s takes inputs, not spread attributes; pass the bag to an Attributes input, as attrs=\"{%s}\"", target, call.Spread.Path.String()), call.P, call.E)
}

// resolveAttributeBag types an attribute bag literal supplied to an
// Attributes input. Its entries are checked in the caller's scope, each for
// the context its name requires, and every name may be written once.
func (s *Snapshot) resolveAttributeBag(path string, caller pathScope, bag *ast.AttributeNode) {
	s.resolveAttributes(path, caller, bag.Bag)
	s.validateSpreads(path, bag.Bag)
	s.validateBagCollisions(path, bag.Bag)
	for _, entry := range ast.NamedAttributes(bag.Bag) {
		if entry.BagRange != nil || entry.Quoted == nil {
			continue
		}
		if _, exact := exactQuotedPath(entry.Quoted); exact || !hasPathSegment(entry.Quoted) {
			continue
		}
		if ScriptAttribute(entry.Name) || StyleAttribute(entry.Name) {
			s.addDiagnostic(path, "attribute-bag-value", fmt.Sprintf("%s in an attribute bag takes a literal or one whole value; build mixed script or CSS in Go", entry.Name), entry.P, entry.E)
		}
	}
}

func hasPathSegment(quoted *ast.QuotedValue) bool {
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.PathSegment {
			return true
		}
	}
	return false
}

// validateBagCollisions reports a name that a bag literal can contain twice,
// unconditional duplicates included: unlike element attributes, a bag has
// no earlier duplicate behaviour to keep.
func (s *Snapshot) validateBagCollisions(path string, entries []*ast.AttributeNode) {
	s.validateAttributeCollisionsMode(path, entries, true)
}
