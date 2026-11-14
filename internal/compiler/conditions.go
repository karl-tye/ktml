package compiler

import (
	"fmt"

	"github.com/karl-tye/ktml/internal/ast"
)

// ConditionNarrowing returns the path spellings a condition proves present
// when it is true and when it is false. The rules are syntactic:
//
//	p?       true: {p}
//	!c       swaps the sets of c
//	(c)      the sets of c
//	a && b   true: T(a) ∪ T(b)   false: F(a) ∩ F(b)
//	a || b   true: T(a) ∩ T(b)   false: F(a) ∪ F(b)
func ConditionNarrowing(condition *ast.ConditionExpr) (whenTrue, whenFalse []string) {
	if condition == nil {
		return nil, nil
	}
	switch condition.Kind {
	case ast.PresenceCondition:
		return []string{condition.Path.String()}, nil
	case ast.NotCondition:
		t, f := ConditionNarrowing(condition.Operands[0])
		return f, t
	case ast.GroupCondition:
		return ConditionNarrowing(condition.Operands[0])
	case ast.AndCondition, ast.OrCondition:
		var union, intersection []string
		for index, operand := range condition.Operands {
			t, f := ConditionNarrowing(operand)
			joined, met := t, f
			if condition.Kind == ast.OrCondition {
				joined, met = f, t
			}
			union = unionPaths(union, joined)
			if index == 0 {
				intersection = met
			} else {
				intersection = intersectPaths(intersection, met)
			}
		}
		if condition.Kind == ast.OrCondition {
			return intersection, union
		}
		return union, intersection
	}
	return nil, nil
}

// OperandNarrowing returns the paths narrowed for each operand of an and or
// or condition by the operands before it, as Go short-circuits them.
func OperandNarrowing(condition *ast.ConditionExpr) [][]string {
	narrowed := make([][]string, len(condition.Operands))
	var accumulated []string
	for index, operand := range condition.Operands {
		narrowed[index] = append([]string(nil), accumulated...)
		t, f := ConditionNarrowing(operand)
		if condition.Kind == ast.OrCondition {
			t = f
		}
		accumulated = unionPaths(accumulated, t)
	}
	return narrowed
}

func unionPaths(a, b []string) []string {
	result := append([]string(nil), a...)
	for _, path := range b {
		if !containsPath(result, path) {
			result = append(result, path)
		}
	}
	return result
}

func intersectPaths(a, b []string) []string {
	var result []string
	for _, path := range a {
		if containsPath(b, path) {
			result = append(result, path)
		}
	}
	return result
}

func containsPath(paths []string, path string) bool {
	for _, candidate := range paths {
		if candidate == path {
			return true
		}
	}
	return false
}

// resolveConditionNode types a condition and resolves each branch with the
// paths it narrows.
func (s *Snapshot) resolveConditionNode(path string, source []byte, caller pathScope, node *ast.ConditionNode) {
	s.resolveCondition(path, caller, node.Condition)
	whenTrue, whenFalse := ConditionNarrowing(node.Condition)
	then := caller.withNarrowed(whenTrue)
	for _, child := range node.Then {
		s.resolveNode(path, source, then, child)
	}
	otherwise := caller.withNarrowed(whenFalse)
	for _, child := range node.Else {
		s.resolveNode(path, source, otherwise, child)
	}
}

// resolveCondition types every operand. A path operand must be a present
// boolean; a presence operand must be optional.
func (s *Snapshot) resolveCondition(path string, caller pathScope, condition *ast.ConditionExpr) {
	if condition == nil {
		return
	}
	switch condition.Kind {
	case ast.PathCondition:
		resolved, ok := s.resolvePath(path, caller, condition.Path)
		if !ok {
			return
		}
		spelling := condition.Path.String()
		switch {
		case resolved.Type.Kind == ContentTypeKind:
			s.addDiagnostic(path, "non-boolean-condition", fmt.Sprintf("%s is content; a condition requires boolean; test content with %s?", spelling, spelling), condition.P, condition.E)
		case resolved.Optional && resolved.Type.IsScalar(BooleanType):
			last := resolved.Parts[len(resolved.Parts)-1]
			s.addDiagnostic(path, "optional-value-access", fmt.Sprintf("optional %s %s must be narrowed before value access; test it with %s? && %s", last.kind(), last.Name, spelling, spelling), condition.P, condition.E)
		case resolved.Optional:
			s.addDiagnostic(path, "non-boolean-condition", fmt.Sprintf("%s is optional %s; a condition requires boolean; test presence with %s?", spelling, resolved.Type, spelling), condition.P, condition.E)
		case !resolved.Type.IsScalar(BooleanType):
			s.addDiagnostic(path, "non-boolean-condition", fmt.Sprintf("%s is %s; a condition requires boolean; derive a boolean in Go", spelling, resolved.Type), condition.P, condition.E)
		}
	case ast.PresenceCondition:
		resolved, ok := s.resolvePath(path, caller, condition.Path)
		if !ok {
			return
		}
		last := resolved.Parts[len(resolved.Parts)-1]
		if !last.Optional && !last.Narrowed {
			s.addDiagnostic(path, "presence-requires-optional", fmt.Sprintf("%s is not optional; ? tests only optional values", condition.Path.String()), condition.P, condition.E)
		}
	case ast.NotCondition, ast.GroupCondition:
		s.resolveCondition(path, caller, condition.Operands[0])
	case ast.AndCondition, ast.OrCondition:
		for index, narrowed := range OperandNarrowing(condition) {
			s.resolveCondition(path, caller.withNarrowed(narrowed), condition.Operands[index])
		}
	}
}
