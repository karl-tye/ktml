package templmigrate

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	parser "github.com/a-h/templ/parser/v2"
)

// clauseGroup is a component the converter adds for a switch over a closed
// set: a bodyless signature and one Render Clause per Variant. It is
// printed after the template that holds the switch.
type clauseGroup struct {
	name     string
	comment  string
	selector string
	inputs   []string
	clauses  []clauseBody
}

type clauseBody struct {
	variant string
	body    []kNode
}

// switchExpr converts a templ switch. A switch whose tag has a named type
// and whose cases are constants of that type is a closed set: it becomes
// a clause group over a Presentation State the presenter selects, one
// Variant per case and Other for the default (or for values no case
// matches, which render nothing). Any other switch becomes an if/else-if
// chain over presenter booleans, one per case, which renders the same,
// and is reported for review as a state candidate.
func (te *tmplEmitter) switchExpr(n *parser.SwitchExpression, next parser.Node) []kNode {
	src := te.c.node(te.fe.file, "SwitchExpression", n.Range)
	if !te.view() {
		te.partial(src, n.Range, "switch", "switch in a direct template")
		return nil
	}
	start, end, ok := te.fe.rw.span(n.Expression)
	if !ok {
		te.partial(src, n.Range, "switch", "switch not found in the generated Go")
		return nil
	}
	tag, problems := te.fe.rw.rewrite(start, end)
	for _, problem := range problems {
		te.partial(src, n.Range, "presenter-reference", problem)
	}
	unresolved := len(problems) > 0
	if !unresolved {
		if variants := te.closedSet(n); variants != nil {
			te.c.summary.Switches.Clauses++
			return te.clauseSwitch(n, next, src, tag, variants)
		}
	}
	te.c.summary.Switches.Booleans++
	te.review(src, n.Range, "closed-presentation-state", "the switch converts to presenter booleans; model its cases as a Presentation State with Render Clauses")
	// switch tag { case …: v.CaseN = true; … }
	result := &kIf{src: src}
	caseFields := make([]string, len(n.Cases))
	if unresolved {
		// The switch cannot run in the presenter package: no case is taken.
		te.open("switch { // TODO(ktml-migrate): switch " + tag)
	} else {
		te.open("switch " + tag + " {")
	}
	var bodies [][]kNode
	for i, c := range n.Cases {
		caseSrc := te.c.node(te.fe.file, "CaseExpression", c.Expression.Range)
		label := strings.TrimSpace(c.Expression.Value)
		rewritten, problems := te.fe.rw.expr(c.Expression)
		for _, problem := range problems {
			te.partial(caseSrc, c.Expression.Range, "presenter-reference", problem)
		}
		te.indent--
		switch {
		case unresolved && strings.HasPrefix(label, "default"):
			te.line("default:")
		case unresolved || len(problems) > 0:
			te.line("case false: // TODO(ktml-migrate): " + label)
		default:
			te.line(strings.TrimSpace(rewritten))
		}
		te.indent++
		hint := []string{"is"}
		if value, ok := strings.CutPrefix(label, "case "); ok {
			hint = append(hint, splitWord(strings.TrimSuffix(value, ":"))...)
		} else {
			hint = append(hint, "default")
		}
		f := &field{name: uniqueName(te.scope.rec.used, lowerCamel(hint)), ktmlType: "boolean", kind: fieldDerived, src: src0(caseSrc)}
		te.scope.rec.fields = append(te.scope.rec.fields, f)
		caseFields[i] = te.scope.root + "." + f.name
		te.blocks = append(te.blocks, map[string]bool{})
		te.line(te.scope.goVar + "." + f.goName() + " = true")
		bodies = append(bodies, te.children(c.Children, next, stripEnds))
		te.blocks = te.blocks[:len(te.blocks)-1]
	}
	te.close("}")
	for i, c := range n.Cases {
		if strings.HasPrefix(strings.TrimSpace(c.Expression.Value), "default") {
			result.hasElse = true
			result.elseBody = bodies[i]
			continue
		}
		result.branches = append(result.branches, kBranch{cond: caseFields[i], body: bodies[i]})
	}
	if len(result.branches) == 0 {
		return result.elseBody
	}
	return []kNode{result}
}

// closedSet returns the Variant name of each case of a switch over a
// closed set, or nil. The set is closed when the tag has a named type and
// every case value is a constant of that type; the default case is the
// Variant "". A case body that renders a content input or spreads an
// attribute bag keeps the switch inline (a clause group would need the
// input forwarded), as does a switch with an init statement or a
// fallthrough.
func (te *tmplEmitter) closedSet(n *parser.SwitchExpression) []string {
	stmt, ok := stmtContaining[*ast.SwitchStmt](te.fe.t, n.Expression)
	if !ok || stmt.Init != nil || stmt.Tag == nil || len(stmt.Body.List) != len(n.Cases) {
		return nil
	}
	named, ok := types.Unalias(te.typeOf(stmt.Tag)).(*types.Named)
	if !ok {
		return nil
	}
	if _, ok := named.Underlying().(*types.Basic); !ok {
		return nil
	}
	info := te.fe.t.info
	variants := make([]string, len(n.Cases))
	for i, s := range stmt.Body.List {
		clause, ok := s.(*ast.CaseClause)
		if !ok {
			return nil
		}
		if clause.List == nil {
			if !strings.HasPrefix(strings.TrimSpace(n.Cases[i].Expression.Value), "default") {
				return nil
			}
			continue
		}
		var words []string
		for _, value := range clause.List {
			var ident *ast.Ident
			switch v := unparen(value).(type) {
			case *ast.Ident:
				ident = v
			case *ast.SelectorExpr:
				ident = v.Sel
			}
			if ident == nil {
				return nil
			}
			c, ok := info.Uses[ident].(*types.Const)
			if !ok || !types.Identical(c.Type(), named) {
				return nil
			}
			name := c.Name()
			if rest, ok := strings.CutPrefix(name, named.Obj().Name()); ok && rest != "" && rest[0] >= 'A' && rest[0] <= 'Z' {
				name = rest
			}
			for _, part := range splitWord(name) {
				words = append(words, upperFirst(part))
			}
		}
		variants[i] = strings.Join(words, "")
		if variants[i] == "" {
			variants[i] = fmt.Sprintf("Case%d", i+1)
		}
	}
	usesInput := false
	ast.Inspect(stmt.Body, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok {
			if scope, ok := te.locals[info.Uses[ident]]; ok && scope == nil {
				usesInput = true
			}
		}
		return !usesInput
	})
	if usesInput || casesHave(n.Cases, func(node parser.Node) bool {
		switch node.(type) {
		case *parser.ChildrenExpression, *parser.Fallthrough:
			return true
		}
		return false
	}) {
		return nil
	}
	return variants
}

// casesHave reports whether a node of the case bodies matches.
func casesHave(cases []parser.CaseExpression, match func(parser.Node) bool) bool {
	var visit func([]parser.Node) bool
	visit = func(nodes []parser.Node) bool {
		for _, node := range nodes {
			if match(node) {
				return true
			}
			if n, ok := node.(parser.CompositeNode); ok && visit(n.ChildNodes()) {
				return true
			}
		}
		return false
	}
	for _, c := range cases {
		if visit(c.Children) {
			return true
		}
	}
	return false
}

// clauseSwitch converts a switch over a closed set into a call of a new
// clause group. The case bodies keep their record paths, so the group
// takes the records in scope (the view, and the items of enclosing loops)
// besides the state.
func (te *tmplEmitter) clauseSwitch(n *parser.SwitchExpression, next parser.Node, src *Node, tag string, variants []string) []kNode {
	stmt, _ := stmtContaining[*ast.SwitchStmt](te.fe.t, n.Expression)
	hint := hintOf(stmt.Tag)
	word := upperFirst(lowerCamel(hint[len(hint)-1:]))
	name := te.fe.recordName(upperFirst(te.sig.component) + word)
	te.fe.recUsed[name+"Props"] = true
	stateName := te.fe.recordName(name + "State")
	te.fe.recUsed[stateName+"Variant"] = true

	used := map[string]bool{}
	hasDefault := false
	for i, v := range variants {
		if v == "" {
			hasDefault = true
			continue
		}
		variants[i] = uniqueName(used, v)
	}
	other := uniqueName(used, "Other")
	state := newRecord(stateName, fmt.Sprintf("The cases of %s's switch at %s:%d.", te.sig.name, te.fe.file.rel, int(n.Range.From.Line)+1))
	for _, v := range variants {
		if v != "" {
			state.variants = append(state.variants, v)
		}
	}
	state.variants = append(state.variants, other)
	te.fe.records = append(te.fe.records, state)

	// The selector is reserved while the bodies are emitted, so that no
	// loop item in them takes its name. state is a keyword only at the
	// start of a top-level declaration, so it names an input.
	selector := "state"
	for i := 2; te.ktml[selector]; i++ {
		selector = fmt.Sprintf("state%d", i)
	}
	te.ktml[selector] = true
	defer delete(te.ktml, selector)
	f := &field{name: uniqueName(te.scope.rec.used, lowerCamel(append(hint[len(hint)-1:], "state"))), ktmlType: stateName, kind: fieldWiring, src: src0(src)}
	te.scope.rec.fields = append(te.scope.rec.fields, f)
	assign := func(variant string) {
		te.line(te.scope.goVar + "." + f.goName() + " = New" + stateName + variant + "()")
	}

	group := &clauseGroup{name: name, selector: selector, comment: state.comment}
	group.inputs = append(group.inputs, selector+": "+stateName)
	call := &kCall{name: name, src: src, attrs: []kAttr{{kind: attrValue, name: selector, value: `"{` + te.scope.root + "." + f.name + `}"`}}}
	var chain []*recScope
	for s := te.scope; s != nil; s = s.parent {
		chain = append([]*recScope{s}, chain...)
	}
	for _, s := range chain {
		group.inputs = append(group.inputs, s.root+": "+s.rec.name)
		call.attrs = append(call.attrs, kAttr{kind: attrValue, name: s.root, value: `"{` + s.root + `}"`})
	}

	te.open("switch " + tag + " {")
	for i, c := range n.Cases {
		caseSrc := te.c.node(te.fe.file, "CaseExpression", c.Expression.Range)
		rewritten, problems := te.fe.rw.expr(c.Expression)
		for _, problem := range problems {
			te.partial(caseSrc, c.Expression.Range, "presenter-reference", problem)
		}
		te.indent--
		te.line(strings.TrimSpace(rewritten))
		te.indent++
		variant := variants[i]
		if variant == "" {
			variant = other
		}
		te.blocks = append(te.blocks, map[string]bool{})
		assign(variant)
		group.clauses = append(group.clauses, clauseBody{variant: variant, body: te.children(c.Children, next, stripEnds)})
		te.blocks = te.blocks[:len(te.blocks)-1]
	}
	if !hasDefault {
		// templ renders nothing for a value no case matches.
		te.indent--
		te.line("default:")
		te.indent++
		assign(other)
		group.clauses = append(group.clauses, clauseBody{variant: other})
	}
	te.close("}")
	te.groups = append(te.groups, group)
	return []kNode{call}
}

// printGroups writes the clause groups the template's switches added.
func (te *tmplEmitter) printGroups(p *ktmlPrinter) {
	for _, g := range te.groups {
		p.writeLine(0, "// "+g.comment)
		p.writeLine(0, g.name+" ["+strings.Join(g.inputs, ", ")+"]")
		p.writeLine(0, "")
		for _, c := range g.clauses {
			p.writeLine(0, g.name+" ["+g.selector+" = "+c.variant+"] {")
			p.nodes(1, c.body)
			p.writeLine(0, "}")
			p.writeLine(0, "")
		}
	}
}
