package gogen

import (
	"fmt"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

// outlet renders a content input of the component being generated. Optional
// content writes nothing when it is nil; inside its presence branch it needs
// no second check.
func (g *generator) outlet(element *ast.ElementNode, info input) error {
	if len(element.Attributes) > 0 || len(element.Children) > 0 {
		return fmt.Errorf("content outlet %s cannot have attributes or a body", element.Name)
	}
	call := "props.%s.Render(%s, w)"
	if g.options.API == APIDirect {
		call = "props.%s(%s, w)"
	}
	site, _ := g.snapshot.OutletSource(element)
	render := fmt.Sprintf("if err := "+g.markIn(element.P, element.P+len(element.Name))+call+"; err != nil { return err }", exported(info.name), g.contextExpression(site))
	if info.mode == ast.RequiredInput || g.isNarrowed(info.name) {
		g.emitf("\t%s\n", render)
		return nil
	}
	g.emitf("\tif props.%s != nil { %s }\n", exported(info.name), render)
	return nil
}

// callContent lowers a call's unlabelled children and slot fills to one
// ktml.ComponentFunc closure each, declared before the call, and returns their
// props assignments in the target's input order.
func (g *generator) callContent(call *ast.ElementNode, target component, callerFields map[string]input) ([]string, error) {
	var children []ast.Node
	fills := make(map[string]*ast.SlotNode)
	for _, child := range call.Children {
		slot, fill := child.(*ast.SlotNode)
		if !fill {
			children = append(children, child)
			continue
		}
		info, ok := target.inputNamed(slot.Name)
		if !ok || !info.isContent() || slot.Name == compiler.ChildrenInput {
			return nil, fmt.Errorf("component %s has no slot %s", call.Name, slot.Name)
		}
		if _, duplicate := fills[slot.Name]; duplicate {
			return nil, fmt.Errorf("component %s slot %s is filled more than once", call.Name, slot.Name)
		}
		fills[slot.Name] = slot
	}
	var assignments []string
	for _, field := range target.inputs {
		if !field.isContent() {
			continue
		}
		var body []ast.Node
		present := false
		if field.name == compiler.ChildrenInput {
			body, present = children, len(children) > 0
		} else if slot, ok := fills[field.name]; ok {
			body, present = slot.Body, true
		}
		if !present {
			if field.mode == ast.RequiredInput {
				return nil, fmt.Errorf("component %s requires content %s", call.Name, field.name)
			}
			continue
		}
		local, err := g.contentClosure(body, callerFields)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, exported(field.name)+": "+local)
	}
	if len(children) > 0 {
		if info, ok := target.inputNamed(compiler.ChildrenInput); !ok || !info.isContent() {
			return nil, fmt.Errorf("component %s does not render children", call.Name)
		}
	}
	return assignments, nil
}

// contentClosure writes one fill as a closure over the caller's scope and
// returns its local name.
func (g *generator) contentClosure(body []ast.Node, callerFields map[string]input) (string, error) {
	g.usesKTML = true
	g.contentSequence++
	local := fmt.Sprintf("ktmlContent%d", g.contentSequence)
	// Fills only receive the caller's *rt.Writer, so the wrap never owns.
	g.usesRuntime = true
	g.emitf("\t%s := ktml.ComponentFunc(func(ctx context.Context, ktmlW io.Writer) error {\n\t\tw, _ := rt.Wrap(ktmlW)\n", local)
	if err := g.body(body, callerFields); err != nil {
		return "", err
	}
	g.emitln("\t\treturn nil")
	g.emitln("\t})")
	return local, nil
}
