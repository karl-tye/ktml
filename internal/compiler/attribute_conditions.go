package compiler

import (
	"fmt"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
)

// resolveAttributes types the attribute entries of an element: values in
// their output contexts, and the conditions of conditional groups, whose
// branches see the paths they narrow.
func (s *Snapshot) resolveAttributes(path string, caller pathScope, entries []*ast.AttributeNode) {
	for _, entry := range entries {
		switch {
		case entry.Condition != nil:
			condition := entry.Condition
			s.resolveCondition(path, caller, condition.Condition)
			whenTrue, whenFalse := ConditionNarrowing(condition.Condition)
			s.resolveAttributes(path, caller.withNarrowed(whenTrue), condition.Then)
			s.resolveAttributes(path, caller.withNarrowed(whenFalse), condition.Else)
		case entry.Spread != nil:
			s.resolveSpread(path, caller, entry)
		case entry.BagRange != nil:
			s.addDiagnostic(path, "attribute-bag-placement", fmt.Sprintf("an attribute bag ( … ) is only the value of a component input of type Attributes, not of the element attribute %s", entry.Name), entry.BagRange.Start, entry.BagRange.End)
		default:
			if HTMLAttribute(entry.Name) && entry.Quoted != nil {
				for _, segment := range entry.Quoted.Segments {
					if segment.Kind == ast.PathSegment && segment.Path != nil {
						s.addDiagnostic(path, "html-attribute-value", fmt.Sprintf("%s is an HTML document the browser runs in the page's origin, so a path cannot fill it; write it as literal text", entry.Name), segment.Path.P, segment.Path.E)
					}
				}
				continue
			}
			context := attributeContext
			if ScriptAttribute(entry.Name) {
				context = scriptAttributeContext
				s.scriptPositions = scriptPositions(entry.Quoted)
			} else if StyleAttribute(entry.Name) {
				context = styleAttributeContext
			} else if URLAttribute(entry.Name) {
				context = urlAttributeContext
			}
			s.resolveQuotedValue(path, caller, entry.Quoted, context, entry.Name)
			s.scriptPositions = nil
		}
	}
}

// attributeOccurrence is one named attribute and the conditional branches
// that must all be taken for it to be written.
type attributeOccurrence struct {
	attribute *ast.AttributeNode
	branches  []attributeBranch
}

type attributeBranch struct {
	group  *ast.AttributeCondition
	branch int
}

// exclusive reports whether two occurrences can never both be written: they
// lie in different branches of one conditional group.
func (a attributeOccurrence) exclusive(b attributeOccurrence) bool {
	for _, left := range a.branches {
		for _, right := range b.branches {
			if left.group == right.group && left.branch != right.branch {
				return true
			}
		}
	}
	return false
}

// validateAttributeCollisions reports a conditional attribute whose name can
// be written twice: once more unconditionally, in another conditional group,
// or twice in one branch. Names compare case-insensitively, as in HTML. The
// same name may appear in exclusive branches of one group. Duplicates among
// unconditional attributes keep their earlier behaviour.
func (s *Snapshot) validateAttributeCollisions(path string, entries []*ast.AttributeNode) {
	s.validateAttributeCollisionsMode(path, entries, false)
}

// validateAttributeCollisionsMode reports duplicates; with strict set, two
// unconditional attributes of one name are reported too.
func (s *Snapshot) validateAttributeCollisionsMode(path string, entries []*ast.AttributeNode, strict bool) {
	var occurrences []attributeOccurrence
	var walk func([]*ast.AttributeNode, []attributeBranch)
	walk = func(entries []*ast.AttributeNode, branches []attributeBranch) {
		for _, entry := range entries {
			switch {
			case entry.Condition != nil:
				group := entry.Condition
				walk(group.Then, append(append([]attributeBranch(nil), branches...), attributeBranch{group, 0}))
				walk(group.Else, append(append([]attributeBranch(nil), branches...), attributeBranch{group, 1}))
			case entry.Spread == nil:
				occurrences = append(occurrences, attributeOccurrence{attribute: entry, branches: branches})
			}
		}
	}
	walk(entries, nil)
	for later := range occurrences {
		for earlier := range later {
			first, second := occurrences[earlier], occurrences[later]
			if !strict && len(first.branches) == 0 && len(second.branches) == 0 {
				continue
			}
			if !strings.EqualFold(first.attribute.Name, second.attribute.Name) || first.exclusive(second) {
				continue
			}
			message := "attribute %s can be written twice; a conditional attribute may share its name only with the other branches of its own if"
			if strict {
				message = "attribute %s can be written twice; an attribute bag names each attribute once, except in exclusive branches of one if"
			}
			s.addDiagnostic(path, "duplicate-attribute", fmt.Sprintf(message, second.attribute.Name), second.attribute.P, second.attribute.E)
			break
		}
	}
}

// reportConditionalInputs reports conditional groups in a component call,
// whose attributes are inputs.
func (s *Snapshot) reportConditionalInputs(path string, target string, call *ast.ElementNode) {
	for _, entry := range call.Attributes {
		if entry.Spread != nil {
			s.reportCallSpreads(path, target, entry)
		}
		if entry.Condition != nil {
			s.addDiagnostic(path, "conditional-call-input", fmt.Sprintf("component %s inputs cannot be conditional; pass a value Go derives, or branch around the call", target), entry.P, entry.E)
		}
	}
}
