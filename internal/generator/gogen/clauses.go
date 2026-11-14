package gogen

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

// componentID returns the canonical "<package ID>.<Name>" used in render
// errors. The package ID is the Go import path when it is known.
func (g *generator) componentID(name string) string {
	if g.options.Packages != nil && g.dir != "" {
		if pkg, err := g.options.Packages(g.dir); err == nil && pkg.ImportPath != "" {
			return pkg.ImportPath + "." + exported(name)
		}
	}
	return g.options.Package + "." + exported(name)
}

// dispatch selects one clause or default body before the component writes.
// Nil or unknown states return rt.InvalidState.
func (g *generator) dispatch(model component, fieldTypes map[string]input) error {
	group := model.group
	selector, ok := fieldTypes[group.Selector.Name]
	if !ok {
		return fmt.Errorf("selector %s is not an input", group.Selector.Name)
	}
	g.usesRuntime = true
	invalid := fmt.Sprintf("return rt.InvalidState(%s, %s)", strconv.Quote(g.componentID(model.name)), strconv.Quote(group.Selector.Name))
	clauses := make(map[string]compiler.Clause, len(group.Clauses))
	for _, clause := range group.Clauses {
		clauses[clause.Case] = clause
	}
	residual := make(map[string]bool, len(group.Residual))
	for _, name := range group.Residual {
		residual[name] = true
	}
	switch group.Kind {
	case compiler.StateSelector:
		state, ok := g.snapshot.State(group.Selector.Type.State)
		if !ok {
			return fmt.Errorf("unknown state %s", group.Selector.Type.Name)
		}
		qualifier, err := g.qualifier(filepath.Dir(state.File))
		if err != nil {
			return err
		}
		expression := "props." + exported(group.Selector.Name)
		g.emitf("\tif %s == nil { %s }\n", expression, invalid)
		g.emitf("\tswitch %s%s.Variant() {\n", g.mark(group.Selector.NameRange), expression)
		var residualCases []string
		for _, variant := range state.Variants {
			constant := qualifier + state.Name + "Variant" + variant.Name
			clause, specialized := clauses[variant.Name]
			if !specialized {
				if residual[variant.Name] {
					residualCases = append(residualCases, constant)
				}
				continue
			}
			g.emitf("\tcase %s%s:\n", g.mark(clause.PatternRange), constant)
			locals := fieldTypes
			if len(clause.Bindings) > 0 {
				g.emitf("\t\tktmlPayload, ok := %s%sAs%s%s(%s)\n\t\tif !ok { %s }\n", g.mark(clause.PatternRange), qualifier, state.Name, variant.Name, expression, invalid)
				locals = g.bind(fieldTypes, clause.Bindings, func(binding compiler.Binding) string {
					return "ktmlPayload." + compiler.GoFieldName(binding.Field)
				})
			}
			if err := g.componentBody(clause.Body, locals); err != nil {
				return err
			}
		}
		if len(residualCases) > 0 {
			g.emitf("\tcase %s%s:\n", g.mark(group.DefaultRange), strings.Join(residualCases, ", "))
			if err := g.componentBody(group.DefaultBody, fieldTypes); err != nil {
				return err
			}
		}
		g.emitf("\tdefault:\n\t\t%s\n\t}\n", invalid)
	case compiler.BooleanSelector:
		expression, err := inputExpression(selector.name, selector)
		if err != nil {
			return err
		}
		g.emitf("\tif %s {\n", expression)
		if err := g.caseBody(group, clauses, compiler.TrueCase, fieldTypes, nil); err != nil {
			return err
		}
		g.emitln("\t} else {")
		if err := g.caseBody(group, clauses, compiler.FalseCase, fieldTypes, nil); err != nil {
			return err
		}
		g.emitln("\t}")
	case compiler.OptionalSelector:
		expression := "props." + exported(group.Selector.Name)
		g.emitf("\tif %s != nil {\n", expression)
		if err := g.caseBody(group, clauses, compiler.SomeCase, fieldTypes, func(compiler.Binding) string { return "*" + expression }); err != nil {
			return err
		}
		g.emitln("\t} else {")
		if err := g.caseBody(group, clauses, compiler.NoneCase, fieldTypes, nil); err != nil {
			return err
		}
		g.emitln("\t}")
	default:
		return fmt.Errorf("unsupported selector kind %q", group.Kind)
	}
	return nil
}

// caseBody writes the specialized clause for a built-in case, or the default
// body when only the default handles it.
func (g *generator) caseBody(group *compiler.ClauseGroup, clauses map[string]compiler.Clause, name string, fieldTypes map[string]input, value func(compiler.Binding) string) error {
	clause, specialized := clauses[name]
	if !specialized {
		return g.componentBody(group.DefaultBody, fieldTypes)
	}
	locals := fieldTypes
	if len(clause.Bindings) > 0 && value != nil {
		locals = g.bind(fieldTypes, clause.Bindings, value)
	}
	return g.componentBody(clause.Body, locals)
}

// bind declares one immutable Go local per binding and returns the path roots
// visible in the clause body.
func (g *generator) bind(fieldTypes map[string]input, bindings []compiler.Binding, value func(compiler.Binding) string) map[string]input {
	locals := make(map[string]input, len(fieldTypes)+len(bindings))
	for name, info := range fieldTypes {
		locals[name] = info
	}
	for _, binding := range bindings {
		g.bindSequence++
		local := fmt.Sprintf("ktmlBind%d", g.bindSequence)
		g.emitf("\t\t%s := %s\n\t\t_ = %s\n", local, value(binding), local)
		mode := ast.RequiredInput
		if binding.Optional {
			mode = ast.OptionalInput
		}
		locals[binding.Name] = input{name: binding.Name, typ: binding.Type, mode: mode, expr: local}
	}
	return locals
}

func (g *generator) body(nodes []ast.Node, fieldTypes map[string]input) error {
	for _, node := range nodes {
		if err := g.node(node, fieldTypes); err != nil {
			return err
		}
	}
	return nil
}

// componentBody writes one default or clause body of the component being
// generated. The ktmldev variant marks the instance (ADR-0004): a body that is
// exactly one element carries the identity as its data-ktml-i attribute, a
// document body (one that starts with doctype, or only calls a document
// component) carries none, and any other body is surrounded by ktml:c
// boundaries.
func (g *generator) componentBody(nodes []ast.Node, fieldTypes map[string]input) error {
	if !g.development() {
		return g.body(nodes, fieldTypes)
	}
	switch g.snapshot.InstanceShape(g.file, nodes, g.isMarkupElement(fieldTypes)) {
	case compiler.DocumentInstance:
		return g.body(nodes, fieldTypes)
	case compiler.SingleRootInstance:
		g.instanceRoot = nodes[0].(*ast.ElementNode)
		err := g.body(nodes, fieldTypes)
		g.instanceRoot = nil
		return err
	}
	g.emitf("\tif err := dev.BeginInstance(w, ktmlInstance); err != nil { return rt.WriteError(%s, err) }\n", g.currentID)
	if err := g.body(nodes, fieldTypes); err != nil {
		return err
	}
	g.emitf("\tif err := dev.EndInstance(w, ktmlInstance); err != nil { return rt.WriteError(%s, err) }\n", g.currentID)
	return nil
}

// isMarkupElement reports whether an element node writes an HTML element
// rather than calling a component or rendering a content outlet.
func (g *generator) isMarkupElement(fieldTypes map[string]input) func(*ast.ElementNode) bool {
	return func(element *ast.ElementNode) bool {
		if info, outlet := fieldTypes[element.Name]; outlet && info.isContent() {
			return false
		}
		_, isComponent := g.components[element.Name]
		return !isComponent
	}
}
