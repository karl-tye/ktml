package compiler

import (
	"fmt"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/parser"
	"github.com/karl-tye/ktml/internal/token"
)

// Editor tooling for content inputs and conditions. Completion scans a source
// that may not parse; navigation and rename use the snapshot's content uses.

const (
	// SlotCompletion is a named slot offered after slot in a call body.
	SlotCompletion CompletionKind = "slot"
	// ContentTypeCompletion is the content type in input type position.
	ContentTypeCompletion CompletionKind = "content"
)

// declaredContent returns the content input names of a declaration: its
// explicit content inputs plus children.
func declaredContent(declaration *ast.ElementDecl) map[string]bool {
	names := map[string]bool{ChildrenInput: true}
	for _, attribute := range declaration.Attributes {
		if attribute != nil && attribute.Kind == ast.InputDeclaration && attribute.TypeDecl == ContentTypeName {
			names[attribute.Name] = true
		}
	}
	return names
}

// ContentOccurrence is a content input at an outlet, a slot fill name, or its
// declaration.
type ContentOccurrence struct {
	Component Component
	Input     Input
	Range     SourceRange
}

// ContentOccurrenceAt returns the content input whose outlet, fill name, or
// declaration name is at offset.
func (s *Snapshot) ContentOccurrenceAt(file string, offset int) (ContentOccurrence, bool) {
	if s == nil {
		return ContentOccurrence{}, false
	}
	if use, ok := s.ContentUseAt(file, offset); ok {
		component, found := s.components[use.Component]
		if input, exists := inputNamed(component, use.Input); found && exists {
			return ContentOccurrence{Component: component, Input: input, Range: use.Range}, true
		}
	}
	path, err := canonicalPath(file)
	if err != nil {
		return ContentOccurrence{}, false
	}
	for _, resolved := range s.paths[path] {
		root := resolved.Parts[0]
		if root.Input == "" || root.Type.Kind != ContentTypeKind || offset < root.Range.Start || offset > root.Range.End {
			continue
		}
		component, found := s.components[resolved.Component]
		if input, exists := inputNamed(component, root.Input); found && exists {
			return ContentOccurrence{Component: component, Input: input, Range: root.Range}, true
		}
	}
	for _, component := range s.components {
		if component.File != path {
			continue
		}
		for _, input := range component.Inputs {
			if input.IsContent() && !input.Implicit && offset >= input.NameRange.Start && offset <= input.NameRange.End {
				return ContentOccurrence{Component: component, Input: input, Range: input.NameRange}, true
			}
		}
	}
	return ContentOccurrence{}, false
}

// bodyKeywords start body nodes, so an outlet cannot be spelled with them.
var bodyKeywords = map[string]bool{"if": true, "else": true, "for": true, "slot": true}

// ContentRename returns every range a rename of a content input must edit:
// the declaration, its outlets, and the slot fills of every call. children
// and implicit inputs cannot be renamed, and the new name may not be children
// or another input of the component.
func (s *Snapshot) ContentRename(occurrence ContentOccurrence, newName string) ([]SourceRange, error) {
	input := occurrence.Input
	if input.Implicit || input.Name == ChildrenInput {
		return nil, fmt.Errorf("children cannot be renamed")
	}
	if newName == ChildrenInput || bodyKeywords[newName] || !ValidItemName(newName) || !validGeneratedGoField(GeneratedGoField(newName)) {
		return nil, fmt.Errorf("content input name %s must be an identifier other than children", newName)
	}
	if newName == input.Name {
		return nil, fmt.Errorf("content input is already named %s", newName)
	}
	if _, exists := inputNamed(occurrence.Component, newName); exists {
		return nil, fmt.Errorf("%s is already an input of %s", newName, occurrence.Component.Name)
	}
	ranges := append([]SourceRange{input.NameRange}, s.ContentUsesOf(occurrence.Component.ID, input.Name)...)
	return uniqueRanges(ranges), nil
}

// ContentUsesOf returns every outlet, slot fill, and condition path root of
// one content input across the workspace.
func (s *Snapshot) ContentUsesOf(component SymbolID, input string) []SourceRange {
	ranges := s.ContentReferences(component, input)
	for _, paths := range s.paths {
		for _, resolved := range paths {
			if root := resolved.Parts[0]; resolved.Component == component && root.Input == input && root.Type.Kind == ContentTypeKind {
				ranges = append(ranges, root.Range)
			}
		}
	}
	return uniqueRanges(ranges)
}

// slotCompletions completes slot names after `slot` at the start of a body
// node in a component call body: the called component's named content inputs
// that the call does not fill yet.
func slotCompletions(path string, source []byte, tokens []lexeme, stack []bracketFrame, end, prefixStart int, snapshots []*Snapshot) (CompletionList, bool) {
	if len(stack) < 2 || stack[0].owner.kind != componentOwner || end < 2 {
		return CompletionList{}, false
	}
	keyword := tokens[end-1]
	if keyword.tok != token.IDENT || keyword.lit != "slot" {
		return CompletionList{}, false
	}
	if previous := tokens[end-2]; previous.tok != token.LBRACE && previous.tok != token.RBRACE && !newlineBetween(source, previous.end, keyword.start) {
		return CompletionList{}, false
	}
	frame := stack[len(stack)-1]
	if frame.tok != token.LBRACE {
		return CompletionList{}, false
	}
	name, ok := callNameBefore(source, tokens, frame.opener)
	if !ok {
		return CompletionList{}, false
	}
	component, ok := textualComponent(path, tokens, name, snapshots)
	if !ok {
		return CompletionList{}, false
	}
	filled := filledSlots(tokens, frame.opener)
	list := CompletionList{Start: prefixStart, Items: []Completion{}}
	for _, input := range component.Inputs {
		if !input.IsContent() || input.Name == ChildrenInput || filled[input.Name] {
			continue
		}
		list.Items = append(list.Items, Completion{Label: input.Name, Detail: ContentSignature(component, input), Kind: SlotCompletion})
	}
	return list, true
}

// textualComponent resolves a component name in an edited source that may not
// parse: through the snapshots, as a local declaration, or through the
// source's own import statements.
func textualComponent(path string, tokens []lexeme, name string, snapshots []*Snapshot) (Component, bool) {
	if component, ok := resolveComponent(path, name, snapshots); ok {
		return component, true
	}
	candidates := []SymbolID{componentID(path, name)}
	for index := 0; index < len(tokens); index++ {
		if tokens[index].tok != token.IMPORT {
			continue
		}
		named := false
		for index++; index < len(tokens) && tokens[index].tok != token.FROM; index++ {
			if tokens[index].tok == token.IDENT && tokens[index].lit == name {
				named = true
			}
		}
		if named && index+1 < len(tokens) && tokens[index+1].tok == token.STRING {
			if dependency, err := resolveImportPath(path, tokens[index+1].lit); err == nil {
				candidates = append(candidates, componentID(dependency, name))
			}
		}
	}
	for _, snapshot := range snapshots {
		for _, id := range candidates {
			if component, ok := snapshot.Component(id); ok {
				return component, true
			}
		}
	}
	return Component{}, false
}

func newlineBetween(source []byte, start, end int) bool {
	return start < end && end <= len(source) && strings.IndexByte(string(source[start:end]), '\n') >= 0
}

// callNameBefore returns the element name of the header whose body opens at
// token index opener: the name before a continuation group, or the first
// token of the header line.
func callNameBefore(source []byte, tokens []lexeme, opener int) (string, bool) {
	index := opener - 1
	if index < 0 {
		return "", false
	}
	if tokens[index].tok == token.RPAREN {
		depth := 0
		for ; index >= 0; index-- {
			if tokens[index].tok == token.RPAREN {
				depth++
			} else if tokens[index].tok == token.LPAREN {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		index--
		if index < 0 || tokens[index].tok != token.IDENT {
			return "", false
		}
		return tokens[index].lit, true
	}
	for index > 0 {
		previous := tokens[index-1]
		if previous.tok == token.LBRACE || previous.tok == token.RBRACE || newlineBetween(source, previous.end, tokens[index].start) {
			break
		}
		index--
	}
	if tokens[index].tok != token.IDENT {
		return "", false
	}
	return tokens[index].lit, true
}

// filledSlots returns the slot names filled directly in the body opening at
// token index opener.
func filledSlots(tokens []lexeme, opener int) map[string]bool {
	filled := make(map[string]bool)
	depth := 0
	for index := opener; index < len(tokens); index++ {
		switch tokens[index].tok {
		case token.LBRACE:
			depth++
		case token.RBRACE:
			depth--
			if depth == 0 {
				return filled
			}
		case token.IDENT:
			if depth == 1 && tokens[index].lit == "slot" && index+2 < len(tokens) && tokens[index+1].tok == token.IDENT && tokens[index+2].tok == token.LBRACE {
				filled[tokens[index+1].lit] = true
			}
		}
	}
	return filled
}

// conditionStart returns the token index of the `if` whose header contains
// token index last, or false when last is not inside a condition header.
func conditionStart(tokens []lexeme, last int) (int, bool) {
	for index := last; index >= 0; index-- {
		switch tokens[index].tok {
		case token.LBRACE, token.RBRACE, token.STRING:
			return 0, false
		case token.IDENT:
			if tokens[index].lit == "if" {
				return index, true
			}
		}
	}
	return 0, false
}

// conditionCompletions completes path roots and fields in a condition header
// after if, !, &&, ||, or (.
func conditionCompletions(path string, source []byte, tokens []lexeme, stack []bracketFrame, end, prefixStart int, snapshots []*Snapshot) (CompletionList, bool) {
	if len(stack) == 0 || stack[0].tok != token.LBRACE || stack[0].owner.kind != componentOwner || end < 1 {
		return CompletionList{}, false
	}
	var qualifier []string
	operator := end - 1
	if tokens[operator].tok == token.PERIOD {
		if tokens[operator].end != prefixStart {
			return CompletionList{}, false
		}
		parts, first, ok := readPathBack(tokens, operator-1)
		if !ok || tokens[operator-1].end != tokens[operator].start {
			return CompletionList{}, false
		}
		qualifier, operator = parts, first-1
	}
	if operator < 0 {
		return CompletionList{}, false
	}
	current := tokens[operator]
	switch {
	case current.tok == token.IDENT && current.lit == "if":
	case current.tok == token.NOT, current.tok == token.LAND, current.tok == token.LOR, current.tok == token.LPAREN:
	default:
		return CompletionList{}, false
	}
	start, ok := conditionStart(tokens, operator)
	if !ok {
		return CompletionList{}, false
	}
	visible := textualTypes(path, source, tokens, snapshots)
	roots := scopeRoots(path, source, tokens, stack, visible, snapshots)
	if current.tok == token.LAND || current.tok == token.LOR {
		if prefix, ok := parseCondition(source[tokens[start].end:current.start]); ok {
			whenTrue, whenFalse := ConditionNarrowing(prefix)
			if current.tok == token.LOR {
				whenTrue = whenFalse
			}
			roots = narrowRoots(roots, whenTrue)
		}
	}
	list := CompletionList{Start: prefixStart}
	if qualifier == nil {
		list.Items = rootCompletions(roots)
	} else {
		list.Items = fieldCompletions(roots, qualifier, snapshots)
	}
	return list, true
}

// parseCondition parses condition source text on its own.
func parseCondition(text []byte) (condition *ast.ConditionExpr, ok bool) {
	defer func() {
		if recover() != nil {
			condition, ok = nil, false
		}
	}()
	p := parser.New([]byte("X [] {\nif " + string(text) + " {}\n}\n"))
	document := p.ParseDocument()
	if p.Err() != nil || len(document.Nodes) != 1 {
		return nil, false
	}
	declaration, isDeclaration := document.Nodes[0].(*ast.ElementDecl)
	if !isDeclaration || len(declaration.Body) != 1 {
		return nil, false
	}
	node, isCondition := declaration.Body[0].(*ast.ConditionNode)
	if !isCondition {
		return nil, false
	}
	return node.Condition, true
}

// branchNarrowing returns the paths narrowed in the branch body that opens at
// token index opener: the true set of its if header, or the false set of the
// if before an else, plus the false sets of earlier else-if links.
func branchNarrowing(source []byte, tokens []lexeme, opener int) []string {
	if opener < 1 {
		return nil
	}
	if previous := tokens[opener-1]; previous.tok == token.IDENT && previous.lit == "else" {
		return elseNarrowing(source, tokens, opener-1)
	}
	start, ok := conditionStart(tokens, opener-1)
	if !ok {
		return nil
	}
	condition, ok := parseCondition(source[tokens[start].end:tokens[opener].start])
	if !ok {
		return nil
	}
	whenTrue, _ := ConditionNarrowing(condition)
	if start > 0 && tokens[start-1].tok == token.IDENT && tokens[start-1].lit == "else" {
		whenTrue = unionPaths(whenTrue, elseNarrowing(source, tokens, start-1))
	}
	return whenTrue
}

// elseNarrowing returns the false set of the if chain ending before the else
// at token index keyword.
func elseNarrowing(source []byte, tokens []lexeme, keyword int) []string {
	closing := keyword - 1
	if closing < 0 || tokens[closing].tok != token.RBRACE {
		return nil
	}
	depth := 0
	opener := -1
	for index := closing; index >= 0; index-- {
		if tokens[index].tok == token.RBRACE {
			depth++
		} else if tokens[index].tok == token.LBRACE {
			depth--
			if depth == 0 {
				opener = index
				break
			}
		}
	}
	if opener < 1 {
		return nil
	}
	start, ok := conditionStart(tokens, opener-1)
	if !ok {
		return nil
	}
	condition, ok := parseCondition(source[tokens[start].end:tokens[opener].start])
	if !ok {
		return nil
	}
	_, whenFalse := ConditionNarrowing(condition)
	if start > 0 && tokens[start-1].tok == token.IDENT && tokens[start-1].lit == "else" {
		whenFalse = unionPaths(whenFalse, elseNarrowing(source, tokens, start-1))
	}
	return whenFalse
}

// narrowRoots marks narrowed roots present and records the narrowed paths
// each root's fields may use.
func narrowRoots(roots []pathRoot, narrowed []string) []pathRoot {
	if len(narrowed) == 0 {
		return roots
	}
	result := make([]pathRoot, len(roots))
	for index, root := range roots {
		for _, spelling := range narrowed {
			if spelling == root.name {
				root.optional = false
			}
			if strings.HasPrefix(spelling, root.name+".") {
				root.narrowed = append(append([]string(nil), root.narrowed...), spelling)
			}
		}
		result[index] = root
	}
	return result
}
