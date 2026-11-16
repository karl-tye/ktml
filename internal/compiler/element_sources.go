package compiler

import (
	"slices"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/textposition"
)

// ElementStep is one element of a page's element path, as the component
// picker sees it in the browser: its tag, id, and classes.
type ElementStep struct {
	Tag     string   `json:"tag"`
	ID      string   `json:"id,omitempty"`
	Classes []string `json:"classes,omitempty"`
}

// ContentLink is the component call that rendered an instance: the calling
// component and the call's c<n> site. Content an instance renders at an
// outlet is the call's children or slot fill.
type ContentLink struct {
	Caller SymbolID
	Site   string
}

// ElementSource is the declaration of one element of an element path. Line
// and Column are 1-based; Column counts UTF-16 code units, as editors do. Component is the
// component whose source declares the element.
type ElementSource struct {
	Found     bool
	Range     SourceRange
	Line      int
	Column    int
	Component SymbolID
}

// LocateElement finds the declaration of every element of path, from the top
// of an instance of component down. It matches markup elements by tag, with
// each static id and static class token required on the page element;
// condition branches and repetition bodies are transparent, calls are opaque
// (their output is another instance), and an outlet continues in the content
// its instance's call supplied: links[0] for the instance, links[1] for the
// caller's own instance, and so on. The highest scoring match wins, an id
// counting more than each class, and source order breaks ties. An implicit
// tbody, which browsers insert, is skipped. Each step has the source of its
// match, or none.
func (s *Snapshot) LocateElement(component SymbolID, links []ContentLink, path []ElementStep) []ElementSource {
	sources := make([]ElementSource, len(path))
	if s == nil || len(path) == 0 {
		return sources
	}
	bodies, ok := s.componentBodies(component)
	if !ok {
		return sources
	}
	locator := &elementLocator{snapshot: s, links: links}
	var best *elementMatch
	for _, body := range bodies {
		match := locator.match(sourceNodes{nodes: body, component: component}, path)
		if match != nil && (best == nil || match.score > best.score) {
			best = match
		}
	}
	for index := 0; best != nil && index < len(path); index++ {
		element := best.elements[index]
		if element.node == nil {
			continue
		}
		sources[index] = s.elementSource(element)
	}
	return sources
}

// componentBodies returns a component's default and clause bodies.
func (s *Snapshot) componentBodies(id SymbolID) ([][]ast.Node, bool) {
	component, ok := s.components[id]
	if !ok {
		return nil, false
	}
	if group, grouped := s.groups[id]; grouped {
		var bodies [][]ast.Node
		for _, clause := range group.Clauses {
			bodies = append(bodies, clause.Body)
		}
		if group.HasDefault {
			bodies = append(bodies, group.DefaultBody)
		}
		return bodies, true
	}
	file := s.files[component.File]
	if file.doc == nil {
		return nil, false
	}
	for _, node := range file.doc.Nodes {
		if declaration, ok := node.(*ast.ElementDecl); ok && declaration.Name == component.Name && declaration.Pos() == component.Range.Start {
			return [][]ast.Node{declaration.Body}, true
		}
	}
	return nil, false
}

// callNode finds the call of a component at a c<n> site.
func (s *Snapshot) callNode(caller SymbolID, site string) (*ast.ElementNode, bool) {
	bodies, ok := s.componentBodies(caller)
	if !ok {
		return nil, false
	}
	var found *ast.ElementNode
	var walk func([]ast.Node)
	walk = func(nodes []ast.Node) {
		for _, node := range nodes {
			if found != nil {
				return
			}
			switch value := node.(type) {
			case *ast.ElementNode:
				if source, ok := s.callSources[value]; ok && source == site {
					found = value
					return
				}
				walk(value.Children)
			case *ast.SlotNode:
				walk(value.Body)
			case *ast.ConditionNode:
				walk(value.Then)
				walk(value.Else)
			case *ast.RepetitionNode:
				walk(value.Body)
			}
		}
	}
	for _, body := range bodies {
		walk(body)
	}
	return found, found != nil
}

func (s *Snapshot) elementSource(element locatedElement) ElementSource {
	component := s.components[element.component]
	start := element.node.Pos()
	line, character := textposition.Position(s.files[component.File].source, start)
	return ElementSource{
		Found:     true,
		Range:     SourceRange{File: component.File, Start: start, End: element.node.End()},
		Line:      line + 1,
		Column:    character + 1,
		Component: element.component,
	}
}

// sourceNodes are body nodes of one component's source; link indexes the
// ContentLink that supplies that component instance's outlets.
type sourceNodes struct {
	nodes     []ast.Node
	component SymbolID
	link      int
}

type locatedElement struct {
	node      *ast.ElementNode
	component SymbolID
	link      int
}

// elementMatch is one way an element path matches: an element per step (nil
// for a skipped step) and its score.
type elementMatch struct {
	elements []locatedElement
	score    int
}

type elementLocator struct {
	snapshot *Snapshot
	links    []ContentLink
	// following bounds outlet hops, against content cycles.
	following int
}

// candidates lists the markup elements a body writes at its top level,
// looking through conditions, repetitions, and outlets.
func (l *elementLocator) candidates(body sourceNodes, into []locatedElement) []locatedElement {
	for _, node := range body.nodes {
		switch value := node.(type) {
		case *ast.ElementNode:
			if _, outlet := l.snapshot.outletSources[value]; outlet {
				into = l.outlet(body, value.Name, into)
				continue
			}
			if _, call := l.snapshot.callSources[value]; call {
				continue
			}
			into = append(into, locatedElement{node: value, component: body.component, link: body.link})
		case *ast.ConditionNode:
			into = l.candidates(sourceNodes{nodes: value.Then, component: body.component, link: body.link}, into)
			into = l.candidates(sourceNodes{nodes: value.Else, component: body.component, link: body.link}, into)
		case *ast.RepetitionNode:
			into = l.candidates(sourceNodes{nodes: value.Body, component: body.component, link: body.link}, into)
		}
	}
	return into
}

// outlet lists the candidates of the content an instance's call supplied for
// the content input named input.
func (l *elementLocator) outlet(body sourceNodes, input string, into []locatedElement) []locatedElement {
	if body.link >= len(l.links) || l.following > 64 {
		return into
	}
	link := l.links[body.link]
	call, ok := l.snapshot.callNode(link.Caller, link.Site)
	if !ok {
		return into
	}
	var content []ast.Node
	for _, child := range call.Children {
		slot, fill := child.(*ast.SlotNode)
		switch {
		case input == ChildrenInput && !fill:
			content = append(content, child)
		case fill && slot.Name == input:
			content = slot.Body
		}
	}
	l.following++
	defer func() { l.following-- }()
	return l.candidates(sourceNodes{nodes: content, component: link.Caller, link: body.link + 1}, into)
}

// match finds the best match of path among the elements body writes.
func (l *elementLocator) match(body sourceNodes, path []ElementStep) *elementMatch {
	if len(path) == 0 {
		return &elementMatch{}
	}
	step := path[0]
	var best *elementMatch
	for _, candidate := range l.candidates(body, nil) {
		score, ok := stepScore(candidate.node, step)
		if !ok {
			continue
		}
		rest := l.match(sourceNodes{nodes: candidate.node.Children, component: candidate.component, link: candidate.link}, path[1:])
		if rest == nil {
			// The element matches, and nothing below it does.
			rest = &elementMatch{elements: make([]locatedElement, len(path)-1), score: -1 << 20}
		}
		total := score + rest.score
		if best == nil || total > best.score {
			best = &elementMatch{elements: append([]locatedElement{candidate}, rest.elements...), score: total}
		}
	}
	if best == nil && strings.EqualFold(step.Tag, "tbody") {
		if rest := l.match(body, path[1:]); rest != nil {
			return &elementMatch{elements: append([]locatedElement{{}}, rest.elements...), score: rest.score}
		}
	}
	return best
}

// stepScore reports whether a markup element can be the page element step,
// and how well it matches: 4 for a matching static id, 1 for each static
// class token.
func stepScore(element *ast.ElementNode, step ElementStep) (int, bool) {
	if !strings.EqualFold(element.Name, step.Tag) {
		return 0, false
	}
	score := 1
	for _, attribute := range element.Attributes {
		if !attribute.IsNamed() {
			continue
		}
		switch strings.ToLower(attribute.Name) {
		case "id":
			value, static := staticAttribute(attribute)
			if !static {
				continue
			}
			if value != step.ID {
				return 0, false
			}
			score += 4
		case "class":
			for _, token := range staticClassTokens(attribute) {
				if !slices.Contains(step.Classes, token) {
					return 0, false
				}
				score++
			}
		}
	}
	return score, true
}

// staticAttribute returns an attribute's value when it has no interpolation.
func staticAttribute(attribute *ast.AttributeNode) (string, bool) {
	if attribute.Quoted == nil {
		return attribute.Value, true
	}
	var value strings.Builder
	for _, segment := range attribute.Quoted.Segments {
		if segment.Kind != ast.LiteralSegment {
			return "", false
		}
		value.WriteString(segment.Literal)
	}
	return value.String(), true
}

// staticClassTokens returns the class tokens of an attribute that no
// interpolation touches: "row row-{id}" has the static token row.
func staticClassTokens(attribute *ast.AttributeNode) []string {
	if attribute.Quoted == nil {
		return strings.Fields(attribute.Value)
	}
	var tokens []string
	segments := attribute.Quoted.Segments
	for index, segment := range segments {
		if segment.Kind != ast.LiteralSegment {
			continue
		}
		fields := strings.Fields(segment.Literal)
		if len(fields) == 0 {
			continue
		}
		literal := segment.Literal
		if index > 0 && !startsWithSpace(literal) {
			fields = fields[1:]
		}
		if index < len(segments)-1 && len(fields) > 0 && !endsWithSpace(literal) {
			fields = fields[:len(fields)-1]
		}
		tokens = append(tokens, fields...)
	}
	return tokens
}

func startsWithSpace(text string) bool {
	return text != "" && strings.TrimLeft(text, " \t\r\n\f") != text
}

func endsWithSpace(text string) bool {
	return text != "" && strings.TrimRight(text, " \t\r\n\f") != text
}
