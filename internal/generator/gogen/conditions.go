package gogen

import (
	"fmt"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

// condition writes if/else with each branch generated under the paths its
// condition narrows.
func (g *generator) condition(node *ast.ConditionNode, fieldTypes map[string]input) error {
	expression, err := g.conditionExpression(node.Condition, fieldTypes)
	if err != nil {
		return err
	}
	whenTrue, whenFalse := compiler.ConditionNarrowing(node.Condition)
	g.emitf("\tif %s {\n", expression)
	if err := g.withNarrowed(whenTrue, func() error { return g.body(node.Then, fieldTypes) }); err != nil {
		return err
	}
	if len(node.Else) > 0 {
		g.emitln("\t} else {")
		if err := g.withNarrowed(whenFalse, func() error { return g.body(node.Else, fieldTypes) }); err != nil {
			return err
		}
	}
	g.emitln("\t}")
	return nil
}

// conditionExpression lowers a condition to a Go boolean expression. Later
// operands of && and || see the paths earlier operands narrow, as Go
// short-circuits them.
func (g *generator) conditionExpression(condition *ast.ConditionExpr, fieldTypes map[string]input) (string, error) {
	switch condition.Kind {
	case ast.PathCondition:
		expression, typ, err := g.pathExpression(condition.Path, fieldTypes)
		if err != nil {
			return "", fmt.Errorf("condition %s: %w", condition.Path.String(), err)
		}
		if !typ.IsScalar(compiler.BooleanType) {
			return "", fmt.Errorf("condition %s is %s, not boolean", condition.Path.String(), typ)
		}
		return expression, nil
	case ast.PresenceCondition:
		expression, _, err := g.pathValue(condition.Path, fieldTypes, true)
		if err != nil {
			return "", fmt.Errorf("condition %s?: %w", condition.Path.String(), err)
		}
		return expression + " != nil", nil
	case ast.NotCondition:
		operand, err := g.conditionExpression(condition.Operands[0], fieldTypes)
		if err != nil {
			return "", err
		}
		return "!(" + operand + ")", nil
	case ast.GroupCondition:
		operand, err := g.conditionExpression(condition.Operands[0], fieldTypes)
		if err != nil {
			return "", err
		}
		return "(" + operand + ")", nil
	case ast.AndCondition, ast.OrCondition:
		operator := " && "
		if condition.Kind == ast.OrCondition {
			operator = " || "
		}
		parts := make([]string, len(condition.Operands))
		for index, narrowed := range compiler.OperandNarrowing(condition) {
			var part string
			err := g.withNarrowed(narrowed, func() error {
				var err error
				part, err = g.conditionExpression(condition.Operands[index], fieldTypes)
				return err
			})
			if err != nil {
				return "", err
			}
			parts[index] = "(" + part + ")"
		}
		return strings.Join(parts, operator), nil
	}
	return "", fmt.Errorf("unsupported condition %q", condition.Kind)
}
