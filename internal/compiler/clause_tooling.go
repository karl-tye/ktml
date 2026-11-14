package compiler

import (
	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/token"
)

// This file holds editor-tooling helpers for Render Clauses. Like the record
// helpers, they scan a source that may not parse: a clause header is
// recognized by its `name =` first entry, and its group's signature is the
// nearest earlier top-level declaration of the same name in the source.

// isClauseHeader reports whether the bracket at token index opener starts a
// Render Clause header (`Name [selector = ...`).
func isClauseHeader(tokens []lexeme, opener int) bool {
	return opener+2 < len(tokens) && tokens[opener].tok == token.LBRACK &&
		tokens[opener+1].tok == token.IDENT && tokens[opener+2].tok == token.ASSIGN
}

// signatureHead returns the token index of the nearest top-level declaration
// before the clause at head that has the same name and is not itself a clause.
func signatureHead(tokens []lexeme, head int) (int, bool) {
	name := tokens[head].lit
	found := -1
	depth := 0
	for index := 0; index < head; index++ {
		switch tokens[index].tok {
		case token.LBRACK, token.LBRACE:
			depth++
		case token.RBRACK, token.RBRACE:
			if depth > 0 {
				depth--
			}
		case token.IDENT:
			if depth == 0 && tokens[index].lit == name && index+1 < len(tokens) && tokens[index+1].tok == token.LBRACK && !isClauseHeader(tokens, index+1) {
				found = index
			}
		}
	}
	return found, found >= 0
}

// clauseInputs types the signature inputs of the group a clause belongs to.
func clauseInputs(path string, source []byte, tokens []lexeme, head int, visible map[string]visibleType, snapshots []*Snapshot) []Input {
	signature, ok := signatureHead(tokens, head)
	if !ok {
		return nil
	}
	return componentInputs(path, source, tokens, declarationOwner{kind: componentOwner, name: tokens[head].lit, head: signature}, visible, snapshots)
}

// clauseSyntax parses the clause header at head, ignoring its body.
func clauseSyntax(source []byte, tokens []lexeme, head int) (*ast.RenderClause, bool) {
	depth := 0
	end := -1
	for index := head + 1; index < len(tokens) && end < 0; index++ {
		switch tokens[index].tok {
		case token.LBRACK:
			depth++
		case token.RBRACK:
			depth--
			if depth == 0 {
				end = tokens[index].end
			}
		case token.LBRACE, token.RBRACE:
			return nil, false
		}
	}
	if end < 0 {
		return nil, false
	}
	document := recoverParse(append(append([]byte(nil), source[tokens[head].start:end]...), " {}"...))
	if document == nil || len(document.Nodes) == 0 {
		return nil, false
	}
	clause, ok := document.Nodes[0].(*ast.RenderClause)
	return clause, ok
}

// selectorState returns the state a non-optional state selector matches.
func selectorState(selector Input, snapshots []*Snapshot) (State, bool) {
	if selector.Mode == OptionalInput || selector.Type.Kind != StateTypeKind {
		return State{}, false
	}
	return lookupState(selector.Type.State, snapshots)
}

// clausePayload returns the payload fields a Pattern exposes to bindings: the
// matched Variant's fields, or the built-in value of Some.
func clausePayload(selector Input, qualifier, name string, visible map[string]visibleType, snapshots []*Snapshot) []Field {
	if selector.Mode == OptionalInput {
		if qualifier == "" && name == SomeCase {
			return []Field{{Name: "value", Type: selector.Type}}
		}
		return nil
	}
	state, ok := selectorState(selector, snapshots)
	if !ok {
		return nil
	}
	if qualifier != "" {
		if entry, ok := visible[qualifier]; !ok || entry.id != state.ID {
			return nil
		}
	}
	variant, ok := state.Variant(name)
	if !ok {
		return nil
	}
	return variant.Fields
}

// pathRoot is a name a body path may start with.
type pathRoot struct {
	name     string
	typ      Type
	optional bool
	kind     CompletionKind
	// narrowed holds the narrowed field paths rooted at this root.
	narrowed []string
}

// bodyRoots returns the path roots of the body owned by a component or clause
// declaration: the inputs, plus a clause's bindings.
func bodyRoots(path string, source []byte, tokens []lexeme, owner declarationOwner, visible map[string]visibleType, snapshots []*Snapshot) []pathRoot {
	clause := isClauseHeader(tokens, owner.head+1)
	var inputs []Input
	if clause {
		inputs = clauseInputs(path, source, tokens, owner.head, visible, snapshots)
	} else {
		inputs = componentInputs(path, source, tokens, owner, visible, snapshots)
	}
	roots := make([]pathRoot, 0, len(inputs))
	for _, input := range inputs {
		roots = append(roots, pathRoot{name: input.Name, typ: input.Type, optional: input.Mode == OptionalInput, kind: InputCompletion})
	}
	if !clause {
		return roots
	}
	syntax, ok := clauseSyntax(source, tokens, owner.head)
	if !ok {
		return roots
	}
	selector, ok := findInput(inputs, syntax.Selector)
	if !ok {
		return roots
	}
	payload := clausePayload(selector, syntax.Pattern.Qualifier, syntax.Pattern.Name, visible, snapshots)
	for _, binding := range syntax.Bindings {
		for _, field := range payload {
			if field.Name == binding.Field {
				roots = append(roots, pathRoot{name: binding.LocalName(), typ: field.Type, optional: field.Optional, kind: BindingCompletion})
			}
		}
	}
	return roots
}

func findInput(inputs []Input, name string) (Input, bool) {
	for _, input := range inputs {
		if input.Name == name {
			return input, true
		}
	}
	return Input{}, false
}

// clauseHeaderCompletions completes Patterns after `selector =` (optionally
// qualified) and payload fields in binding position of a clause header.
func clauseHeaderCompletions(path string, source []byte, tokens []lexeme, before []int, frame bracketFrame, prefixStart int, snapshots []*Snapshot) (CompletionList, bool) {
	var entries []lexeme
	for _, index := range before {
		if index > frame.opener {
			entries = append(entries, tokens[index])
		}
	}
	list := CompletionList{Start: prefixStart}
	patternPosition := len(entries) == 2
	qualified := len(entries) == 4 && entries[2].tok == token.IDENT && entries[3].tok == token.PERIOD
	bindingPosition := len(entries) >= 3 && entries[len(entries)-1].tok == token.COMMA && entries[2].tok == token.IDENT
	if !patternPosition && !qualified && !bindingPosition {
		return CompletionList{}, false
	}
	visible := textualTypes(path, source, tokens, snapshots)
	head := frame.owner.head
	selector, ok := findInput(clauseInputs(path, source, tokens, head, visible, snapshots), entries[0].lit)
	if !ok {
		return list, true
	}
	if patternPosition || qualified {
		if state, ok := selectorState(selector, snapshots); ok {
			if qualified {
				if entry, ok := visible[entries[2].lit]; !ok || entry.id != state.ID {
					return list, true
				}
			}
			for _, variant := range state.Variants {
				list.Items = append(list.Items, Completion{Label: variant.Name, Detail: VariantSignature(state, variant), Kind: VariantCompletion})
			}
			return list, true
		}
		if qualified {
			return list, true
		}
		var cases []string
		switch {
		case selector.Mode == OptionalInput:
			cases = []string{SomeCase, NoneCase}
		case selector.Type.IsScalar(BooleanType):
			cases = []string{TrueCase, FalseCase}
		}
		for _, name := range cases {
			list.Items = append(list.Items, Completion{Label: name, Detail: selectorTypeName(selector), Kind: VariantCompletion})
		}
		return list, true
	}
	qualifier, name := "", entries[2].lit
	if len(entries) > 4 && entries[3].tok == token.PERIOD && entries[4].tok == token.IDENT {
		qualifier, name = entries[2].lit, entries[4].lit
	}
	bound := make(map[string]bool)
	for index := frame.opener + 1; index < len(tokens) && tokens[index].tok != token.RBRACK && tokens[index].tok != token.LBRACE; index++ {
		current := tokens[index]
		previous := tokens[index-1]
		if current.tok == token.IDENT && previous.tok == token.COMMA && current.start != prefixStart {
			bound[current.lit] = true
		}
	}
	for _, field := range clausePayload(selector, qualifier, name, visible, snapshots) {
		if !bound[field.Name] {
			list.Items = append(list.Items, Completion{Label: field.Name, Detail: field.Type.String(), Kind: FieldCompletion})
		}
	}
	return list, true
}

// BindingOccurrence is a Render Clause binding or repetition item under a
// cursor: its declaring name, or a path root that names it.
type BindingOccurrence struct {
	Binding Binding
	Range   SourceRange
	// Root marks a body path root rather than the header declaration.
	Root bool
}

// BindingOccurrenceAt resolves the binding declared at, or used as a path root
// at, offset.
func (s *Snapshot) BindingOccurrenceAt(file string, offset int) (BindingOccurrence, bool) {
	if part, ok := s.PathPartAt(file, offset); ok && part.Binding != "" {
		if binding, ok := s.BindingAt(file, part.BindingRange.Start); ok && binding.Range == part.BindingRange {
			return BindingOccurrence{Binding: binding, Range: part.Range, Root: true}, true
		}
	}
	if binding, ok := s.BindingAt(file, offset); ok {
		return BindingOccurrence{Binding: binding, Range: binding.Range}, true
	}
	return s.itemOccurrenceAt(file, offset)
}

// BindingReferences returns the body path roots that name a binding. For a
// repetition item they are its key root and body path roots, including paths
// that did not type.
func (s *Snapshot) BindingReferences(binding Binding) []SourceRange {
	if repetition, ok := s.repetitionOfItem(binding); ok {
		return repetitionItemReferences(repetition.File, repetition.syntax)
	}
	var ranges []SourceRange
	for _, resolved := range s.Paths(binding.Range.File) {
		if root := resolved.Parts[0]; root.Binding != "" && root.BindingRange == binding.Range {
			ranges = append(ranges, root.Range)
		}
	}
	return ranges
}

// BindingSignature renders a binding for hover text, followed by the payload
// field it binds when that field is a declared symbol, or by the repetition
// header that declares an item.
func (s *Snapshot) BindingSignature(binding Binding) string {
	if binding.Repetition != "" {
		return s.itemSignature(binding)
	}
	optional := ""
	if binding.Optional {
		optional = "?"
	}
	signature := "binding " + binding.Name + optional + ": " + binding.Type.String()
	if binding.FieldID != "" {
		if symbol, ok := s.Symbol(binding.FieldID); ok {
			signature += "\n" + symbol.Detail
		}
	}
	return signature
}
